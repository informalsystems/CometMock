package utils

import (
	"bytes"
	"fmt"

	abci "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/cometbft/cometbft/types"
)

// mostly copy-pasted/adjusted from https://github.com/p-offtermatt/cometbft/blob/ph/make-public/state/execution.go#L486
// BuildExtendedCommitInfo populates an ABCI extended commit from the
// corresponding CometBFT extended commit ec, using the stored validator set
// from ec.  It requires ec to include the original precommit votes along with
// the vote extensions from the last commit.
//
// For heights below the initial height, for which we do not have the required
// data, it returns an empty record.
//
// Assumes that the commit signatures are sorted according to validator index.
func BuildExtendedCommitInfo(ec *types.ExtendedCommit, valSet *types.ValidatorSet, initialHeight int64, ap types.ABCIParams) abci.ExtendedCommitInfo {
	if ec.Height < initialHeight {
		// There are no extended commits for heights below the initial height.
		return abci.ExtendedCommitInfo{}
	}

	var (
		ecSize    = ec.Size()
		valSetLen = len(valSet.Validators)
	)

	// Ensure that the size of the validator set in the extended commit matches
	// the size of the validator set in the state store.
	if ecSize != valSetLen {
		panic(fmt.Errorf(
			"extended commit size (%d) does not match validator set length (%d) at height %d\n\n%v\n\n%v",
			ecSize, valSetLen, ec.Height, ec.ExtendedSignatures, valSet.Validators,
		))
	}

	votes := make([]abci.ExtendedVoteInfo, ecSize)
	for i, val := range valSet.Validators {
		ecs := ec.ExtendedSignatures[i]

		// Absent signatures have empty validator addresses, but otherwise we
		// expect the validator addresses to be the same.
		if ecs.BlockIDFlag != types.BlockIDFlagAbsent && !bytes.Equal(ecs.ValidatorAddress, val.Address) {
			panic(fmt.Errorf("validator address of extended commit signature in position %d (%s) does not match the corresponding validator's at height %d (%s)",
				i, ecs.ValidatorAddress, ec.Height, val.Address,
			))
		}

		// Check if vote extensions were enabled during the commit's height: ec.Height.
		// ec is the commit from the previous height, so if extensions were enabled
		// during that height, we ensure they are present and deliver the data to
		// the proposer. If they were not enabled during this previous height, we
		// will not deliver extension data.
		if err := ecs.EnsureExtension(ap.VoteExtensionsEnabled(ec.Height)); err != nil {
			panic(fmt.Errorf("commit at height %d has problems with vote extension data; err %w", ec.Height, err))
		}

		votes[i] = abci.ExtendedVoteInfo{
			Validator:          types.TM2PB.Validator(val),
			BlockIdFlag:        cmtproto.BlockIDFlag(ecs.BlockIDFlag),
			VoteExtension:      ecs.Extension,
			ExtensionSignature: ecs.ExtensionSignature,
		}
	}

	return abci.ExtendedCommitInfo{
		Round: ec.Round,
		Votes: votes,
	}
}

func BuildLastCommitInfo(block *types.Block, lastValSet *types.ValidatorSet, initialHeight int64) abci.CommitInfo {
	if block.Height == initialHeight {
		// there is no last commit for the initial height.
		// return an empty value.
		return abci.CommitInfo{}
	}

	var (
		commitSize = block.LastCommit.Size()
		valSetLen  = len(lastValSet.Validators)
	)

	// ensure that the size of the validator set in the last commit matches
	// the size of the validator set in the state store.
	if commitSize != valSetLen {
		panic(fmt.Sprintf(
			"commit size (%d) doesn't match validator set length (%d) at height %d\n\n%v\n\n%v",
			commitSize, valSetLen, block.Height, block.LastCommit.Signatures, lastValSet.Validators,
		))
	}

	votes := make([]abci.VoteInfo, block.LastCommit.Size())
	for i, val := range lastValSet.Validators {
		commitSig := block.LastCommit.Signatures[i]
		votes[i] = abci.VoteInfo{
			Validator:   types.TM2PB.Validator(val),
			BlockIdFlag: cmtproto.BlockIDFlag(commitSig.BlockIDFlag),
		}
	}

	return abci.CommitInfo{
		Round: block.LastCommit.Round,
		Votes: votes,
	}
}
