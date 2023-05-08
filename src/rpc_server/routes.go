package rpc_server

import (
	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/cometbft/cometbft/libs/bytes"
	ctypes "github.com/cometbft/cometbft/rpc/core/types"
	rpc "github.com/cometbft/cometbft/rpc/jsonrpc/server"
	rpctypes "github.com/cometbft/cometbft/rpc/jsonrpc/types"
	"github.com/cometbft/cometbft/types"
	"github.com/p-offtermatt/CometMock/src/abci_client"
)

var Routes = map[string]*rpc.RPCFunc{
	// // tx broadcast API
	// "broadcast_tx_commit": rpc.NewRPCFunc(BroadcastTxCommit, "tx"),
	"broadcast_tx_sync": rpc.NewRPCFunc(BroadcastTxSync, "tx"),
	// "broadcast_tx_async": rpc.NewRPCFunc(BroadcastTxAsync, "tx"),

	// // abci API
	"abci_query": rpc.NewRPCFunc(ABCIQuery, "path,data,height,prove"),
}

// func BroadcastTxCommit(ctx *rpctypes.Context, tx types.Tx) (*ctypes.ResultBroadcastTxCommit, error) {
// }

// BroadcastTxSync would normally broadcast a transaction and wait until it gets the result from CheckTx.
// In our case, we always include the transition in the next block, and return when that block is committed.
func BroadcastTxSync(ctx *rpctypes.Context, tx types.Tx) (*ctypes.ResultBroadcastTx, error) {
	abci_client.GlobalClient.Logger.Info(
		"BroadcastTxSync called", "tx", tx)

	res, err := BroadcastTxs(&tx)
	if err != nil {
		return nil, err
	}

	return &ctypes.ResultBroadcastTx{
		Code:      res.CheckTx.Code,
		Data:      res.CheckTx.Data,
		Log:       res.CheckTx.Log,
		Codespace: res.CheckTx.Codespace,
		Hash:      tx.Hash(),
	}, nil
}

// BroadcastTxAsync would normally broadcast a transaction and return immediately.
// In our case, we always include the transition in the next block, and return when that block is committed.
// func BroadcastTxAsync(ctx *rpctypes.Context, tx types.Tx) (*ctypes.ResultBroadcastTx, error) {
// }

// BroadcastTx delivers a transaction to the ABCI client, includes it in the next block, then returns.
func BroadcastTxs(tx *types.Tx) (*ctypes.ResultBroadcastTxCommit, error) {
	abci_client.GlobalClient.Logger.Info(
		"BroadcastTxs called", "tx", tx)

	byteTx := []byte(*tx)

	_, deliverTxRes, _, _, err := abci_client.GlobalClient.RunBlock(&byteTx)
	if err != nil {
		return nil, err
	}

	return &ctypes.ResultBroadcastTxCommit{
		CheckTx:   abci.ResponseCheckTx{}, // TODO: actually check the tx if it is ever necessary
		DeliverTx: *deliverTxRes,
		Hash:      tx.Hash(),
		Height:    abci_client.GlobalClient.CurState.LastBlockHeight,
	}, nil
}

func ABCIQuery(
	ctx *rpctypes.Context,
	path string,
	data bytes.HexBytes,
	height int64,
	prove bool,
) (*ctypes.ResultABCIQuery, error) {
	abci_client.GlobalClient.Logger.Info(
		"ABCIQuery called", "path", "data", "height", "prove", path, data, height, prove)

	response, err := abci_client.GlobalClient.SendAbciQuery(data, path, height, prove)
	return &ctypes.ResultABCIQuery{Response: *response}, err
}
