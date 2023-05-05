package main

import (
	"os"
	"strings"

	comet_abciclient "github.com/cometbft/cometbft/abci/client"
	cometlog "github.com/cometbft/cometbft/libs/log"
	"github.com/cometbft/cometbft/state"
	"github.com/p-offtermatt/CometMock/src/abci_client"
	"github.com/p-offtermatt/CometMock/src/rpc_server"
)

func main() {
	logger := cometlog.NewTMLogger(cometlog.NewSyncWriter(os.Stdout))

	if len(os.Args) != 4 {
		logger.Error("Usage: <app-addresses> <genesis-file> <cometmock-listen-address>")
	}

	args := os.Args[1:]

	appAddresses := strings.Split(args[0], ",")
	genesisFile := args[1]
	cometMockListenAddress := args[2]

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

	abci_client.GlobalClient.Clients = clients
	abci_client.GlobalClient.Logger = logger
	abci_client.GlobalClient.CurState = curState

	// // initialize chain
	// newState, err := SendInitChain(clients, curState, genesisDoc)
	// if err != nil {
	// 	logger.Fatal(err.Error())
	// }
	// curState = *newState

	// abciResponses := protostate.ABCIResponses{}

	// // run a single block

	// abciResponses.BeginBlock, err = SendBeginBlock(clients, curState)
	// if err != nil {
	// 	logger.Fatal(err.Error())
	// }
	// logger.Println(abciResponses.BeginBlock)

	// abciResponses.EndBlock, err = SendEndBlock(clients, curState)
	// if err != nil {
	// 	logger.Fatal(err.Error())
	// }
	// logger.Println(abciResponses.EndBlock)

	// responseCommit, err := SendCommit(clients)
	// if err != nil {
	// 	logger.Fatal(err.Error())
	// }
	// logger.Println(responseCommit)

	rpc_server.StartRPCServerWithDefaultConfig(cometMockListenAddress, logger)
}
