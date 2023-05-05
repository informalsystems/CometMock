package abci_client

import (
	"fmt"
	"time"

	abciclient "github.com/cometbft/cometbft/abci/client"
	abcitypes "github.com/cometbft/cometbft/abci/types"
	"github.com/cometbft/cometbft/crypto/merkle"
	cometlog "github.com/cometbft/cometbft/libs/log"
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

func (a *AbciClient) SendDeliverTx(tx []byte) (*abcitypes.ResponseDeliverTx, error) {
	// build the DeliverTx request
	deliverTxRequest := abcitypes.RequestDeliverTx{
		Tx: tx,
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

// RunBlock runs a block with specified transactions through the ABCI application.
// It calls BeginBlock, DeliverTx, EndBlock, Commit and then
// updates the state.
func (a *AbciClient) RunBlock(txs *[]byte)
