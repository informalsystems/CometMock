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
	"github.com/informalsystems/CometMock/cometmock/abci_client"
	"github.com/informalsystems/CometMock/cometmock/rpc_server"
	"github.com/informalsystems/CometMock/cometmock/storage"
)

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

	if len(os.Args) != 6 {
		logger.Error("Usage: <app-addresses> <genesis-file> <cometmock-listen-address> <node-homes> <abci-connection-mode>")
	}

	args := os.Args[1:]

	appAddresses := strings.Split(args[0], ",")
	genesisFile := args[1]
	cometMockListenAddress := args[2]
	nodeHomesString := args[3]
	connectionMode := args[4]

	// read node homes from args
	nodeHomes := strings.Split(nodeHomesString, ",")

	// get priv validators from node Homes
	privVals := GetMockPVsFromNodeHomes(nodeHomes)

	if connectionMode != "socket" && connectionMode != "grpc" {
		logger.Error("Invalid connection mode: %s. Connection mode must be either 'socket' or 'grpc'.", "connectionMode:", connectionMode)
	}

	genesisDoc, err := state.MakeGenesisDocFromFile(genesisFile)
	if err != nil {
		logger.Error(err.Error())
	}

	curState, err := state.MakeGenesisState(genesisDoc)
	if err != nil {
		logger.Error(err.Error())
	}

	clients := []abci_client.AbciCounterpartyClient{}
	privValsMap := make(map[string]types.PrivValidator)

	for i, appAddress := range appAddresses {
		logger.Info("Connecting to client at %v", appAddress)

		var client comet_abciclient.Client
		if connectionMode == "grpc" {
			client = comet_abciclient.NewGRPCClient(appAddress, true)
		} else {
			client = comet_abciclient.NewSocketClient(appAddress, true)
		}
		client.SetLogger(logger)
		client.Start()

		privVal := privVals[i]

		pubkey, err := privVal.GetPubKey()
		if err != nil {
			logger.Error(err.Error())
			panic(err)
		}
		validatorAddress := pubkey.Address()

		privValsMap[validatorAddress.String()] = privVal

		counterpartyClient := abci_client.NewAbciCounterpartyClient(client, appAddress, validatorAddress.String(), privVal)

		clients = append(clients, *counterpartyClient)

	}

	for _, privVal := range privVals {
		pubkey, err := privVal.GetPubKey()
		if err != nil {
			logger.Error(err.Error())
			panic(err)
		}
		addr := pubkey.Address()

		privValsMap[addr.String()] = privVal
	}

	abci_client.GlobalClient = abci_client.NewAbciClient(
		clients,
		logger,
		curState,
		&types.Block{},
		&types.Commit{},
		&storage.MapStorage{},
		privValsMap,
		true,
	)

	// connect to clients
	abci_client.GlobalClient.RetryDisconnectedClients()

	// initialize chain
	err = abci_client.GlobalClient.SendInitChain(curState, genesisDoc)
	if err != nil {
		logger.Error(err.Error())
		panic(err)
	}

	// run an empty block
	_, _, _, _, _, err = abci_client.GlobalClient.RunBlock(nil)
	if err != nil {
		logger.Error(err.Error())
		panic(err)
	}

	go rpc_server.StartRPCServerWithDefaultConfig(cometMockListenAddress, logger)

	// produce a block every second
	for {
		_, _, _, _, _, err := abci_client.GlobalClient.RunBlock(nil)
		if err != nil {
			logger.Error(err.Error())
			panic(err)
		}
		time.Sleep(1 * time.Second)
	}
}
