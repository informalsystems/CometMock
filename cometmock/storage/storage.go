package storage

import (
	"fmt"
	"sync"

	abcitypes "github.com/cometbft/cometbft/abci/types"
	cometstate "github.com/cometbft/cometbft/state"
	"github.com/cometbft/cometbft/types"
)

// Storage is an interface for storing blocks, commits and states by height.
// All methods are thread-safe.
type Storage interface {
	// GetBlock returns the block at a given height.
	GetBlock(height int64) (*types.Block, error)

	// GetCommit returns the commit at a given height.
	GetCommit(height int64) (*types.Commit, error)

	// GetState returns the state at a given height. This is the state after
	// applying the block at that height.
	GetState(height int64) (*cometstate.State, error)

	// GetResponses returns the ABCI responses from a given height.
	GetResponses(height int64) (*abcitypes.ResponseFinalizeBlock, error)

	// LockBeforeStateUpdate locks the storage for state update.
	LockBeforeStateUpdate()

	// UnlockAfterStateUpdate unlocks the storage for state update.
	UnlockAfterStateUpdate()

	// UpdateStores updates the storage with the given block, commit, state and responses.
	// It is assumed that the block, commit, state and responses are all from the same height.
	// If they are not, the storage will be in an inconsistent state.
	// If the storage is already updated with the given height, the storage will overwrite the existing data.
	// This method is *not* thread-safe.
	// Before calling this, the caller should call LockForStateUpdate().
	// After calling this, the caller should call UnlockForStateUpdate().
	UpdateStores(
		height int64,
		block *types.Block,
		commit *types.Commit,
		state *cometstate.State,
		responses *abcitypes.ResponseFinalizeBlock,
	) error
}

// MapStorage is a simple in-memory implementation of Storage.
type MapStorage struct {
	// a mutex that gets locked while the state is being updated,
	// so that a) updates do not interleave and b) reads do not happen while
	// the state is being updated, i.e. two stores might give bogus data.
	stateUpdateMutex sync.RWMutex
	blocks           map[int64]*types.Block
	commits          map[int64]*types.Commit
	states           map[int64]*cometstate.State
	responses        map[int64]*abcitypes.ResponseFinalizeBlock
}

// ensure MapStorage implements Storage
var _ Storage = (*MapStorage)(nil)

func (m *MapStorage) insertBlock(height int64, block *types.Block) error {
	if m.blocks == nil {
		m.blocks = make(map[int64]*types.Block)
	}
	m.blocks[height] = block
	return nil
}

func (m *MapStorage) GetBlock(height int64) (*types.Block, error) {
	m.stateUpdateMutex.RLock()
	defer m.stateUpdateMutex.RUnlock()
	if m.blocks == nil {
		m.blocks = make(map[int64]*types.Block)
	}
	if block, ok := m.blocks[height]; ok {
		return block, nil
	}
	return nil, fmt.Errorf("block for height %v not found", height)
}

func (m *MapStorage) insertCommit(height int64, commit *types.Commit) error {
	if m.commits == nil {
		m.commits = make(map[int64]*types.Commit)
	}

	m.commits[height] = commit
	return nil
}

func (m *MapStorage) GetCommit(height int64) (*types.Commit, error) {
	m.stateUpdateMutex.RLock()
	defer m.stateUpdateMutex.RUnlock()
	if m.commits == nil {
		m.commits = make(map[int64]*types.Commit)
	}

	if commit, ok := m.commits[height]; ok {
		return commit, nil
	}
	return nil, fmt.Errorf("commit for height %v not found", height)
}

func (m *MapStorage) insertState(height int64, state *cometstate.State) error {
	if m.states == nil {
		m.states = make(map[int64]*cometstate.State)
	}

	m.states[height] = state
	return nil
}

func (m *MapStorage) GetState(height int64) (*cometstate.State, error) {
	m.stateUpdateMutex.RLock()
	defer m.stateUpdateMutex.RUnlock()
	if m.states == nil {
		m.states = make(map[int64]*cometstate.State)
	}

	if state, ok := m.states[height]; ok {
		return state, nil
	}
	return nil, fmt.Errorf("state for height %v not found", height)
}

func (m *MapStorage) insertResponses(
	height int64,
	responses *abcitypes.ResponseFinalizeBlock,
) error {
	if m.responses == nil {
		m.responses = make(map[int64]*abcitypes.ResponseFinalizeBlock)
	}

	m.responses[height] = responses
	return nil
}

func (m *MapStorage) GetResponses(height int64) (*abcitypes.ResponseFinalizeBlock, error) {
	m.stateUpdateMutex.RLock()
	defer m.stateUpdateMutex.RUnlock()
	if m.responses == nil {
		m.responses = make(map[int64]*abcitypes.ResponseFinalizeBlock)
	}

	if responses, ok := m.responses[height]; ok {
		return responses, nil
	}
	return nil, fmt.Errorf("responses for height %v not found", height)
}

func (m *MapStorage) LockBeforeStateUpdate() {
	m.stateUpdateMutex.Lock()
}

func (m *MapStorage) UnlockAfterStateUpdate() {
	m.stateUpdateMutex.Unlock()
}

func (m *MapStorage) UpdateStores(height int64, block *types.Block, commit *types.Commit, state *cometstate.State, responses *abcitypes.ResponseFinalizeBlock) error {
	m.insertBlock(height, block)
	m.insertCommit(height, commit)
	m.insertState(height, state)
	m.insertResponses(height, responses)
	return nil
}
