package rpc_server

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/cometbft/cometbft/libs/bytes"
	cmtmath "github.com/cometbft/cometbft/libs/math"
	cmtquery "github.com/cometbft/cometbft/libs/pubsub/query"
	"github.com/cometbft/cometbft/p2p"
	cometp2p "github.com/cometbft/cometbft/p2p"
	ctypes "github.com/cometbft/cometbft/rpc/core/types"
	rpc "github.com/cometbft/cometbft/rpc/jsonrpc/server"
	rpctypes "github.com/cometbft/cometbft/rpc/jsonrpc/types"
	"github.com/cometbft/cometbft/types"
	"github.com/cometbft/cometbft/version"
	"github.com/informalsystems/CometMock/cometmock/abci_client"
	"github.com/informalsystems/CometMock/cometmock/utils"
)

const (
	defaultPerPage = 30
	maxPerPage     = 100
)

var Routes = map[string]*rpc.RPCFunc{
	// websocket
	"subscribe":       rpc.NewWSRPCFunc(Subscribe, "query"),
	"unsubscribe":     rpc.NewWSRPCFunc(Unsubscribe, "query"),
	"unsubscribe_all": rpc.NewWSRPCFunc(UnsubscribeAll, ""),

	// info API
	"health":           rpc.NewRPCFunc(Health, ""),
	"status":           rpc.NewRPCFunc(Status, ""),
	"validators":       rpc.NewRPCFunc(Validators, "height,page,per_page"),
	"block":            rpc.NewRPCFunc(Block, "height", rpc.Cacheable("height")),
	"consensus_params": rpc.NewRPCFunc(ConsensusParams, "height", rpc.Cacheable("height")),
	// "header":           rpc.NewRPCFunc(Header, "height", rpc.Cacheable("height")), // not available in 0.34.x
	"commit":        rpc.NewRPCFunc(Commit, "height", rpc.Cacheable("height")),
	"block_results": rpc.NewRPCFunc(BlockResults, "height", rpc.Cacheable("height")),
	"tx":            rpc.NewRPCFunc(Tx, "hash,prove", rpc.Cacheable()),
	"tx_search":     rpc.NewRPCFunc(TxSearch, "query,prove,page,per_page,order_by"),
	"block_search":  rpc.NewRPCFunc(BlockSearch, "query,page,per_page,order_by"),

	// tx broadcast API
	"broadcast_tx_commit": rpc.NewRPCFunc(BroadcastTxCommit, "tx"),
	"broadcast_tx_sync":   rpc.NewRPCFunc(BroadcastTxSync, "tx"),
	"broadcast_tx_async":  rpc.NewRPCFunc(BroadcastTxAsync, "tx"),

	// abci API
	"abci_query": rpc.NewRPCFunc(ABCIQuery, "path,data,height,prove"),

	// cometmock specific API
	"advance_blocks":            rpc.NewRPCFunc(AdvanceBlocks, "num_blocks"),
	"set_signing_status":        rpc.NewRPCFunc(SetSigningStatus, "private_key_address,status"),
	"advance_time":              rpc.NewRPCFunc(AdvanceTime, "duration_in_seconds"),
	"cause_double_sign":         rpc.NewRPCFunc(CauseDoubleSign, "private_key_address"),
	"cause_light_client_attack": rpc.NewRPCFunc(CauseLightClientAttack, "private_key_address"),
}

type ResultCauseLightClientAttack struct{}

func CauseLightClientAttack(ctx *rpctypes.Context, privateKeyAddress string) (*ResultCauseLightClientAttack, error) {
	err := abci_client.GlobalClient.CauseLightClientAttack(privateKeyAddress)
	return &ResultCauseLightClientAttack{}, err
}

type ResultCauseDoubleSign struct{}

func CauseDoubleSign(ctx *rpctypes.Context, privateKeyAddress string) (*ResultCauseDoubleSign, error) {
	err := abci_client.GlobalClient.CauseDoubleSign(privateKeyAddress)
	return &ResultCauseDoubleSign{}, err
}

type ResultAdvanceTime struct {
	NewTime time.Time `json:"new_time"`
}

// AdvanceTime advances the block time by the given duration.
// This API is specific to CometMock.
func AdvanceTime(ctx *rpctypes.Context, duration_in_seconds time.Duration) (*ResultAdvanceTime, error) {
	if duration_in_seconds < 0 {
		return nil, errors.New("duration to advance time by must be greater than 0")
	}

	abci_client.GlobalClient.IncrementTimeOffset(duration_in_seconds * time.Second)
	return &ResultAdvanceTime{time.Now().Add(abci_client.GlobalClient.GetTimeOffset())}, nil
}

type ResultSetSigningStatus struct {
	NewSigningStatusMap map[string]bool `json:"new_signing_status_map"`
}

func SetSigningStatus(ctx *rpctypes.Context, privateKeyAddress string, status string) (*ResultSetSigningStatus, error) {
	if status != "down" && status != "up" {
		return nil, errors.New("status must be either `up` to have the validator sign, or `down` to have the validator not sign")
	}

	err := abci_client.GlobalClient.SetSigningStatus(privateKeyAddress, status == "up")

	return &ResultSetSigningStatus{
		NewSigningStatusMap: abci_client.GlobalClient.GetSigningStatusMap(),
	}, err
}

type ResultAdvanceBlocks struct{}

// AdvanceBlocks advances the block height by numBlocks, running empty blocks.
// This API is specific to CometMock.
func AdvanceBlocks(ctx *rpctypes.Context, numBlocks int) (*ResultAdvanceBlocks, error) {
	if numBlocks < 1 {
		return nil, errors.New("num_blocks must be greater than 0")
	}

	err := abci_client.GlobalClient.RunEmptyBlocks(numBlocks)
	if err != nil {
		return nil, err
	}
	return &ResultAdvanceBlocks{}, nil
}

// BlockSearch searches for a paginated set of blocks matching BeginBlock and
// EndBlock event search criteria.
func BlockSearch(
	ctx *rpctypes.Context,
	query string,
	pagePtr, perPagePtr *int,
	orderBy string,
) (*ctypes.ResultBlockSearch, error) {
	q, err := cmtquery.New(query)
	if err != nil {
		return nil, err
	}

	results, err := abci_client.GlobalClient.BlockIndex.Search(ctx.Context(), q)
	if err != nil {
		return nil, err
	}

	// sort results (must be done before pagination)
	switch orderBy {
	case "desc", "":
		sort.Slice(results, func(i, j int) bool { return results[i] > results[j] })

	case "asc":
		sort.Slice(results, func(i, j int) bool { return results[i] < results[j] })

	default:
		return nil, errors.New("expected order_by to be either `asc` or `desc` or empty")
	}

	// paginate results
	totalCount := len(results)
	perPage := validatePerPage(perPagePtr)

	page, err := validatePage(pagePtr, perPage, totalCount)
	if err != nil {
		return nil, err
	}

	skipCount := validateSkipCount(page, perPage)
	pageSize := cmtmath.MinInt(perPage, totalCount-skipCount)

	apiResults := make([]*ctypes.ResultBlock, 0, pageSize)
	for i := skipCount; i < skipCount+pageSize; i++ {
		block, err := abci_client.GlobalClient.Storage.GetBlock(results[i])
		if err != nil {
			return nil, err
		}
		if block != nil {
			if err != nil {
				return nil, err
			}
			blockId, err := utils.GetBlockIdFromBlock(block)
			if err != nil {
				return nil, err
			}

			apiResults = append(apiResults, &ctypes.ResultBlock{
				Block:   block,
				BlockID: *blockId,
			})
		}
	}

	return &ctypes.ResultBlockSearch{Blocks: apiResults, TotalCount: totalCount}, nil
}

// Tx allows you to query the transaction results. `nil` could mean the
// transaction is in the mempool, invalidated, or was not sent in the first
// place.
// More: https://docs.tendermint.com/v0.34/rpc/#/Info/tx
func Tx(ctx *rpctypes.Context, hash []byte, prove bool) (*ctypes.ResultTx, error) {
	txIndexer := abci_client.GlobalClient.TxIndex

	r, err := txIndexer.Get(hash)
	if err != nil {
		return nil, err
	}

	if r == nil {
		return nil, fmt.Errorf("tx (%X) not found", hash)
	}

	height := r.Height
	index := r.Index

	var proof types.TxProof
	if prove {
		block, err := abci_client.GlobalClient.Storage.GetBlock(height)
		if err != nil {
			return nil, err
		}
		proof = block.Data.Txs.Proof(int(index)) // XXX: overflow on 32-bit machines
	}

	return &ctypes.ResultTx{
		Hash:     hash,
		Height:   height,
		Index:    index,
		TxResult: r.Result,
		Tx:       r.Tx,
		Proof:    proof,
	}, nil
}

// TxSearch allows you to query for multiple transactions results. It returns a
// list of transactions (maximum ?per_page entries) and the total count.
// More: https://docs.tendermint.com/v0.34/rpc/#/Info/tx_search
func TxSearch(
	ctx *rpctypes.Context,
	query string,
	prove bool,
	pagePtr, perPagePtr *int,
	orderBy string,
) (*ctypes.ResultTxSearch, error) {
	if len(query) > maxQueryLength {
		return nil, errors.New("maximum query length exceeded")
	}

	q, err := cmtquery.New(query)
	if err != nil {
		return nil, err
	}

	results, err := abci_client.GlobalClient.TxIndex.Search(ctx.Context(), q)
	if err != nil {
		return nil, err
	}

	// sort results (must be done before pagination)
	switch orderBy {
	case "desc":
		sort.Slice(results, func(i, j int) bool {
			if results[i].Height == results[j].Height {
				return results[i].Index > results[j].Index
			}
			return results[i].Height > results[j].Height
		})
	case "asc", "":
		sort.Slice(results, func(i, j int) bool {
			if results[i].Height == results[j].Height {
				return results[i].Index < results[j].Index
			}
			return results[i].Height < results[j].Height
		})
	default:
		return nil, errors.New("expected order_by to be either `asc` or `desc` or empty")
	}

	// paginate results
	totalCount := len(results)
	perPage := validatePerPage(perPagePtr)

	page, err := validatePage(pagePtr, perPage, totalCount)
	if err != nil {
		return nil, err
	}

	skipCount := validateSkipCount(page, perPage)
	pageSize := cmtmath.MinInt(perPage, totalCount-skipCount)

	apiResults := make([]*ctypes.ResultTx, 0, pageSize)
	for i := skipCount; i < skipCount+pageSize; i++ {
		r := results[i]

		var proof types.TxProof
		if prove {
			block, err := abci_client.GlobalClient.Storage.GetBlock(r.Height)
			if err != nil {
				return nil, err
			}
			proof = block.Data.Txs.Proof(int(r.Index)) // XXX: overflow on 32-bit machines
		}

		apiResults = append(apiResults, &ctypes.ResultTx{
			Hash:     types.Tx(r.Tx).Hash(),
			Height:   r.Height,
			Index:    r.Index,
			TxResult: r.Result,
			Tx:       r.Tx,
			Proof:    proof,
		})
	}

	return &ctypes.ResultTxSearch{Txs: apiResults, TotalCount: totalCount}, nil
}

func getHeight(latestHeight int64, heightPtr *int64) (int64, error) {
	if heightPtr != nil {
		height := *heightPtr
		if height <= 0 {
			return 0, fmt.Errorf("height must be greater than 0, but got %d", height)
		}
		if height > latestHeight {
			return 0, fmt.Errorf("height %d must be less than or equal to the current blockchain height %d",
				height, latestHeight)
		}
		return height, nil
	}
	return latestHeight, nil
}

// // Header gets block header at a given height.
// // If no height is provided, it will fetch the latest header.
// // More: https://docs.cometbft.com/v0.37/rpc/#/Info/header
// func Header(ctx *rpctypes.Context, heightPtr *int64) (*ctypes.ResultHeader, error) {
// 	height, err := getHeight(abci_client.GlobalClient.LastBlock.Height, heightPtr)
// 	if err != nil {
// 		return nil, err
// 	}

// 	block, err := abci_client.GlobalClient.Storage.GetBlock(height)
// 	if err != nil {
// 		return nil, err
// 	}

// 	return &ctypes.ResultHeader{Header: &block.Header}, nil
// }

// Commit gets block commit at a given height.
// If no height is provided, it will fetch the commit for the latest block.
// More: https://docs.cometbft.com/main/rpc/#/Info/commit
func Commit(ctx *rpctypes.Context, heightPtr *int64) (*ctypes.ResultCommit, error) {
	height, err := getHeight(abci_client.GlobalClient.LastBlock.Height, heightPtr)
	if err != nil {
		return nil, err
	}

	commit, err := abci_client.GlobalClient.Storage.GetCommit(height)
	if err != nil {
		return nil, err
	}

	block, err := abci_client.GlobalClient.Storage.GetBlock(height)
	if err != nil {
		return nil, err
	}

	return ctypes.NewResultCommit(&block.Header, commit, true), nil
}

// ConsensusParams gets the consensus parameters at the given block height.
// If no height is provided, it will fetch the latest consensus params.
// More: https://docs.cometbft.com/v0.37/rpc/#/Info/consensus_params
func ConsensusParams(ctx *rpctypes.Context, heightPtr *int64) (*ctypes.ResultConsensusParams, error) {
	height, err := getHeight(abci_client.GlobalClient.LastBlock.Height, heightPtr)
	if err != nil {
		return nil, err
	}

	stateForHeight, err := abci_client.GlobalClient.Storage.GetState(height)
	if err != nil {
		return nil, err
	}

	consensusParams := stateForHeight.ConsensusParams

	return &ctypes.ResultConsensusParams{
		BlockHeight:     height,
		ConsensusParams: consensusParams,
	}, nil
}

// Status returns CometBFT status including node info, pubkey, latest block
// hash, app hash, block height and time.
// More: https://docs.cometbft.com/v0.37/rpc/#/Info/status
func Status(ctx *rpctypes.Context) (*ctypes.ResultStatus, error) {
	// return status as if we are the first validator
	curState := abci_client.GlobalClient.CurState
	validator := curState.Validators.Validators[0]

	nodeInfo := cometp2p.DefaultNodeInfo{
		DefaultNodeID: cometp2p.PubKeyToID(validator.PubKey),
		Network:       abci_client.GlobalClient.CurState.ChainID,
		Other: cometp2p.DefaultNodeInfoOther{
			TxIndex: "on",
		},
		Version: "0.34.20",
		ProtocolVersion: p2p.NewProtocolVersion(
			version.P2PProtocol, // global
			curState.Version.Consensus.Block,
			curState.Version.Consensus.App,
		),
	}
	syncInfo := ctypes.SyncInfo{
		LatestBlockHash:   abci_client.GlobalClient.LastBlock.Hash(),
		LatestAppHash:     abci_client.GlobalClient.LastBlock.AppHash,
		LatestBlockHeight: abci_client.GlobalClient.LastBlock.Height,
		LatestBlockTime:   abci_client.GlobalClient.CurState.LastBlockTime,
		CatchingUp:        false,
	}
	validatorInfo := ctypes.ValidatorInfo{
		Address:     validator.Address,
		PubKey:      validator.PubKey,
		VotingPower: validator.VotingPower,
	}
	result := &ctypes.ResultStatus{
		NodeInfo:      nodeInfo,
		SyncInfo:      syncInfo,
		ValidatorInfo: validatorInfo,
	}

	return result, nil
}

// Health gets node health. Returns empty result (200 OK) on success, no
// response - in case of an error.
func Health(ctx *rpctypes.Context) (*ctypes.ResultHealth, error) {
	return &ctypes.ResultHealth{}, nil
}

// BroadcastTxCommit broadcasts a transaction,
// and wait until it is included in a block and and comitted.
// In our case, this means running a block with just the the transition,
// then return.
func BroadcastTxCommit(ctx *rpctypes.Context, tx types.Tx) (*ctypes.ResultBroadcastTxCommit, error) {
	abci_client.GlobalClient.Logger.Info(
		"BroadcastTxCommit called", "tx", tx)

	res, err := BroadcastTx(&tx)
	return res, err
}

// BroadcastTxSync would normally broadcast a transaction and wait until it gets the result from CheckTx.
// In our case, we run a block with just the transition in it,
// then return.
func BroadcastTxSync(ctx *rpctypes.Context, tx types.Tx) (*ctypes.ResultBroadcastTx, error) {
	abci_client.GlobalClient.Logger.Info(
		"BroadcastTxSync called", "tx", tx)

	resBroadcastTx, err := BroadcastTx(&tx)
	if err != nil {
		return nil, err
	}

	return &ctypes.ResultBroadcastTx{
		Code:      resBroadcastTx.CheckTx.Code,
		Data:      resBroadcastTx.CheckTx.Data,
		Log:       resBroadcastTx.CheckTx.Log,
		Hash:      resBroadcastTx.Hash,
		Codespace: resBroadcastTx.CheckTx.Codespace,
	}, nil
}

// BroadcastTxAsync would normally broadcast a transaction and return immediately.
// In our case, we always include the transition in the next block, and return when that block is committed.
// ResultBroadcastTx is empty, since we do not return the result of CheckTx nor DeliverTx.
func BroadcastTxAsync(ctx *rpctypes.Context, tx types.Tx) (*ctypes.ResultBroadcastTx, error) {
	abci_client.GlobalClient.Logger.Info(
		"BroadcastTxAsync called", "tx", tx)

	_, err := BroadcastTx(&tx)
	if err != nil {
		return nil, err
	}

	return &ctypes.ResultBroadcastTx{}, nil
}

// BroadcastTx delivers a transaction to the ABCI client, includes it in the next block, then returns.
func BroadcastTx(tx *types.Tx) (*ctypes.ResultBroadcastTxCommit, error) {
	abci_client.GlobalClient.Logger.Info(
		"BroadcastTxs called", "tx", tx)

	byteTx := []byte(*tx)

	_, responseCheckTx, responseDeliverTx, _, _, err := abci_client.GlobalClient.RunBlock(&byteTx)
	if err != nil {
		return nil, err
	}

	// TODO: fill the return value if necessary
	return &ctypes.ResultBroadcastTxCommit{
		CheckTx:   *responseCheckTx,
		DeliverTx: *responseDeliverTx,
		Height:    abci_client.GlobalClient.LastBlock.Height,
		Hash:      tx.Hash(),
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

	abci_client.GlobalClient.Logger.Info(
		"Response to ABCI query", response.String())
	return &ctypes.ResultABCIQuery{Response: *response}, err
}

func Validators(ctx *rpctypes.Context, heightPtr *int64, pagePtr, perPagePtr *int) (*ctypes.ResultValidators, error) {
	height, err := getHeight(abci_client.GlobalClient.LastBlock.Height, heightPtr)
	if err != nil {
		return nil, err
	}

	pastState, err := abci_client.GlobalClient.Storage.GetState(height)
	if err != nil {
		return nil, err
	}

	validators := pastState.Validators

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

func Block(ctx *rpctypes.Context, heightPtr *int64) (*ctypes.ResultBlock, error) {
	height, err := getHeight(abci_client.GlobalClient.LastBlock.Height, heightPtr)
	if err != nil {
		return nil, err
	}

	block, err := abci_client.GlobalClient.Storage.GetBlock(height)
	if err != nil {
		return nil, err
	}

	blockID, err := utils.GetBlockIdFromBlock(block)
	if err != nil {
		return nil, err
	}

	return &ctypes.ResultBlock{BlockID: *blockID, Block: abci_client.GlobalClient.LastBlock}, nil
}

// BlockResults gets ABCIResults at a given height.
// If no height is provided, it will fetch results for the latest block.
//
// Results are for the height of the block containing the txs.
// Thus response.results.deliver_tx[5] is the results of executing
// getBlock(h).Txs[5]
// More: https://docs.cometbft.com/v0.37/rpc/#/Info/block_results
func BlockResults(ctx *rpctypes.Context, heightPtr *int64) (*ctypes.ResultBlockResults, error) {
	height, err := getHeight(abci_client.GlobalClient.LastBlock.Height, heightPtr)
	if err != nil {
		return nil, err
	}

	results, err := abci_client.GlobalClient.Storage.GetResponses(height)
	if err != nil {
		return nil, err
	}

	return &ctypes.ResultBlockResults{
		Height:                height,
		TxsResults:            results.DeliverTxs,
		BeginBlockEvents:      results.BeginBlock.Events,
		EndBlockEvents:        results.EndBlock.Events,
		ValidatorUpdates:      results.EndBlock.ValidatorUpdates,
		ConsensusParamUpdates: results.EndBlock.ConsensusParamUpdates,
	}, nil
}
