package utils

import "github.com/cometbft/cometbft/types"

func GetBlockIdFromBlock(block *types.Block) (*types.BlockID, error) {
	partSet := block.MakePartSet(2)

	partSetHeader := partSet.Header()
	blockID := types.BlockID{
		Hash:          block.Hash(),
		PartSetHeader: partSetHeader,
	}
	return &blockID, nil
}
