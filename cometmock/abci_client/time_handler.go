package abci_client

import (
	"sync"
	"time"
)

// A TimeHandler is responsible for
// deciding the timestamps of blocks.
// It will be called by AbciClient.RunBlock
// to decide on a block time.
// It may decide the time based on any number of factors,
// and the parameters of its methods might expand over time as needed.
// The TimeHandler does not have a way to decide the time of the first block,
// which is expected to be done externally, e.g. from the Genesis.
type TimeHandler interface {
	// CONTRACT: TimeHandler.GetBlockTime will be called
	// precisely once for each block after the first.
	// It returns the timestamp of the next block.
	GetBlockTime(lastBlockTimestamp time.Time) time.Time

	// AdvanceTime advances the timestamp of all following blocks by
	// the given duration.
	// The duration needs to be non-negative.
	// It returns the timestamp that the next block would have if it
	// was produced now.
	AdvanceTime(duration time.Duration) time.Time
}

// The SystemClockTimeHandler uses the system clock
// to decide the timestamps of blocks.
// It will return the system time + offset for each block.
// The offset is calculated by the initial timestamp
// + the sum of all durations passed to AdvanceTime.
type SystemClockTimeHandler struct {
	// The offset to add to the system time.
	curOffset time.Duration

	// A mutex that ensures that there are no concurrent calls
	// to AdvanceTime
	mutex sync.Mutex
}

func NewSystemClockTimeHandler(initialTimestamp time.Time) *SystemClockTimeHandler {
	return &SystemClockTimeHandler{
		curOffset: time.Since(initialTimestamp),
	}
}

func (s *SystemClockTimeHandler) GetBlockTime(lastBlockTimestamp time.Time) time.Time {
	return time.Now().Add(s.curOffset)
}

func (s *SystemClockTimeHandler) AdvanceTime(duration time.Duration) time.Time {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	s.curOffset += duration
	return time.Now().Add(s.curOffset)
}

var _ TimeHandler = (*SystemClockTimeHandler)(nil)

// The FixedBlockTimeHandler uses a fixed duration
// to advance the timestamp of a block compared to the previous block.
// The block timestamps therefore do not at all depend on the system time,
// but on the time of the previous block.
type FixedBlockTimeHandler struct {
	// The fixed duration to add to the last block time
	// when deciding the next block timestamp.
	blockTime time.Duration

	// The offset to add to the last block time.
	// This will be cleared after each block,
	// but since the block time of the next block depends
	// on the last block,
	// this will shift the timestamps of all future blocks.
	curBlockOffset time.Duration

	// A mutex that ensures that GetBlockTime and AdvanceTime
	// are not called concurrently.
	// Otherwise, the block offset might be put into a broken state.
	mutex sync.Mutex

	// The timestamp of the last block we produced.
	// If this is used before the first block is produced,
	// it will be the zero time.
	lastBlockTimestamp time.Time
}

func NewFixedBlockTimeHandler(blockTime time.Duration) *FixedBlockTimeHandler {
	return &FixedBlockTimeHandler{
		blockTime:      blockTime,
		curBlockOffset: 0,
	}
}

func (f *FixedBlockTimeHandler) GetBlockTime(lastBlockTimestamp time.Time) time.Time {
	f.mutex.Lock()
	defer f.mutex.Unlock()

	res := lastBlockTimestamp.Add(f.blockTime + f.curBlockOffset)
	f.curBlockOffset = 0
	f.lastBlockTimestamp = res
	return res
}

// FixedBlockTimeHandler.AdvanceTime will only return the correct next block time
// after GetBlockTime has been called once, but it will
// still advance the time correctly before that - only the output will be wrong.
func (f *FixedBlockTimeHandler) AdvanceTime(duration time.Duration) time.Time {
	f.mutex.Lock()
	defer f.mutex.Unlock()

	f.curBlockOffset += duration
	return f.lastBlockTimestamp.Add(f.blockTime + f.curBlockOffset)
}

var _ TimeHandler = (*FixedBlockTimeHandler)(nil)
