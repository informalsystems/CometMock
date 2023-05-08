package rpc_server

import (
	"fmt"

	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/cometbft/cometbft/libs/bytes"
	cmtmath "github.com/cometbft/cometbft/libs/math"
	ctypes "github.com/cometbft/cometbft/rpc/core/types"
	rpc "github.com/cometbft/cometbft/rpc/jsonrpc/server"
	rpctypes "github.com/cometbft/cometbft/rpc/jsonrpc/types"
	"github.com/cometbft/cometbft/types"
	"github.com/p-offtermatt/CometMock/src/abci_client"
)

const (
	defaultPerPage = 30
	maxPerPage     = 100
)

var Routes = map[string]*rpc.RPCFunc{
	// info API
	"validators": rpc.NewRPCFunc(Validators, "height,page,per_page"),

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

func Validators(ctx *rpctypes.Context, heightPtr *int64, pagePtr, perPagePtr *int) (*ctypes.ResultValidators, error) {
	// only the last height is available, since we do not keep past heights at the moment
	if heightPtr != nil {
		return nil, fmt.Errorf("height parameter is not supported, use version of the function without height")
	}

	height := abci_client.GlobalClient.CurState.LastBlockHeight

	validators := abci_client.GlobalClient.CurState.LastValidators

	totalCount := len(validators.Validators)
	perPage := validatePerPage(perPagePtr)
	page, err := validatePage(pagePtr, perPage, totalCount)
	if err != nil {
		return nil, err
	}

	skipCount := validateSkipCount(page, perPage)

	v := validators.Validators[skipCount : skipCount+cmtmath.MinInt(perPage, totalCount-skipCount)]

	return &ctypes.ResultValidators{
		BlockHeight: height,
		Validators:  v,
		Count:       len(v),
		Total:       totalCount,
	}, nil
}

// validatePage is adapted from https://github.com/cometbft/cometbft/blob/9267594e0a17c01cc4a97b399ada5eaa8a734db5/rpc/core/env.go#L107
func validatePage(pagePtr *int, perPage, totalCount int) (int, error) {
	if perPage < 1 {
		panic(fmt.Sprintf("zero or negative perPage: %d", perPage))
	}

	if pagePtr == nil { // no page parameter
		return 1, nil
	}

	pages := ((totalCount - 1) / perPage) + 1
	if pages == 0 {
		pages = 1 // one page (even if it's empty)
	}
	page := *pagePtr
	if page <= 0 || page > pages {
		return 1, fmt.Errorf("page should be within [1, %d] range, given %d", pages, page)
	}

	return page, nil
}

// validatePerPage is adapted from https://github.com/cometbft/cometbft/blob/9267594e0a17c01cc4a97b399ada5eaa8a734db5/rpc/core/env.go#L128
func validatePerPage(perPagePtr *int) int {
	if perPagePtr == nil { // no per_page parameter
		return defaultPerPage
	}

	perPage := *perPagePtr
	if perPage < 1 {
		return defaultPerPage
	} else if perPage > maxPerPage {
		return maxPerPage
	}
	return perPage
}

// validateSkipCount is adapted from https://github.com/cometbft/cometbft/blob/9267594e0a17c01cc4a97b399ada5eaa8a734db5/rpc/core/env.go#L171
func validateSkipCount(page, perPage int) int {
	skipCount := (page - 1) * perPage
	if skipCount < 0 {
		return 0
	}

	return skipCount
}
