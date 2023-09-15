package abci_client

import (
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/barkimedes/go-deepcopy"
	db "github.com/cometbft/cometbft-db"
	abcitypes "github.com/cometbft/cometbft/abci/types"
	cryptoenc "github.com/cometbft/cometbft/crypto/encoding"
	"github.com/cometbft/cometbft/crypto/merkle"
	cometlog "github.com/cometbft/cometbft/libs/log"
	cmtmath "github.com/cometbft/cometbft/libs/math"
	cmtstate "github.com/cometbft/cometbft/proto/tendermint/state"
	cmttypes "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/cometbft/cometbft/state"
	blockindexkv "github.com/cometbft/cometbft/state/indexer/block/kv"
	"github.com/cometbft/cometbft/state/txindex"
	indexerkv "github.com/cometbft/cometbft/state/txindex/kv"
	"github.com/cometbft/cometbft/types"
	"github.com/informalsystems/CometMock/cometmock/storage"
	"github.com/informalsystems/CometMock/cometmock/utils"
)

var GlobalClient *AbciClient

// store a mutex that allows only running one block at a time
var blockMutex = sync.Mutex{}

// store a mutex that disallows accessing state information while it is being updated
var stateUpdateMutex = sync.Mutex{}

var verbose = false

type MisbehaviourType int

const (
	DuplicateVote MisbehaviourType = iota
	Lunatic
	Amnesia
	Equivocation
)

// AbciClient facilitates calls to the ABCI interface of multiple nodes.
// It also tracks the current state and a common logger.
type AbciClient struct {
	Clients []AbciCounterpartyClient

	Logger         cometlog.Logger
	CurState       state.State
	EventBus       types.EventBus
	LastBlock      *types.Block
	LastCommit     *types.Commit
	Storage        storage.Storage
	PrivValidators map[string]types.PrivValidator // maps validator addresses to their priv validator structs
	IndexerService *txindex.IndexerService
	TxIndex        *indexerkv.TxIndex
	BlockIndex     *blockindexkv.BlockerIndexer

	// if this is true, then an error will be returned if the responses from the clients are not all equal.
	// can be used to check for nondeterminism in apps, but also slows down execution a bit,
	// though performance difference was not measured.
	ErrorOnUnequalResponses bool

	// validator addresses are mapped to false if they should not be signing, and to true if they should
	signingStatus      map[string]bool
	signingStatusMutex sync.RWMutex

	// time offset. whenever we qury the time, we add this offset to it
	// this means after modifying this, blocks will have the timestamp offset by this value.
	// this will look to the app like one block took a long time to be produced.
	timeOffset time.Duration
}

func (a *AbciClient) GetTimeOffset() time.Duration {
	return a.timeOffset
}

func (a *AbciClient) IncrementTimeOffset(additionalOffset time.Duration) error {
	if additionalOffset < 0 {
		a.Logger.Error("time offset cannot be decremented, please provide a positive offset")
		return fmt.Errorf("time offset cannot be decremented, please provide a positive offset")
	}
	a.Logger.Debug("Incrementing time offset", "additionalOffset", additionalOffset.String())
	a.timeOffset = a.timeOffset + additionalOffset
	return nil
}

func (a *AbciClient) CauseLightClientAttack(address string, misbehaviourType string) error {
	a.Logger.Info("Causing double sign", "address", address)

	validator, err := a.GetValidatorFromAddress(address)
	if err != nil {
		return err
	}

	// get the misbehaviour type from the string
	var misbehaviour MisbehaviourType
	switch misbehaviourType {
	case "Lunatic":
		misbehaviour = Lunatic
	case "Amnesia":
		misbehaviour = Amnesia
	case "Equivocation":
		misbehaviour = Equivocation
	default:
		return fmt.Errorf("unknown misbehaviour type %s, possible types are: Equivocation, Lunatic, Amnesia", misbehaviourType)
	}

	_, _, _, _, _, err = a.RunBlockWithEvidence(nil, map[*types.Validator]MisbehaviourType{validator: misbehaviour})
	return err
}

func (a *AbciClient) CauseDoubleSign(address string) error {
	a.Logger.Info("Causing double sign", "address", address)

	validator, err := a.GetValidatorFromAddress(address)
	if err != nil {
		return err
	}

	_, _, _, _, _, err = a.RunBlockWithEvidence(nil, map[*types.Validator]MisbehaviourType{validator: DuplicateVote})
	return err
}

func (a *AbciClient) GetValidatorFromAddress(address string) (*types.Validator, error) {
	for _, validator := range a.CurState.Validators.Validators {
		if validator.Address.String() == address {
			return validator, nil
		}
	}
	return nil, fmt.Errorf("validator with address %s not found", address)
}

// GetSigningStatusMap gets a copy of the signing status map that can be used for reading.
func (a *AbciClient) GetSigningStatusMap() map[string]bool {
	a.signingStatusMutex.RLock()
	defer a.signingStatusMutex.RUnlock()

	statusMap := make(map[string]bool, len(a.signingStatus))
	for k, v := range a.signingStatus {
		statusMap[k] = v
	}
	return statusMap
}

// GetSigningStatus gets the signing status of the given address.
func (a *AbciClient) GetSigningStatus(address string) (bool, error) {
	a.signingStatusMutex.RLock()
	defer a.signingStatusMutex.RUnlock()

	status, ok := a.signingStatus[address]
	if !ok {
		return false, fmt.Errorf("address %s not found in signing status map, please double-check this is the key address of a validator key", address)
	}
	return status, nil
}

func (a *AbciClient) SetSigningStatus(address string, status bool) error {
	a.signingStatusMutex.Lock()
	defer a.signingStatusMutex.Unlock()

	_, ok := a.signingStatus[address]
	if !ok {
		return fmt.Errorf("address %s not found in signing status map, please double-check this is the key address of a validator key", address)
	}
	a.signingStatus[address] = status

	a.Logger.Info("Set signing status", "address", address, "status", status)

	return nil
}

func CreateAndStartEventBus(logger cometlog.Logger) (*types.EventBus, error) {
	eventBus := types.NewEventBus()
	eventBus.SetLogger(logger.With("module", "events"))
	if err := eventBus.Start(); err != nil {
		return nil, err
	}
	return eventBus, nil
}

func CreateAndStartIndexerService(eventBus *types.EventBus, logger cometlog.Logger) (*txindex.IndexerService, *indexerkv.TxIndex, *blockindexkv.BlockerIndexer, error) {
	txIndexer := indexerkv.NewTxIndex(db.NewMemDB())
	blockIndexer := blockindexkv.New(db.NewMemDB())

	indexerService := txindex.NewIndexerService(txIndexer, blockIndexer, eventBus, false)
	indexerService.SetLogger(logger.With("module", "txindex"))

	return indexerService, txIndexer, blockIndexer, indexerService.Start()
}

func NewAbciClient(clients []AbciCounterpartyClient, logger cometlog.Logger, curState state.State, lastBlock *types.Block, lastCommit *types.Commit, storage storage.Storage, privValidators map[string]types.PrivValidator, errorOnUnequalResponses bool) *AbciClient {
	signingStatus := make(map[string]bool)
	for addr := range privValidators {
		signingStatus[addr] = true
	}

	eventBus, err := CreateAndStartEventBus(logger)
	if err != nil {
		logger.Error(err.Error())
		panic(err)
	}

	indexerService, txIndex, blockIndex, err := CreateAndStartIndexerService(eventBus, logger)
	if err != nil {
		logger.Error(err.Error())
		panic(err)
	}

	return &AbciClient{
		Clients:                 clients,
		Logger:                  logger,
		CurState:                curState,
		EventBus:                *eventBus,
		LastBlock:               lastBlock,
		LastCommit:              lastCommit,
		Storage:                 storage,
		PrivValidators:          privValidators,
		IndexerService:          indexerService,
		TxIndex:                 txIndex,
		BlockIndex:              blockIndex,
		ErrorOnUnequalResponses: errorOnUnequalResponses,
		signingStatus:           signingStatus,
	}
}

func (a *AbciClient) RetryDisconnectedClients() {
	a.Logger.Info("Retrying disconnected clients")
	for i, client := range a.Clients {
		if !client.isConnected {
			infoRes, err := a.callClientWithTimeout(client, func(c AbciCounterpartyClient) (interface{}, error) {
				return c.Client.InfoAsync(abcitypes.RequestInfo{}), nil
			}, 500*time.Millisecond)

			if err != nil {
				if unreachableErr, ok := err.(*ClientUnreachableError); ok {
					a.Logger.Error(unreachableErr.Error())
				} else {
					a.Logger.Error("Error calling client at address %v: %v", client.NetworkAddress, err)
				}
			} else {
				client.isConnected = true
				a.Clients[i] = client
				// resync the app state
				// infoRes.(abcitypes.ResponseInfo).LastBlockHeight
				_ = infoRes
			}
		}
	}
}

func (a *AbciClient) SyncApp(startHeight int64, client AbciCounterpartyClient) error {
	return nil
}

type ClientUnreachableError struct {
	Address string
}

func (e *ClientUnreachableError) Error() string {
	return fmt.Sprintf("client at address %v is unavailable", e.Address)
}

// callClientsWithTimeout calls the given function on all clients and returns the results.
// If a client does not respond within the given timeout, it is set to not connected.
// If a client is not connected, the function is not called and nil is returned.
func (a *AbciClient) callClientsWithTimeout(f func(AbciCounterpartyClient) (interface{}, error), timeout time.Duration) ([]interface{}, error) {
	results := make([]interface{}, 0)

	for i, client := range a.Clients {
		if !client.isConnected {
			// do not call the client if it is not connected
			continue
		}
		result, err := a.callClientWithTimeout(client, f, timeout)
		if err != nil {
			if unreachableErr, ok := err.(*ClientUnreachableError); ok {
				a.Logger.Error(unreachableErr.Error())
			} else {
				// handle other errors
				a.Logger.Error("Error calling client at address %v: %v", client.NetworkAddress, err)
			}
			client.isConnected = false
			a.Clients[i] = client
			continue
		}
		results = append(results, result)
	}

	return results, nil
}

// callClientWithTimeout calls the given function on the given client and returns the result.
// If the client does not respond within the given timeout, it is set to not connected, and
// a ClientUnreachableError is returned
// An error is returned if the client responds with an error.
func (a *AbciClient) callClientWithTimeout(client AbciCounterpartyClient, f func(AbciCounterpartyClient) (interface{}, error), timeout time.Duration) (interface{}, error) {
	done := make(chan struct{})
	var result interface{}
	var err error

	go func() {
		result, err = f(client)
		close(done)
	}()

	select {
	case <-done:
		// Call completed within timeout, return the result
		return result, err
	case <-time.After(timeout):
		a.Logger.Info("Client did not respond", "networkAddress", client.NetworkAddress, "timeout", timeout)
		return nil, &ClientUnreachableError{Address: client.NetworkAddress}
	}
}

func (a *AbciClient) SendABCIInfo() (*abcitypes.ResponseInfo, error) {
	if verbose {
		a.Logger.Info("Sending Info to clients")
	}
	// send Info to all clients and collect the responses
	f := func(client AbciCounterpartyClient) (interface{}, error) {
		return client.Client.InfoSync(abcitypes.RequestInfo{})
	}
	responses, err := a.callClientsWithTimeout(f, 500*time.Millisecond)
	if err != nil {
		return nil, err
	}

	if a.ErrorOnUnequalResponses {
		// return an error if the responses are not all equal
		for i := 1; i < len(responses); i++ {
			if !reflect.DeepEqual(responses[i], responses[0]) {
				return nil, fmt.Errorf("responses are not all equal: %v is not equal to %v", responses[i], responses[0])
			}
		}
	}

	return responses[0].(*abcitypes.ResponseInfo), nil
}

func (a *AbciClient) SendBeginBlock(block *types.Block) (*abcitypes.ResponseBeginBlock, error) {
	if verbose {
		a.Logger.Info("Sending BeginBlock to clients")
	}

	// build the BeginBlock request. evidence from block is taken to build misbehavior list
	byzVals := make([]abcitypes.Evidence, 0)
	for _, evidence := range block.Evidence.Evidence {
		byzVals = append(byzVals, evidence.ABCI()...)
	}
	beginBlockRequest := a.CreateBeginBlockRequest(&block.Header, block.LastCommit, byzVals)

	// send BeginBlock to all clients and collect the responses
	f := func(client AbciCounterpartyClient) (interface{}, error) {
		return client.Client.BeginBlockSync(*beginBlockRequest)
	}
	responses, err := a.callClientsWithTimeout(f, 500*time.Millisecond)
	if err != nil {
		return nil, err
	}

	if a.ErrorOnUnequalResponses {
		// return an error if the responses are not all equal
		for i := 1; i < len(responses); i++ {
			if !reflect.DeepEqual(responses[i], responses[0]) {
				return nil, fmt.Errorf("responses are not all equal: %v is not equal to %v", responses[i], responses[0])
			}
		}
	}

	return responses[0].(*abcitypes.ResponseBeginBlock), nil
}

func (a *AbciClient) CreateBeginBlockRequest(header *types.Header, lastCommit *types.Commit, misbehavior []abcitypes.Evidence) *abcitypes.RequestBeginBlock {
	commitSigs := lastCommit.Signatures

	// if this is the first block, LastCommitInfo.Votes will be empty, see https://github.com/cometbft/cometbft/blob/release/v0.34.24/state/execution.go#L342
	voteInfos := make([]abcitypes.VoteInfo, len(commitSigs))
	if lastCommit.Height != 0 {
		for i := range commitSigs {
			val := a.CurState.LastValidators.Validators[i]
			byteAddress := val.Address.Bytes()

			abciVal := abcitypes.Validator{
				Address: byteAddress,
				Power:   val.VotingPower,
			}

			signedLastBlock := !commitSigs[i].Absent()

			voteInfos[i] = abcitypes.VoteInfo{
				Validator:       abciVal,
				SignedLastBlock: signedLastBlock,
			}
		}
	}

	return &abcitypes.RequestBeginBlock{
		// TODO: fill in Votes
		LastCommitInfo:      abcitypes.LastCommitInfo{Round: lastCommit.Round, Votes: voteInfos},
		Header:              *header.ToProto(),
		ByzantineValidators: misbehavior,
	}
}

func (a *AbciClient) SendInitChain(genesisState state.State, genesisDoc *types.GenesisDoc) error {
	if verbose {
		a.Logger.Info("Sending InitChain to clients")
	}
	// build the InitChain request
	initChainRequest := CreateInitChainRequest(genesisState, genesisDoc)

	// send InitChain to all clients and collect the responses
	f := func(client AbciCounterpartyClient) (interface{}, error) {
		return client.Client.InitChainSync(*initChainRequest)
	}
	responses, err := a.callClientsWithTimeout(f, 20*time.Second)
	if err != nil {
		return err
	}

	if a.ErrorOnUnequalResponses {
		// return an error if the responses are not all equal
		for i := 1; i < len(responses); i++ {
			if !reflect.DeepEqual(responses[i], responses[0]) {
				return fmt.Errorf("responses are not all equal: %v is not equal to %v", responses[i], responses[0])
			}
		}
	}

	// update the state
	err = a.UpdateStateFromInit(responses[0].(*abcitypes.ResponseInitChain))
	if err != nil {
		return err
	}

	return nil
}

func CreateInitChainRequest(genesisState state.State, genesisDoc *types.GenesisDoc) *abcitypes.RequestInitChain {
	consensusParams := types.TM2PB.ConsensusParams(&genesisState.ConsensusParams)

	genesisValidators := genesisDoc.Validators

	validators := make([]*types.Validator, len(genesisValidators))
	for i, val := range genesisValidators {
		validators[i] = types.NewValidator(val.PubKey, val.Power)
	}
	validatorSet := types.NewValidatorSet(validators)
	nextVals := types.TM2PB.ValidatorUpdates(validatorSet)

	initChainRequest := abcitypes.RequestInitChain{
		Validators:      nextVals,
		InitialHeight:   genesisState.InitialHeight,
		Time:            genesisDoc.GenesisTime,
		ChainId:         genesisState.ChainID,
		ConsensusParams: consensusParams,
		AppStateBytes:   genesisDoc.AppState,
	}
	return &initChainRequest
}

func (a *AbciClient) UpdateStateFromInit(res *abcitypes.ResponseInitChain) error {
	// if response contained a non-empty app hash, update the app hash, otherwise we keep the one from the genesis file
	if len(res.AppHash) > 0 {
		a.CurState.AppHash = res.AppHash
	}

	// if response specified validators, update the validators, otherwise we keep the ones from the genesis file
	if len(res.Validators) > 0 {
		validators, err := types.PB2TM.ValidatorUpdates(res.Validators)
		if err != nil {
			return err
		}

		a.CurState.LastValidators = types.NewValidatorSet(validators)
		a.CurState.Validators = types.NewValidatorSet(validators)
		a.CurState.NextValidators = types.NewValidatorSet(validators).CopyIncrementProposerPriority(1)
	}

	// if response specified consensus params, update the consensus params, otherwise we keep the ones from the genesis file
	if res.ConsensusParams != nil {
		a.CurState.ConsensusParams = types.UpdateConsensusParams(a.CurState.ConsensusParams, res.ConsensusParams)
		a.CurState.Version.Consensus.App = a.CurState.ConsensusParams.Version.AppVersion
	}

	// to conform with RFC-6962
	a.CurState.LastResultsHash = merkle.HashFromByteSlices(nil)

	return nil
}

func (a *AbciClient) SendEndBlock() (*abcitypes.ResponseEndBlock, error) {
	if verbose {
		a.Logger.Info("Sending EndBlock to clients")
	}
	// build the EndBlock request
	endBlockRequest := abcitypes.RequestEndBlock{
		Height: a.CurState.LastBlockHeight + 1,
	}

	// send EndBlock to all clients and collect the responses
	f := func(client AbciCounterpartyClient) (interface{}, error) {
		return client.Client.EndBlockSync(endBlockRequest)
	}
	responses, err := a.callClientsWithTimeout(f, 500*time.Millisecond)
	if err != nil {
		return nil, err
	}

	// return an error if the responses are not all equal
	for i := 1; i < len(responses); i++ {
		if !reflect.DeepEqual(responses[i], responses[0]) {
			return nil, fmt.Errorf("responses are not all equal: %v is not equal to %v", responses[i], responses[0])
		}
	}

	return responses[0].(*abcitypes.ResponseEndBlock), nil
}

func (a *AbciClient) SendCommit() (*abcitypes.ResponseCommit, error) {
	a.Logger.Info("Sending Commit to clients")
	// send Commit to all clients and collect the responses
	f := func(client AbciCounterpartyClient) (interface{}, error) {
		return client.Client.CommitSync()
	}
	responses, err := a.callClientsWithTimeout(f, 500*time.Millisecond)
	if err != nil {
		return nil, err
	}

	if a.ErrorOnUnequalResponses {
		// return an error if the responses are not all equal
		for i := 1; i < len(responses); i++ {
			if !reflect.DeepEqual(responses[i], responses[0]) {
				return nil, fmt.Errorf("responses are not all equal: %v is not equal to %v", responses[i], responses[0])
			}
		}
	}

	return responses[0].(*abcitypes.ResponseCommit), nil
}

func (a *AbciClient) SendCheckTx(tx *[]byte) (*abcitypes.ResponseCheckTx, error) {
	// build the CheckTx request
	checkTxRequest := abcitypes.RequestCheckTx{
		Tx: *tx,
	}

	// send CheckTx to all clients and collect the responses
	f := func(client AbciCounterpartyClient) (interface{}, error) {
		return client.Client.CheckTxSync(checkTxRequest)
	}
	responses, err := a.callClientsWithTimeout(f, 500*time.Millisecond)
	if err != nil {
		return nil, err
	}

	if a.ErrorOnUnequalResponses {
		// return an error if the responses are not all equal
		for i := 1; i < len(responses); i++ {
			if !reflect.DeepEqual(responses[i], responses[0]) {
				return nil, fmt.Errorf("responses are not all equal: %v is not equal to %v", responses[i], responses[0])
			}
		}
	}

	return responses[0].(*abcitypes.ResponseCheckTx), nil
}

func (a *AbciClient) SendDeliverTx(tx *[]byte) (*abcitypes.ResponseDeliverTx, error) {
	// build the DeliverTx request
	deliverTxRequest := abcitypes.RequestDeliverTx{
		Tx: *tx,
	}

	// send DeliverTx to all clients and collect the responses
	f := func(client AbciCounterpartyClient) (interface{}, error) {
		return client.Client.DeliverTxSync(deliverTxRequest)
	}
	responses, err := a.callClientsWithTimeout(f, 500*time.Millisecond)
	if err != nil {
		return nil, err
	}

	if a.ErrorOnUnequalResponses {
		// return an error if the responses are not all equal
		for i := 1; i < len(responses); i++ {
			if !reflect.DeepEqual(responses[i], responses[0]) {
				return nil, fmt.Errorf("responses are not all equal: %v is not equal to %v", responses[i], responses[0])
			}
		}
	}

	return responses[0].(*abcitypes.ResponseDeliverTx), nil
}

func (a *AbciClient) SendAbciQuery(data []byte, path string, height int64, prove bool) (*abcitypes.ResponseQuery, error) {
	// find the first connected client
	var client *AbciCounterpartyClient
	for _, c := range a.Clients {
		if c.isConnected {
			client = &c
			break
		}
	}
	if client == nil {
		return nil, fmt.Errorf("no connected clients")
	}

	// build the Query request
	request := abcitypes.RequestQuery{
		Data:   data,
		Path:   path,
		Height: height,
		Prove:  prove,
	}

	// send Query to the client and collect the response
	f := func(client AbciCounterpartyClient) (interface{}, error) {
		return client.Client.QuerySync(request)
	}
	response, err := a.callClientWithTimeout(*client, f, 500*time.Millisecond)
	if err != nil {
		return nil, err
	}

	return response.(*abcitypes.ResponseQuery), nil
}

// RunEmptyBlocks runs a specified number of empty blocks through ABCI.
func (a *AbciClient) RunEmptyBlocks(numBlocks int) error {
	for i := 0; i < numBlocks; i++ {
		_, _, _, _, _, err := a.RunBlock(nil)
		if err != nil {
			return err
		}
	}
	return nil
}

// RunBlock runs a block with a specified transaction through the ABCI application.
// It calls RunBlockWithTimeAndProposer with the current time and the LastValidators.Proposer.
func (a *AbciClient) RunBlock(tx *[]byte) (*abcitypes.ResponseBeginBlock, *abcitypes.ResponseCheckTx, *abcitypes.ResponseDeliverTx, *abcitypes.ResponseEndBlock, *abcitypes.ResponseCommit, error) {
	return a.RunBlockWithTimeAndProposer(tx, time.Now().Add(a.timeOffset), a.CurState.LastValidators.Proposer, make(map[*types.Validator]MisbehaviourType, 0))
}

// RunBlockWithEvidence runs a block with a specified transaction through the ABCI application.
// It also produces the specified evidence for the specified misbehaving validators.
func (a *AbciClient) RunBlockWithEvidence(tx *[]byte, misbehavingValidators map[*types.Validator]MisbehaviourType) (*abcitypes.ResponseBeginBlock, *abcitypes.ResponseCheckTx, *abcitypes.ResponseDeliverTx, *abcitypes.ResponseEndBlock, *abcitypes.ResponseCommit, error) {
	return a.RunBlockWithTimeAndProposer(tx, time.Now().Add(a.timeOffset), a.CurState.LastValidators.Proposer, misbehavingValidators)
}

func (a *AbciClient) ConstructDuplicateVoteEvidence(v *types.Validator) (*types.DuplicateVoteEvidence, error) {
	privVal := a.PrivValidators[v.Address.String()]
	lastBlock := a.LastBlock
	blockId, err := utils.GetBlockIdFromBlock(lastBlock)
	if err != nil {
		return nil, err
	}

	lastState, err := a.Storage.GetState(lastBlock.Height)
	if err != nil {
		return nil, err
	}

	// get the index of the validator in the last state
	index, valInLastState := lastState.Validators.GetByAddress(v.Address)

	// produce vote A.
	voteA := &cmttypes.Vote{
		ValidatorAddress: v.Address,
		ValidatorIndex:   int32(index),
		Height:           lastBlock.Height,
		Round:            1,
		Timestamp:        time.Now().Add(a.timeOffset),
		Type:             cmttypes.PrecommitType,
		BlockID:          blockId.ToProto(),
	}

	// produce vote B, which just has a different round.
	voteB := &cmttypes.Vote{
		ValidatorAddress: v.Address,
		ValidatorIndex:   int32(index),
		Height:           lastBlock.Height,
		Round:            2, // this is what differentiates the votes
		Timestamp:        time.Now().Add(a.timeOffset),
		Type:             cmttypes.PrecommitType,
		BlockID:          blockId.ToProto(),
	}

	// sign the votes
	privVal.SignVote(a.CurState.ChainID, voteA)
	privVal.SignVote(a.CurState.ChainID, voteB)

	// votes need to pass validation rules
	convertedVoteA, err := types.VoteFromProto(voteA)
	err = convertedVoteA.ValidateBasic()
	if err != nil {
		a.Logger.Error("Error validating vote A", "error", err)
		return nil, err
	}

	convertedVoteB, err := types.VoteFromProto(voteB)
	err = convertedVoteB.ValidateBasic()
	if err != nil {
		a.Logger.Error("Error validating vote B", "error", err)
		return nil, err
	}

	// build the actual evidence
	evidence := types.DuplicateVoteEvidence{
		VoteA: convertedVoteA,
		VoteB: convertedVoteB,

		TotalVotingPower: lastState.Validators.TotalVotingPower(),
		ValidatorPower:   valInLastState.VotingPower,
		Timestamp:        lastBlock.Time,
	}
	return &evidence, nil
}

func (a *AbciClient) ConstructLightClientAttackEvidence(
	v *types.Validator,
	misbehaviourType MisbehaviourType,
) (*types.LightClientAttackEvidence, error) {
	lastBlock := a.LastBlock

	lastState, err := a.Storage.GetState(lastBlock.Height)
	if err != nil {
		return nil, err
	}

	// deepcopy the last block so we can modify it
	cp, err := deepcopy.Anything(lastBlock)
	if err != nil {
		return nil, err
	}

	// force the type conversion into a block
	var conflictingBlock *types.Block
	conflictingBlock = cp.(*types.Block)

	switch misbehaviourType {
	case Lunatic:
		// modify the app hash to be invalid
		conflictingBlock.AppHash = []byte("some other app hash")
	case Amnesia:
		// TODO not sure how to handle this yet, just leave the block intact for now
	case Equivocation:
		// get another valid block by making it have a different time
		conflictingBlock.Time = conflictingBlock.Time.Add(1 * time.Second)
	default:
		return nil, fmt.Errorf("unknown misbehaviour type %v for light client misbehaviour", misbehaviourType)
	}

	// make the conflicting block into a light block
	signedHeader := types.SignedHeader{
		Header: &conflictingBlock.Header,
		Commit: a.LastCommit,
	}

	conflictingLightBlock := types.LightBlock{
		SignedHeader: &signedHeader,
		ValidatorSet: a.CurState.Validators,
	}

	return &types.LightClientAttackEvidence{
		TotalVotingPower:    lastState.Validators.TotalVotingPower(),
		Timestamp:           lastBlock.Time,
		ByzantineValidators: []*types.Validator{v},
		CommonHeight:        lastBlock.Height - 1,
		ConflictingBlock:    &conflictingLightBlock,
	}, nil
}

// RunBlock runs a block with a specified transaction through the ABCI application.
// It calls BeginBlock, DeliverTx, EndBlock, Commit and then
// updates the state.
// RunBlock is safe for use by multiple goroutines simultaneously.
func (a *AbciClient) RunBlockWithTimeAndProposer(
	tx *[]byte,
	blockTime time.Time,
	proposer *types.Validator,
	misbehavingValidators map[*types.Validator]MisbehaviourType,
) (*abcitypes.ResponseBeginBlock, *abcitypes.ResponseCheckTx, *abcitypes.ResponseDeliverTx, *abcitypes.ResponseEndBlock, *abcitypes.ResponseCommit, error) {
	// lock mutex to avoid running two blocks at the same time
	a.Logger.Debug("Locking mutex")
	blockMutex.Lock()

	defer blockMutex.Unlock()
	defer a.Logger.Debug("Unlocking mutex")

	a.Logger.Info("Running block")
	if verbose {
		a.Logger.Info("State at start of block", "state", a.CurState)
	}

	newHeight := a.CurState.LastBlockHeight + 1

	txs := make([]types.Tx, 0)
	if tx != nil {
		txs = append(txs, *tx)
	}

	var resCheckTx *abcitypes.ResponseCheckTx
	var err error
	if tx != nil {
		resCheckTx, err = a.SendCheckTx(tx)
		if err != nil {
			return nil, nil, nil, nil, nil, err
		}
	}

	// TODO: handle special case where proposer is nil
	var proposerAddress types.Address
	if proposer != nil {
		proposerAddress = proposer.Address
	}

	evidences := make([]types.Evidence, 0)
	for v, misbehaviourType := range misbehavingValidators {
		// match the misbehaviour type to call the correct function
		var evidence types.Evidence
		var err error
		if misbehaviourType == DuplicateVote {
			// create double-sign evidence
			evidence, err = a.ConstructDuplicateVoteEvidence(v)
		} else {
			// create light client attack evidence
			evidence, err = a.ConstructLightClientAttackEvidence(v, misbehaviourType)
		}

		if err != nil {
			return nil, nil, nil, nil, nil, err
		}

		evidences = append(evidences, evidence)
	}

	block, _ := a.CurState.MakeBlock(a.CurState.LastBlockHeight+1, txs, a.LastCommit, evidences, proposerAddress)
	// override the block time, since we do not actually get votes from peers to median the time out of
	block.Time = blockTime
	blockId, err := utils.GetBlockIdFromBlock(block)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}

	commitSigs := []types.CommitSig{}

	for index, val := range a.CurState.Validators.Validators {
		privVal := a.PrivValidators[val.Address.String()]

		shouldSign, err := a.GetSigningStatus(val.Address.String())
		if err != nil {
			return nil, nil, nil, nil, nil, err
		}

		if shouldSign {
			// create and sign a precommit
			vote := &cmttypes.Vote{
				ValidatorAddress: val.Address,
				ValidatorIndex:   int32(index),
				Height:           block.Height,
				Round:            1,
				Timestamp:        time.Now().Add(a.timeOffset),
				Type:             cmttypes.PrecommitType,
				BlockID:          blockId.ToProto(),
			}

			err = privVal.SignVote(a.CurState.ChainID, vote)
			if err != nil {
				return nil, nil, nil, nil, nil, err
			}

			convertedVote, err := types.VoteFromProto(vote)
			if err != nil {
				return nil, nil, nil, nil, nil, err
			}

			commitSig := convertedVote.CommitSig()

			commitSigs = append(commitSigs, commitSig)
		} else {
			commitSigs = append(commitSigs, types.NewCommitSigAbsent())
		}
	}

	a.LastCommit = types.NewCommit(
		block.Height,
		1,
		*blockId,
		commitSigs,
	)

	// sanity check that the commit is signed correctly
	err = a.CurState.Validators.VerifyCommitLightTrusting(a.CurState.ChainID, a.LastCommit, cmtmath.Fraction{Numerator: 1, Denominator: 3})
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}

	// sanity check that the commit makes a proper light block
	signedHeader := types.SignedHeader{
		Header: &block.Header,
		Commit: a.LastCommit,
	}

	lightBlock := types.LightBlock{
		SignedHeader: &signedHeader,
		ValidatorSet: a.CurState.Validators,
	}

	err = lightBlock.ValidateBasic(a.CurState.ChainID)
	if err != nil {
		a.Logger.Error("Light block validation failed", "err", err)
		return nil, nil, nil, nil, nil, err
	}

	resBeginBlock, err := a.SendBeginBlock(block)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}

	var resDeliverTx *abcitypes.ResponseDeliverTx
	if tx != nil {
		resDeliverTx, err = a.SendDeliverTx(tx)
		if err != nil {
			return nil, nil, nil, nil, nil, err
		}
	} else {
		resDeliverTx = nil
	}

	resEndBlock, err := a.SendEndBlock()
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}

	deliverTxResponses := []*abcitypes.ResponseDeliverTx{}
	if tx != nil {
		deliverTxResponses = append(deliverTxResponses, resDeliverTx)
	}

	// lock the state update mutex while the stores are updated to avoid
	// inconsistencies between stores
	a.Storage.LockBeforeStateUpdate()
	a.LastBlock = block

	// copy state so that the historical state is not mutated
	state := a.CurState.Copy()

	// build components of the state update, then call the update function
	abciResponses := cmtstate.ABCIResponses{
		DeliverTxs: deliverTxResponses,
		EndBlock:   resEndBlock,
		BeginBlock: resBeginBlock,
	}

	// insert entries into the storage
	err = a.Storage.UpdateStores(newHeight, block, a.LastCommit, &state, &abciResponses)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}

	// updates state as a side effect. returns an error if the state update fails
	err = a.UpdateStateFromBlock(blockId, block, abciResponses)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	// unlock the state mutex, since we are done updating state
	a.Storage.UnlockAfterStateUpdate()

	resCommit, err := a.SendCommit()
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	a.CurState.AppHash = resCommit.Data

	return resBeginBlock, resCheckTx, resDeliverTx, resEndBlock, resCommit, nil
}

// UpdateStateFromBlock updates the AbciClients state
// after running a block. It updates the
// last block height, last block ID, last
// block results hash, validators, consensus
// params, and app hash.
func (a *AbciClient) UpdateStateFromBlock(
	blockId *types.BlockID,
	block *types.Block,
	abciResponses cmtstate.ABCIResponses,
) error {
	// build components of the state update, then call the update function
	abciValidatorUpdates := abciResponses.EndBlock.ValidatorUpdates
	err := validateValidatorUpdates(abciValidatorUpdates, a.CurState.ConsensusParams.Validator)
	if err != nil {
		return fmt.Errorf("error in validator updates: %v", err)
	}

	validatorUpdates, err := types.PB2TM.ValidatorUpdates(abciValidatorUpdates)
	if err != nil {
		return fmt.Errorf("error converting validator updates: %v", err)
	}

	newState, err := UpdateState(
		a.CurState,
		blockId,
		&block.Header,
		&abciResponses,
		validatorUpdates,
	)
	if err != nil {
		return fmt.Errorf("error updating state: %v", err)
	}

	a.CurState = newState

	// Events are fired after everything else.
	// NOTE: if we crash between Commit and Save, events wont be fired during replay
	fireEvents(a.Logger, &a.EventBus, block, &abciResponses, validatorUpdates)
	return nil
}

// adapted from https://github.com/cometbft/cometbft/blob/9267594e0a17c01cc4a97b399ada5eaa8a734db5/state/execution.go#L478
// updateState returns a new State updated according to the header and responses.
func UpdateState(
	curState state.State,
	blockId *types.BlockID,
	blockHeader *types.Header,
	abciResponses *cmtstate.ABCIResponses,
	validatorUpdates []*types.Validator,
) (state.State, error) {
	// Copy the valset so we can apply changes from EndBlock
	// and update s.LastValidators and s.Validators.
	nValSet := curState.NextValidators.Copy()

	// Update the validator set with the latest abciResponses.
	lastHeightValsChanged := curState.LastHeightValidatorsChanged
	if len(validatorUpdates) > 0 {
		err := nValSet.UpdateWithChangeSet(validatorUpdates)
		if err != nil {
			return curState, fmt.Errorf("error changing validator set: %v", err)
		}
		// Change results from this height but only applies to the next next height.
		lastHeightValsChanged = blockHeader.Height + 1 + 1
	}

	// Update validator proposer priority and set state variables.
	nValSet.IncrementProposerPriority(1)

	// Update the params with the latest abciResponses.
	nextParams := curState.ConsensusParams
	lastHeightParamsChanged := curState.LastHeightConsensusParamsChanged
	if abciResponses.EndBlock.ConsensusParamUpdates != nil {
		// NOTE: must not mutate s.ConsensusParams
		nextParams := types.UpdateConsensusParams(curState.ConsensusParams, abciResponses.EndBlock.ConsensusParamUpdates)
		err := types.ValidateConsensusParams(nextParams)
		if err != nil {
			return curState, fmt.Errorf("error updating consensus params: %v", err)
		}

		curState.Version.Consensus.App = nextParams.Version.AppVersion

		// Change results from this height but only applies to the next height.
		lastHeightParamsChanged = blockHeader.Height + 1
	}

	nextVersion := curState.Version

	return state.State{
		Version:                          nextVersion,
		ChainID:                          curState.ChainID,
		InitialHeight:                    curState.InitialHeight,
		LastBlockHeight:                  blockHeader.Height,
		LastBlockID:                      *blockId,
		LastBlockTime:                    blockHeader.Time,
		NextValidators:                   nValSet,
		Validators:                       curState.NextValidators.Copy(),
		LastValidators:                   curState.Validators.Copy(),
		LastHeightValidatorsChanged:      lastHeightValsChanged,
		ConsensusParams:                  nextParams,
		LastHeightConsensusParamsChanged: lastHeightParamsChanged,
		LastResultsHash:                  state.ABCIResponsesResultsHash(abciResponses),
		// app hash will be populated after commit
		AppHash: nil,
	}, nil
}

// adapted from https://github.com/cometbft/cometbft/blob/9267594e0a17c01cc4a97b399ada5eaa8a734db5/state/execution.go#L452
func validateValidatorUpdates(
	abciUpdates []abcitypes.ValidatorUpdate,
	params cmttypes.ValidatorParams,
) error {
	for _, valUpdate := range abciUpdates {
		if valUpdate.GetPower() < 0 {
			return fmt.Errorf("voting power can't be negative %v", valUpdate)
		} else if valUpdate.GetPower() == 0 {
			// continue, since this is deleting the validator, and thus there is no
			// pubkey to check
			continue
		}

		// Check if validator's pubkey matches an ABCI type in the consensus params
		pk, err := cryptoenc.PubKeyFromProto(valUpdate.PubKey)
		if err != nil {
			return err
		}

		if !types.IsValidPubkeyType(params, pk.Type()) {
			return fmt.Errorf("validator %v is using pubkey %s, which is unsupported for consensus",
				valUpdate, pk.Type())
		}
	}
	return nil
}

func fireEvents(
	logger cometlog.Logger,
	eventBus types.BlockEventPublisher,
	block *types.Block,
	abciResponses *cmtstate.ABCIResponses,
	validatorUpdates []*types.Validator,
) {
	if err := eventBus.PublishEventNewBlock(types.EventDataNewBlock{
		Block:            block,
		ResultBeginBlock: *abciResponses.BeginBlock,
		ResultEndBlock:   *abciResponses.EndBlock,
	}); err != nil {
		logger.Error("failed publishing new block", "err", err)
	}

	eventDataNewBlockHeader := types.EventDataNewBlockHeader{
		Header:           block.Header,
		NumTxs:           int64(len(block.Txs)),
		ResultBeginBlock: *abciResponses.BeginBlock,
		ResultEndBlock:   *abciResponses.EndBlock,
	}

	if err := eventBus.PublishEventNewBlockHeader(eventDataNewBlockHeader); err != nil {
		logger.Error("failed publishing new block header", "err", err)
	}

	if len(block.Evidence.Evidence) != 0 {
		for _, ev := range block.Evidence.Evidence {
			if err := eventBus.PublishEventNewEvidence(types.EventDataNewEvidence{
				Evidence: ev,
				Height:   block.Height,
			}); err != nil {
				logger.Error("failed publishing new evidence", "err", err)
			}
		}
	}

	for i, tx := range block.Data.Txs {
		if err := eventBus.PublishEventTx(types.EventDataTx{TxResult: abcitypes.TxResult{
			Height: block.Height,
			Index:  uint32(i),
			Tx:     tx,
			Result: *(abciResponses.DeliverTxs[i]),
		}}); err != nil {
			logger.Error("failed publishing event TX", "err", err)
		}
	}

	if len(validatorUpdates) > 0 {
		if err := eventBus.PublishEventValidatorSetUpdates(
			types.EventDataValidatorSetUpdates{ValidatorUpdates: validatorUpdates}); err != nil {
			logger.Error("failed publishing event", "err", err)
		}
	}
}
