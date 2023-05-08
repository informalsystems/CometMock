package abci_client

import (
	"fmt"
	"time"

	abciclient "github.com/cometbft/cometbft/abci/client"
	abcitypes "github.com/cometbft/cometbft/abci/types"
	cryptoenc "github.com/cometbft/cometbft/crypto/encoding"
	"github.com/cometbft/cometbft/crypto/merkle"
	"github.com/cometbft/cometbft/libs/bytes"
	cometlog "github.com/cometbft/cometbft/libs/log"
	cmtstate "github.com/cometbft/cometbft/proto/tendermint/state"
	ttypes "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/cometbft/cometbft/state"
	"github.com/cometbft/cometbft/types"
)

var GlobalClient *AbciClient

// AbciClient facilitates calls to the ABCI interface of multiple nodes.
// It also tracks the current state and a common logger.
type AbciClient struct {
	Clients  []abciclient.Client
	Logger   cometlog.Logger
	CurState state.State
}

func (a *AbciClient) SendBeginBlock() (*abcitypes.ResponseBeginBlock, error) {
	a.Logger.Info("Sending BeginBlock to clients")
	// build the BeginBlock request
	beginBlockRequest := CreateBeginBlockRequest(a.CurState, time.Now(), *a.CurState.LastValidators.Proposer)

	// send BeginBlock to all clients and collect the responses
	responses := []*abcitypes.ResponseBeginBlock{}
	for _, client := range a.Clients {
		response, err := client.BeginBlockSync(*beginBlockRequest)
		if err != nil {
			return nil, err
		}
		responses = append(responses, response)
	}

	// return an error if the responses are not all equal
	for i := 1; i < len(responses); i++ {
		if responses[i] != responses[0] {
			return nil, fmt.Errorf("responses are not all equal: %v is not equal to %v", responses[i], responses[0])
		}
	}

	return responses[0], nil
}

func CreateBeginBlockRequest(curState state.State, curTime time.Time, proposer types.Validator) *abcitypes.RequestBeginBlock {
	return &abcitypes.RequestBeginBlock{
		LastCommitInfo: abcitypes.CommitInfo{
			Round: 0,
			Votes: []abcitypes.VoteInfo{},
		},
		Header: ttypes.Header{
			ChainID:         curState.ChainID,
			Version:         curState.Version.Consensus,
			Height:          curState.LastBlockHeight + 1,
			Time:            curTime,
			LastBlockId:     curState.LastBlockID.ToProto(),
			LastCommitHash:  curState.LastResultsHash,
			ProposerAddress: proposer.Address,
		},
	}
}

func (a *AbciClient) SendInitChain(genesisState state.State, genesisDoc *types.GenesisDoc) error {
	a.Logger.Info("Sending InitChain to clients")
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

	// return an error if the responses are not all equal
	for i := 1; i < len(responses); i++ {
		if responses[i] != responses[0] {
			return fmt.Errorf("responses are not all equal: %v is not equal to %v", responses[i], responses[0])
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
	consensusParams := genesisState.ConsensusParams.ToProto()

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
		ConsensusParams: &consensusParams,
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
		a.CurState.ConsensusParams = a.CurState.ConsensusParams.Update(res.ConsensusParams)
		a.CurState.Version.Consensus.App = a.CurState.ConsensusParams.Version.App
	}

	// to conform with RFC-6962
	a.CurState.LastResultsHash = merkle.HashFromByteSlices(nil)

	return nil
}

func (a *AbciClient) SendEndBlock() (*abcitypes.ResponseEndBlock, error) {
	a.Logger.Info("Sending EndBlock to clients")
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
		if responses[i] != responses[0] {
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

	// return an error if the responses are not all equal
	for i := 1; i < len(responses); i++ {
		if responses[i] != responses[0] {
			return nil, fmt.Errorf("responses are not all equal: %v is not equal to %v", responses[i], responses[0])
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

	// return an error if the responses are not all equal
	for i := 1; i < len(responses); i++ {
		if responses[i] != responses[0] {
			return nil, fmt.Errorf("responses are not all equal: %v is not equal to %v", responses[i], responses[0])
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
func (a *AbciClient) RunBlock(tx *[]byte) (*abcitypes.ResponseBeginBlock, *abcitypes.ResponseDeliverTx, *abcitypes.ResponseEndBlock, *abcitypes.ResponseCommit, error) {
	a.Logger.Info("Running block")

	resBeginBlock, err := a.SendBeginBlock()
	if err != nil {
		return nil, nil, nil, nil, err
	}

	resDeliverTx, err := a.SendDeliverTx(tx)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	resEndBlock, err := a.SendEndBlock()
	if err != nil {
		return nil, nil, nil, nil, err
	}

	resCommit, err := a.SendCommit()
	if err != nil {
		return nil, nil, nil, nil, err
	}

	// updates state as a side effect. returns an error if the state update fails
	err = a.UpdateStateFromBlock(resBeginBlock, resEndBlock, resCommit)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	return resBeginBlock, resDeliverTx, resEndBlock, resCommit, nil
}

// UpdateStateFromBlock updates the AbciClients state
// after running a block. It updates the
// last block height, last block ID, last
// block results hash, validators, consensus
// params, and app hash.
func (a *AbciClient) UpdateStateFromBlock(
	beginBlockResponse *abcitypes.ResponseBeginBlock,
	endBlockResponse *abcitypes.ResponseEndBlock,
	commitResponse *abcitypes.ResponseCommit,
) error {
	// build components of the state update, then call the update function

	// TODO: if necessary, construct actual block id
	blockID := types.BlockID{}

	// TODO: if necessary, refine header to be more true to CometBFT behaviour
	header := types.Header{
		Version:            a.CurState.Version.Consensus,
		ChainID:            a.CurState.ChainID,
		Height:             a.CurState.LastBlockHeight + 1,
		Time:               time.Now(),
		LastBlockID:        a.CurState.LastBlockID,
		LastCommitHash:     a.CurState.LastResultsHash,
		DataHash:           bytes.HexBytes{},
		ValidatorsHash:     a.CurState.Validators.Hash(),
		NextValidatorsHash: a.CurState.NextValidators.Hash(),
		ConsensusHash:      a.CurState.ConsensusParams.Hash(),
		AppHash:            a.CurState.AppHash,
		LastResultsHash:    commitResponse.Data,
		EvidenceHash:       bytes.HexBytes{},
		ProposerAddress:    a.CurState.Validators.Proposer.Address,
	}

	abciResponses := cmtstate.ABCIResponses{
		DeliverTxs: []*abcitypes.ResponseDeliverTx{},
		EndBlock:   endBlockResponse,
		BeginBlock: beginBlockResponse,
	}

	abciValidatorUpdates := endBlockResponse.ValidatorUpdates
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
		blockID,
		&header,
		&abciResponses,
		validatorUpdates,
	)
	if err != nil {
		return fmt.Errorf("error updating state: %v", err)
	}

	a.CurState = newState
	return nil
}

// adapted from https://github.com/cometbft/cometbft/blob/9267594e0a17c01cc4a97b399ada5eaa8a734db5/state/execution.go#L478
// updateState returns a new State updated according to the header and responses.
func UpdateState(
	curState state.State,
	blockID types.BlockID,
	header *types.Header,
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
		lastHeightValsChanged = header.Height + 1 + 1
	}

	// Update validator proposer priority and set state variables.
	nValSet.IncrementProposerPriority(1)

	// Update the params with the latest abciResponses.
	nextParams := curState.ConsensusParams
	lastHeightParamsChanged := curState.LastHeightConsensusParamsChanged
	if abciResponses.EndBlock.ConsensusParamUpdates != nil {
		// NOTE: must not mutate s.ConsensusParams
		nextParams = curState.ConsensusParams.Update(abciResponses.EndBlock.ConsensusParamUpdates)
		err := nextParams.ValidateBasic()
		if err != nil {
			return curState, fmt.Errorf("error updating consensus params: %v", err)
		}

		curState.Version.Consensus.App = nextParams.Version.App

		// Change results from this height but only applies to the next height.
		lastHeightParamsChanged = header.Height + 1
	}

	nextVersion := curState.Version

	// NOTE: the AppHash has not been populated.
	// It will be filled on state.Save.
	return state.State{
		Version:                          nextVersion,
		ChainID:                          curState.ChainID,
		InitialHeight:                    curState.InitialHeight,
		LastBlockHeight:                  header.Height,
		LastBlockID:                      blockID,
		LastBlockTime:                    header.Time,
		NextValidators:                   nValSet,
		Validators:                       curState.NextValidators.Copy(),
		LastValidators:                   curState.Validators.Copy(),
		LastHeightValidatorsChanged:      lastHeightValsChanged,
		ConsensusParams:                  nextParams,
		LastHeightConsensusParamsChanged: lastHeightParamsChanged,
		LastResultsHash:                  state.ABCIResponsesResultsHash(abciResponses),
		AppHash:                          nil,
	}, nil
}

// adapted from https://github.com/cometbft/cometbft/blob/9267594e0a17c01cc4a97b399ada5eaa8a734db5/state/execution.go#L452
func validateValidatorUpdates(
	abciUpdates []abcitypes.ValidatorUpdate,
	params types.ValidatorParams,
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
