package main

import (
	ctypes "github.com/cometbft/cometbft/rpc/core/types"
	rpc "github.com/cometbft/cometbft/rpc/jsonrpc/server"
	rpctypes "github.com/cometbft/cometbft/rpc/jsonrpc/types"
)

var Routes = map[string]*rpc.RPCFunc{
	// info API
	"block": rpc.NewRPCFunc(Block, "height", rpc.Cacheable("height")),

	// // tx broadcast API
	// "broadcast_tx_commit": rpc.NewRPCFunc(BroadcastTxCommit, "tx"),
	// "broadcast_tx_sync":   rpc.NewRPCFunc(BroadcastTxSync, "tx"),
	// "broadcast_tx_async":  rpc.NewRPCFunc(BroadcastTxAsync, "tx"),

	// // abci API
	// "abci_query": rpc.NewRPCFunc(ABCIQuery, "path,data,height,prove"),
}

func Block(ctx *rpctypes.Context, heightPtr *int64) (*ctypes.ResultBlock, error) {
	return nil, nil
}

// func BroadcastTxCommit(ctx *rpctypes.Context, tx types.Tx) (*ctypes.ResultBroadcastTxCommit, error) {
// }

// func BroadcastTxSync(ctx *rpctypes.Context, tx types.Tx) (*ctypes.ResultBroadcastTx, error) {
// }

// func BroadcastTxAsync(ctx *rpctypes.Context, tx types.Tx) (*ctypes.ResultBroadcastTx, error) {
// }

// func ABCIQuery(
// 	ctx *rpctypes.Context,
// 	path string,
// 	data bytes.HexBytes,
// 	height int64,
// 	prove bool,
// ) (*ctypes.ResultABCIQuery, error) {
// }
