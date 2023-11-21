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

	argumentString := "[--block-time=value] [--auto-tx=<value>] [--block-production-interval=<value>] [--starting-timestamp=<value>] [--starting-timestamp-from-genesis=<value>] <app-addresses> <genesis-file> <cometmock-listen-address> <node-homes> <abci-connection-mode>"

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
The number of milliseconds by which the block timestamp should advance from one block to the next.
If this is <0, block timestamps will advance with the system time between the block productions.
Even then, it is still possible to shift the block time from the system time, e.g. by setting an initial timestamp
or by using the 'advance_time' endpoint.`,
				Value: -1,
			},
			&cli.BoolFlag{
				Name: "auto-tx",
				Usage: `
If this is true, transactions are included immediately
after they are received via broadcast_tx, i.e. a new block
is created when a BroadcastTx endpoint is hit.
If this is false, transactions are still included
upon creation of new blocks, but CometMock will not specifically produce
a new block when a transaction is broadcast.`,
				Value: true,
			},
			&cli.Int64Flag{
				Name: "block-production-interval",
				Usage: `
Time to sleep between blocks in milliseconds.
To disable block production, set to 0.
This will not necessarily mean block production is this fast
- it is just the sleep time between blocks.
Setting this to a value < 0 disables automatic block production.
In this case, blocks are only produced when instructed explicitly either by
advancing blocks or broadcasting transactions.`,
				Value: 1000,
			},
			&cli.Int64Flag{
				Name: "starting-timestamp",
				Usage: `
The timestamp to use for the first block, given in milliseconds since the unix epoch.
If this is < 0, the current system time is used.
If this is >= 0, the system time is ignored and this timestamp is used for the first block instead.`,
				Value: -1,
			},
			&cli.BoolFlag{
				Name: "starting-timestamp-from-genesis",
				Usage: `
If this is true, it overrides the starting-timestamp, and instead
bases the time for the first block on the genesis time, incremented by the block time
or the system time between creating the genesis request and producing the first block.`,
				Value: false,
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

			blockProductionInterval := c.Int("block-production-interval")
			fmt.Printf("Block production interval: %d\n", blockProductionInterval)

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

			// read starting timestamp from args
			// if starting timestamp should be taken from genesis,
			// read it from there
			var startingTime time.Time
			if c.Bool("starting-timestamp-from-genesis") {
				startingTime = genesisDoc.GenesisTime
			} else {
				if c.Int64("starting-timestamp") < 0 {
					startingTime = time.Now()
				} else {
					dur := time.Duration(c.Int64("starting-timestamp")) * time.Millisecond
					startingTime = time.Unix(0, 0).Add(dur)
				}
			}
			fmt.Printf("Starting time: %s\n", startingTime.Format(time.RFC3339))

			// read block time from args
			blockTime := time.Duration(c.Int64("block-time")) * time.Millisecond
			fmt.Printf("Block time: %d\n", blockTime.Milliseconds())

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

			var timeHandler abci_client.TimeHandler
			if blockTime < 0 {
				timeHandler = abci_client.NewSystemClockTimeHandler(startingTime)
			} else {
				timeHandler = abci_client.NewFixedBlockTimeHandler(blockTime)
			}

			abci_client.GlobalClient = abci_client.NewAbciClient(
				clientMap,
				logger,
				curState,
				&types.Block{},
				&types.ExtendedCommit{},
				&storage.MapStorage{},
				timeHandler,
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

			var firstBlockTime time.Time
			if blockTime < 0 {
				firstBlockTime = startingTime
			} else {
				firstBlockTime = startingTime.Add(blockTime)
			}

			// run an empty block
			err = abci_client.GlobalClient.RunBlockWithTime(firstBlockTime)
			if err != nil {
				logger.Error(err.Error())
				panic(err)
			}

			go rpc_server.StartRPCServerWithDefaultConfig(cometMockListenAddress, logger)

			if blockProductionInterval > 0 {
				// produce blocks according to blockTime
				for {
					err := abci_client.GlobalClient.RunBlock()
					if err != nil {
						logger.Error(err.Error())
						panic(err)
					}
					time.Sleep(time.Millisecond * time.Duration(blockProductionInterval))
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
