package utils

import (
	"time"

	cmttypes "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/cometbft/cometbft/types"
)

// MakeVote creates a signed vote.
// Adapted from https://github.com/cometbft/cometbft/blob/9267594e0a17c01cc4a97b399ada5eaa8a734db5/internal/test/vote.go#L10.
func MakeVote(
	val types.PrivValidator, // PrivValidator is the validator that will sign the vote.
	chainID string,
	valIndex int32,
	height int64,
	round int32,
	step int, // StepType is the step in the consensus process, see https://github.com/cometbft/cometbft/blob/9267594e0a17c01cc4a97b399ada5eaa8a734db5/proto/tendermint/types/types.pb.go#L68
	blockID types.BlockID,
	time time.Time,
) (*types.Vote, error) {
	pubKey, err := val.GetPubKey()
	if err != nil {
		return nil, err
	}

	v := &types.Vote{
		ValidatorAddress: pubKey.Address(),
		ValidatorIndex:   valIndex,
		Height:           height,
		Round:            round,
		Type:             cmttypes.SignedMsgType(step),
		BlockID:          blockID,
		Timestamp:        time,
	}

	vpb := v.ToProto()
	if err := val.SignVote(chainID, vpb); err != nil {
		return nil, err
	}

	v.Signature = vpb.Signature
	return v, nil
}
