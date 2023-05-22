package main

import (
	"os"
	"strings"
	"time"

	comet_abciclient "github.com/cometbft/cometbft/abci/client"
	cometlog "github.com/cometbft/cometbft/libs/log"
	"github.com/cometbft/cometbft/privval"
	"github.com/cometbft/cometbft/state"
	"github.com/cometbft/cometbft/types"
	"github.com/p-offtermatt/CometMock/cometmock/abci_client"
	"github.com/p-offtermatt/CometMock/cometmock/rpc_server"
	"github.com/p-offtermatt/CometMock/cometmock/storage"
)

func CreateAndStartEventBus(logger cometlog.Logger) (*types.EventBus, error) {
	eventBus := types.NewEventBus()
	eventBus.SetLogger(logger.With("module", "events"))
	if err := eventBus.Start(); err != nil {
		return nil, err
	}
	return eventBus, nil
}

// GetMockPVsFromNodeHomes returns a list of MockPVs, created with the priv_validator_key's from the specified node homes
// We use MockPV because they do not do sanity checks that would e.g. prevent double signing
func GetMockPVsFromNodeHomes(nodeHomes []string) []types.PrivValidator {
	mockPVs := make([]types.PrivValidator, 0)

	for _, nodeHome := range nodeHomes {
		privValidatorKeyFile := nodeHome + "/config/priv_validator_key.json"
		privValidatorStateFile := nodeHome + "/data/priv_validator_state.json"
		validator := privval.LoadFilePV(privValidatorKeyFile, privValidatorStateFile)

		mockPV := types.NewMockPVWithParams(validator.Key.PrivKey, false, false)
		mockPVs = append(mockPVs, mockPV)
	}

	return mockPVs
}

func main() {
	logger := cometlog.NewTMLogger(cometlog.NewSyncWriter(os.Stdout))

	if len(os.Args) != 4 {
		logger.Error("Usage: <app-addresses> <genesis-file> <cometmock-listen-address>")
	}

	args := os.Args[1:]

	appAddresses := strings.Split(args[0], ",")
	genesisFile := args[1]
	cometMockListenAddress := args[2]
	nodeHomesString := args[3]

	// read node homes from args
	nodeHomes := strings.Split(nodeHomesString, ",")

	// get priv validators from node Homes
	privVals := GetMockPVsFromNodeHomes(nodeHomes)

	genesisDoc, err := state.MakeGenesisDocFromFile(genesisFile)
	if err != nil {
		logger.Error(err.Error())
	}

	curState, err := state.MakeGenesisState(genesisDoc)
	if err != nil {
		logger.Error(err.Error())
	}

	clients := []comet_abciclient.Client{}

	for _, appAddress := range appAddresses {
		logger.Info("Connecting to client at %v", appAddress)
		client := comet_abciclient.NewGRPCClient(appAddress, true)
		client.SetLogger(logger)
		client.Start()
		clients = append(clients, client)
	}

	eventBus, err := CreateAndStartEventBus(logger)
	if err != nil {
		logger.Error(err.Error())
		panic(err)
	}

	abci_client.GlobalClient = &abci_client.AbciClient{
		Clients:                 clients,
		Logger:                  logger,
		CurState:                curState,
		ErrorOnUnequalResponses: true,
		EventBus:                *eventBus,
		LastCommit:              &types.Commit{},
		Storage:                 &storage.MapStorage{},
		PrivValidators:          privVals,
	}

	// initialize chain
	err = abci_client.GlobalClient.SendInitChain(curState, genesisDoc)
	if err != nil {
		logger.Error(err.Error())
		panic(err)
	}

	// run an empty block
	abci_client.GlobalClient.RunBlock(nil, time.Now(), abci_client.GlobalClient.CurState.LastValidators.Proposer)

	go rpc_server.StartRPCServerWithDefaultConfig(cometMockListenAddress, logger)

	// produce a block every second
	for {
		abci_client.GlobalClient.RunBlock(nil, time.Now(), abci_client.GlobalClient.CurState.LastValidators.Proposer)
		time.Sleep(1 * time.Second)
	}
}
