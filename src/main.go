package main

import (
	"log"
	"os"
	"strings"

	abciclient "github.com/cometbft/cometbft/abci/client"
	cometlog "github.com/cometbft/cometbft/libs/log"
	protostate "github.com/cometbft/cometbft/proto/tendermint/state"
	rpcserver "github.com/cometbft/cometbft/rpc/jsonrpc/server"
	"github.com/cometbft/cometbft/state"
)

func main() {
	logger := log.Default()
	cometLogger := cometlog.NewTMLogger(cometlog.NewSyncWriter(os.Stdout))

	if len(os.Args) != 4 {
		logger.Fatalf("Usage: <app-addresses> <genesis-file> <cometmock-listen-address>")
	}

	args := os.Args[1:]

	appAddresses := strings.Split(args[0], ",")
	genesisFile := args[1]
	cometMockListenAddress := args[2]

	genesisDoc, err := state.MakeGenesisDocFromFile(genesisFile)
	if err != nil {
		logger.Fatal(err.Error())
	}

	curState, err := state.MakeGenesisState(genesisDoc)
	if err != nil {
		logger.Fatalf(err.Error())
	}

	clients := []abciclient.Client{}

	for _, appAddress := range appAddresses {
		logger.Printf("Connecting to client at %v", appAddress)
		client := abciclient.NewGRPCClient(appAddress, true)
		client.SetLogger(cometLogger)
		client.Start()
		clients = append(clients, client)
	}

	// initialize chain
	newState, err := SendInitChain(clients, curState, genesisDoc)
	if err != nil {
		logger.Fatal(err.Error())
	}
	curState = *newState

	abciResponses := protostate.ABCIResponses{}

	// run a single block

	abciResponses.BeginBlock, err = SendBeginBlock(clients, curState)
	if err != nil {
		logger.Fatal(err.Error())
	}
	logger.Println(abciResponses.BeginBlock)

	abciResponses.EndBlock, err = SendEndBlock(clients, curState)
	if err != nil {
		logger.Fatal(err.Error())
	}
	logger.Println(abciResponses.EndBlock)

	responseCommit, err := SendCommit(clients)
	if err != nil {
		logger.Fatal(err.Error())
	}
	logger.Println(responseCommit)

	StartRPCServer(cometMockListenAddress, cometLogger, rpcserver.DefaultConfig())
}
