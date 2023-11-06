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
	genutiltypes "github.com/cosmos/cosmos-sdk/x/genutil/types"
	"github.com/informalsystems/CometMock/cometmock/abci_client"
	"github.com/informalsystems/CometMock/cometmock/rpc_server"
	"github.com/informalsystems/CometMock/cometmock/storage"
	"github.com/urfave/cli/v2"
)

const version = "v0.38.x"

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

	argumentString := "[--block-time=value] <app-addresses> <genesis-file> <cometmock-listen-address> <node-homes> <abci-connection-mode>"

	app := &cli.App{
		Name:            "cometmock",
		HideHelpCommand: true,
		Commands: []*cli.Command{
			{
				Name:  "version",
				Usage: "Print the version of cometmock",
				Action: func(c *cli.Context) error {
					fmt.Printf("%s\n", version)
					return nil
				},
			},
		},
		Flags: []cli.Flag{
			&cli.Int64Flag{
				Name: "block-time",
				Usage: `
Time between blocks in milliseconds.
To disable block production, set to 0.
This will not necessarily mean block production is this fast
- it is just the sleep time between blocks.
Setting this to a value <= 0 disables automatic block production.
In this case, blocks are only produced when instructed explicitly either by
advancing blocks or broadcasting transactions.`,
				Value: 1000,
			},
			&cli.BoolFlag{
				Name: "auto-include-tx",
				Usage: `
If this is true, transactions are included immediately
after they are received via broadcast_tx.
If this is false, transactions are included
upon creation of new blocks.
For full control over what transactions go into blocks,
set this to false and block-time to 0.`,
				Value: true,
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

			appGenesis, err := genutiltypes.AppGenesisFromFile(genesisFile)
			if err != nil {
				logger.Error(err.Error())
			}

			genesisDoc, err := appGenesis.ToGenesisDoc()
			if err != nil {
				logger.Error(err.Error())
				panic(err)
			}

			curState, err := state.MakeGenesisState(genesisDoc)
			if err != nil {
				logger.Error(err.Error())
				panic(err)
			}

			clientMap := make(map[string]abci_client.AbciCounterpartyClient)

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
				counterpartyClient := abci_client.NewAbciCounterpartyClient(client, appAddress, validatorAddress.String(), privVal)

				clientMap[validatorAddress.String()] = *counterpartyClient
			}

			abci_client.GlobalClient = abci_client.NewAbciClient(
				clientMap,
				logger,
				curState,
				&types.Block{},
				&types.ExtendedCommit{},
				&storage.MapStorage{},
				true,
			)

			abci_client.GlobalClient.AutoIncludeTx = c.Bool("auto-include-tx")
			fmt.Printf("Auto include tx: %t\n", abci_client.GlobalClient.AutoIncludeTx)

			// initialize chain
			err = abci_client.GlobalClient.SendInitChain(curState, genesisDoc)
			if err != nil {
				logger.Error(err.Error())
				panic(err)
			}

			// run an empty block
			_, _, _, err = abci_client.GlobalClient.RunBlock()
			if err != nil {
				logger.Error(err.Error())
				panic(err)
			}

			go rpc_server.StartRPCServerWithDefaultConfig(cometMockListenAddress, logger)

			if blockTime > 0 {
				// produce blocks according to blockTime
				for {
					_, _, _, err := abci_client.GlobalClient.RunBlock()
					if err != nil {
						logger.Error(err.Error())
						panic(err)
					}
					time.Sleep(time.Millisecond * time.Duration(blockTime))
				}
			} else {
				// wait forever
				time.Sleep(time.Hour * 24 * 365 * 100) // 100 years
			}
			return nil
		},
	}

	err := app.Run(os.Args)
	if err != nil {
		log.Fatal(err)
	}
}
