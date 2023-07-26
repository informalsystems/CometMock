package utils

import "github.com/cometbft/cometbft/types"

func GetBlockIdFromBlock(block *types.Block) (*types.BlockID, error) {
	partSet, err := block.MakePartSet(2)
	if err != nil {
		return nil, err
	}

	partSetHeader := partSet.Header()
	blockID := types.BlockID{
		Hash:          block.Hash(),
		PartSetHeader: partSetHeader,
	}
	return &blockID, nil
}
