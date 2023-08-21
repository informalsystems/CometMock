package main

import (
	"fmt"
	"log"
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
	"github.com/urfave/cli/v2"
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

	argumentString := "<app-addresses> <genesis-file> <cometmock-listen-address> <node-homes> <abci-connection-mode> [--block-time=value]"

	app := &cli.App{
		Name:            "cometmock",
		HideHelpCommand: true,
		Usage:           "Your application description",
		Flags: []cli.Flag{
			&cli.IntFlag{
				Name: "block-time",
				Usage: `
Time between blocks in milliseconds.
To disable block production, set to 0.
This will not necessarily mean block production is this fast
- it is just the sleep time between blocks.
<<<<<<< HEAD
<<<<<<< HEAD
<<<<<<< HEAD
<<<<<<< HEAD
Setting this to a value <= 0 disables automatic block production.
In this case, blocks are only produced when instructed explicitly either by
advancing blocks or broadcasting transactions.`,
				Value: 1000,
=======
Set this to -1 to disable automatic block production.
In this case, blocks are only produced when instructed explicitly either by
advancing blocks or broadcasting transactions.`,
				Value: 1,
>>>>>>> 03729f0 (Remove help command from binary, but leave --help flag)
=======
Setting this to a value <= 0 disables automatic block production.
In this case, blocks are only produced when instructed explicitly either by
advancing blocks or broadcasting transactions.`,
				Value: 1000,
>>>>>>> 0e63845 (Add logic to adhere to blocktime flag)
=======
Set this to -1 to disable automatic block production.
In this case, blocks are only produced when instructed explicitly either by
advancing blocks or broadcasting transactions.`,
				Value: 1,
>>>>>>> d333615 (Better behaviour when encountering argument errors)
=======
Set this to -1 to disable automatic block production.
In this case, blocks are only produced when instructed explicitly either by
advancing blocks or broadcasting transactions.`,
				Value: 1,
>>>>>>> d333615958054941bbff30c8ee945f8209e3837e
			},
		},
		ArgsUsage: argumentString,
		Action: func(c *cli.Context) error {
			if c.NArg() < 5 {
				return cli.Exit("Not enough arguments.\nUsage: "+argumentString, 1)
			}

			appAddresses := strings.Split(c.Args().Get(0), ",")
			genesisFile := c.Args().Get(1)
			cometMockListenAddress := c.Args().Get(2)
			nodeHomesString := c.Args().Get(3)
			connectionMode := c.Args().Get(4)

			if connectionMode != "socket" && connectionMode != "grpc" {
				return cli.Exit(fmt.Sprintf("Invalid connection mode: %s. Connection mode must be either 'socket' or 'grpc'.\nUsage: %s", connectionMode, argumentString), 1)
			}

			blockTime := c.Int("block-time")
			fmt.Printf("Block time: %d\n", blockTime)

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
		},
	}

	err := app.Run(os.Args)
	if err != nil {
		log.Fatal(err)
	}
}
