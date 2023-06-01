package abci_client

import (
	"fmt"
	"reflect"
	"sync"
	"time"

	abciclient "github.com/cometbft/cometbft/abci/client"
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
	"github.com/p-offtermatt/CometMock/cometmock/storage"
	"github.com/p-offtermatt/CometMock/cometmock/utils"
)

var GlobalClient *AbciClient

// store a mutex that allows only running one block at a time
var blockMutex = sync.Mutex{}

var verbose = false

// AbciClient facilitates calls to the ABCI interface of multiple nodes.
// It also tracks the current state and a common logger.
type AbciClient struct {
	Clients        []abciclient.Client
	Logger         cometlog.Logger
	CurState       state.State
	EventBus       types.EventBus
	LastBlock      *types.Block
	LastCommit     *types.Commit
	Storage        storage.Storage
	PrivValidators map[string]types.PrivValidator
	IndexerService *txindex.IndexerService
	TxIndex        *indexerkv.TxIndex
	BlockIndex     *blockindexkv.BlockerIndexer

	// if this is true, then an error will be returned if the responses from the clients are not all equal.
	// can be used to check for nondeterminism in apps, but also slows down execution a bit,
	// though performance difference was not measured.
	ErrorOnUnequalResponses bool
}

func (a *AbciClient) SendBeginBlock(block *types.Block) (*abcitypes.ResponseBeginBlock, error) {
	if verbose {
		a.Logger.Info("Sending BeginBlock to clients")
	}
	// build the BeginBlock request
	beginBlockRequest := CreateBeginBlockRequest(&block.Header, block.LastCommit)

	// send BeginBlock to all clients and collect the responses
	responses := []*abcitypes.ResponseBeginBlock{}
	for _, client := range a.Clients {
		response, err := client.BeginBlockSync(*beginBlockRequest)
		if err != nil {
			return nil, err
		}
		responses = append(responses, response)
	}

	if a.ErrorOnUnequalResponses {
		// return an error if the responses are not all equal
		for i := 1; i < len(responses); i++ {
			if !reflect.DeepEqual(responses[i], responses[0]) {
				return nil, fmt.Errorf("responses are not all equal: %v is not equal to %v", responses[i], responses[0])
			}
		}
	}

	return responses[0], nil
}

func CreateBeginBlockRequest(header *types.Header, lastCommit *types.Commit) *abcitypes.RequestBeginBlock {
	return &abcitypes.RequestBeginBlock{
		// TODO: fill in Votes
		LastCommitInfo: abcitypes.LastCommitInfo{Round: lastCommit.Round, Votes: []abcitypes.VoteInfo{}},
		Header:         *header.ToProto(),
	}
}

func (a *AbciClient) SendInitChain(genesisState state.State, genesisDoc *types.GenesisDoc) error {
	if verbose {
		a.Logger.Info("Sending InitChain to clients")
	}
	// build the InitChain request
	initChainRequest := CreateInitChainRequest(genesisState, genesisDoc)

	// send InitChain to all clients and collect the responses
	responses := []*abcitypes.ResponseInitChain{}
	for _, client := range a.Clients {
		response, err := client.InitChainSync(*initChainRequest)
		if err != nil {
			return err
		}
		responses = append(responses, response)
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
	err := a.UpdateStateFromInit(responses[0])
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
	responses := []*abcitypes.ResponseEndBlock{}
	for _, client := range a.Clients {
		response, err := client.EndBlockSync(endBlockRequest)
		if err != nil {
			return nil, err
		}
		responses = append(responses, response)
	}

	// return an error if the responses are not all equal
	for i := 1; i < len(responses); i++ {
		if !reflect.DeepEqual(responses[i], responses[0]) {
			return nil, fmt.Errorf("responses are not all equal: %v is not equal to %v", responses[i], responses[0])
		}
	}

	return responses[0], nil
}

func (a *AbciClient) SendCommit() (*abcitypes.ResponseCommit, error) {
	a.Logger.Info("Sending Commit to clients")
	// send Commit to all clients and collect the responses
	responses := []*abcitypes.ResponseCommit{}
	for _, client := range a.Clients {
		response, err := client.CommitSync()
		if err != nil {
			return nil, err
		}
		responses = append(responses, response)
	}

	if a.ErrorOnUnequalResponses {
		// return an error if the responses are not all equal
		for i := 1; i < len(responses); i++ {
			if !reflect.DeepEqual(responses[i], responses[0]) {
				return nil, fmt.Errorf("responses are not all equal: %v is not equal to %v", responses[i], responses[0])
			}
		}
	}

	return responses[0], nil
}

func (a *AbciClient) SendCheckTx(tx *[]byte) (*abcitypes.ResponseCheckTx, error) {
	// build the CheckTx request
	checkTxRequest := abcitypes.RequestCheckTx{
		Tx: *tx,
	}

	// send CheckTx to all clients and collect the responses
	responses := []*abcitypes.ResponseCheckTx{}
	for _, client := range a.Clients {
		response, err := client.CheckTxSync(checkTxRequest)
		if err != nil {
			return nil, err
		}
		responses = append(responses, response)
	}

	if a.ErrorOnUnequalResponses {
		// return an error if the responses are not all equal
		for i := 1; i < len(responses); i++ {
			if !reflect.DeepEqual(responses[i], responses[0]) {
				return nil, fmt.Errorf("responses are not all equal: %v is not equal to %v", responses[i], responses[0])
			}
		}
	}

	return responses[0], nil
}

func (a *AbciClient) SendDeliverTx(tx *[]byte) (*abcitypes.ResponseDeliverTx, error) {
	// build the DeliverTx request
	deliverTxRequest := abcitypes.RequestDeliverTx{
		Tx: *tx,
	}

	// send DeliverTx to all clients and collect the responses
	responses := []*abcitypes.ResponseDeliverTx{}
	for _, client := range a.Clients {
		response, err := client.DeliverTxSync(deliverTxRequest)
		if err != nil {
			return nil, err
		}
		responses = append(responses, response)
	}

	if a.ErrorOnUnequalResponses {
		// return an error if the responses are not all equal
		for i := 1; i < len(responses); i++ {
			if !reflect.DeepEqual(responses[i], responses[0]) {
				return nil, fmt.Errorf("responses are not all equal: %v is not equal to %v", responses[i], responses[0])
			}
		}
	}

	return responses[0], nil
}

func (a *AbciClient) SendAbciQuery(data []byte, path string, height int64, prove bool) (*abcitypes.ResponseQuery, error) {
	client := a.Clients[0]
	request := abcitypes.RequestQuery{
		Data:   data,
		Path:   path,
		Height: height,
		Prove:  prove,
	}
	return client.QuerySync(request)
}

// RunBlock runs a block with a specified transaction through the ABCI application.
// It calls BeginBlock, DeliverTx, EndBlock, Commit and then
// updates the state.
// RunBlock is safe for use by multiple goroutines simultaneously.
func (a *AbciClient) RunBlock(tx *[]byte, blockTime time.Time, proposer *types.Validator) (*abcitypes.ResponseBeginBlock, *abcitypes.ResponseCheckTx, *abcitypes.ResponseDeliverTx, *abcitypes.ResponseEndBlock, *abcitypes.ResponseCommit, error) {
	// lock mutex to avoid running two blocks at the same time
	blockMutex.Lock()

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

	block, _ := a.CurState.MakeBlock(a.CurState.LastBlockHeight+1, txs, a.LastCommit, []types.Evidence{}, proposerAddress)
	// override the block time, since we do not actually get votes from peers to median the time out of
	block.Time = blockTime
	blockId, err := utils.GetBlockIdFromBlock(block)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}

	a.LastBlock = block

	commitSigs := []types.CommitSig{}

	for index, val := range a.CurState.Validators.Validators {
		privVal := a.PrivValidators[val.Address.String()]

		// create and sign a precommit
		vote := &cmttypes.Vote{
			ValidatorAddress: val.Address,
			ValidatorIndex:   int32(index),
			Height:           block.Height,
			Round:            1,
			Timestamp:        time.Now(),
			Type:             cmttypes.PrecommitType,
			BlockID:          blockId.ToProto(),
		}

		privVal.SignVote(a.CurState.ChainID, vote)

		convertedVote, err := types.VoteFromProto(vote)
		if err != nil {
			return nil, nil, nil, nil, nil, err
		}

		commitSig := convertedVote.CommitSig()

		commitSigs = append(commitSigs, commitSig)
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

	// insert entries into the storage
	err = a.Storage.InsertBlock(newHeight, block)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}

	err = a.Storage.InsertCommit(newHeight, a.LastCommit)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}

	// copy state so that the historical state is not mutated
	state := a.CurState.Copy()

	err = a.Storage.InsertState(newHeight, &state)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}

	// build components of the state update, then call the update function
	abciResponses := cmtstate.ABCIResponses{
		DeliverTxs: deliverTxResponses,
		EndBlock:   resEndBlock,
		BeginBlock: resBeginBlock,
	}

	err = a.Storage.InsertResponses(newHeight, &abciResponses)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}

	// updates state as a side effect. returns an error if the state update fails
	err = a.UpdateStateFromBlock(blockId, block, abciResponses)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}

	resCommit, err := a.SendCommit()
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	a.CurState.AppHash = resCommit.Data

	// unlock mutex
	blockMutex.Unlock()

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
		nextParams = types.UpdateConsensusParams(curState.ConsensusParams, abciResponses.EndBlock.ConsensusParamUpdates)
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
