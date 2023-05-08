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

	abci_client.GlobalClient = &abci_client.AbciClient{
		Clients:                 clients,
		Logger:                  logger,
		CurState:                curState,
		ErrorOnUnequalResponses: false,
	}

	// initialize chain
	err = abci_client.GlobalClient.SendInitChain(curState, genesisDoc)
	if err != nil {
		logger.Error(err.Error())
		panic(err)
	}

	// run an empty block
	abci_client.GlobalClient.RunBlock(nil)

	rpc_server.StartRPCServerWithDefaultConfig(cometMockListenAddress, logger)
}
