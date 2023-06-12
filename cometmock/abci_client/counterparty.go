package abci_client

import (
	abciclient "github.com/cometbft/cometbft/abci/client"
	"github.com/cometbft/cometbft/types"
)

// AbciCounterpartyClient is a wrapper around the ABCI client that is used to connect to the abci server.
// We keep extra information:
// * the address of the app
// * whether the app is alive
// * the priv validator associated with that app (i.e. its private key)
type AbciCounterpartyClient struct {
	Client           abciclient.Client
	NetworkAddress   string
	ValidatorAddress string
	isConnected      bool
	PrivValidator    types.PrivValidator
}

// NewAbciCounterpartyClient creates a new AbciCounterpartyClient.
// It always starts with isConnected = false.
// Call AbciClient.RetryDisconnectedClients to try to connect to the app.
func NewAbciCounterpartyClient(client abciclient.Client, networkAddress, validatorAddress string, privValidator types.PrivValidator) *AbciCounterpartyClient {
	return &AbciCounterpartyClient{
		Client:           client,
		NetworkAddress:   networkAddress,
		ValidatorAddress: validatorAddress,
		isConnected:      false,
		PrivValidator:    privValidator,
	}
}
