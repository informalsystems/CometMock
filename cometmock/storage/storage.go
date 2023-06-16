package storage

import (
	"fmt"
	"sync"

	protostate "github.com/cometbft/cometbft/proto/tendermint/state"
	cometstate "github.com/cometbft/cometbft/state"
	"github.com/cometbft/cometbft/types"
)

// Storage is an interface for storing blocks, commits and states by height.
// All methods are thread-safe.
type Storage interface {
	// InsertBlock inserts a block at a given height.
	// If there is already a block at that height, it should be overwritten.
	InsertBlock(height int64, block *types.Block) error
	// GetBlock returns the block at a given height.
	GetBlock(height int64) (*types.Block, error)

	// InsertCommit inserts a commit at a given height.
	// If there is already a commit at that height, it should be overwritten.
	InsertCommit(height int64, commit *types.Commit) error
	// GetCommit returns the commit at a given height.
	GetCommit(height int64) (*types.Commit, error)

	// InsertState inserts a state at a given height. This is the state after
	// applying the block at that height.
	// If there is already a state at that height, it should be overwritten.
	InsertState(height int64, state *cometstate.State) error
	// GetState returns the state at a given height. This is the state after
	// applying the block at that height.
	GetState(height int64) (*cometstate.State, error)

	// InsertResponses inserts the ABCI responses from a given height.
	// If there are already responses at that height, they should be overwritten.
	InsertResponses(height int64, responses *protostate.ABCIResponses) error
	// GetResponses returns the ABCI responses from a given height.
	GetResponses(height int64) (*protostate.ABCIResponses, error)
}

// MapStorage is a simple in-memory implementation of Storage.
type MapStorage struct {
	blocks         map[int64]*types.Block
	blocksMutex    sync.RWMutex
	commits        map[int64]*types.Commit
	commitMutex    sync.RWMutex
	states         map[int64]*cometstate.State
	statesMutex    sync.RWMutex
	responses      map[int64]*protostate.ABCIResponses
	responsesMutex sync.RWMutex
}

// ensure MapStorage implements Storage
var _ Storage = (*MapStorage)(nil)

func (m *MapStorage) InsertBlock(height int64, block *types.Block) error {
	m.blocksMutex.Lock()
	defer m.blocksMutex.Unlock()
	if m.blocks == nil {
		m.blocks = make(map[int64]*types.Block)
	}
	m.blocks[height] = block
	return nil
}

func (m *MapStorage) GetBlock(height int64) (*types.Block, error) {
	m.blocksMutex.RLock()
	defer m.blocksMutex.RUnlock()

	if m.blocks == nil {
		m.blocks = make(map[int64]*types.Block)
	}
	if block, ok := m.blocks[height]; ok {
		return block, nil
	}
	return nil, fmt.Errorf("block for height %v not found", height)
}

func (m *MapStorage) InsertCommit(height int64, commit *types.Commit) error {
	m.commitMutex.Lock()
	defer m.commitMutex.Unlock()

	if m.commits == nil {
		m.commits = make(map[int64]*types.Commit)
	}

	m.commits[height] = commit
	return nil
}

func (m *MapStorage) GetCommit(height int64) (*types.Commit, error) {
	m.commitMutex.RLock()
	defer m.commitMutex.RUnlock()

	if m.commits == nil {
		m.commits = make(map[int64]*types.Commit)
	}

	if commit, ok := m.commits[height]; ok {
		return commit, nil
	}
	return nil, fmt.Errorf("commit for height %v not found", height)
}

func (m *MapStorage) InsertState(height int64, state *cometstate.State) error {
	m.statesMutex.Lock()
	defer m.statesMutex.Unlock()

	if m.states == nil {
		m.states = make(map[int64]*cometstate.State)
	}

	m.states[height] = state
	return nil
}

func (m *MapStorage) GetState(height int64) (*cometstate.State, error) {
	m.statesMutex.RLock()
	defer m.statesMutex.RUnlock()

	if m.states == nil {
		m.states = make(map[int64]*cometstate.State)
	}

	if state, ok := m.states[height]; ok {
		return state, nil
	}
	return nil, fmt.Errorf("state for height %v not found", height)
}

func (m *MapStorage) InsertResponses(height int64, responses *protostate.ABCIResponses) error {
	m.responsesMutex.Lock()
	defer m.responsesMutex.Unlock()

	if m.responses == nil {
		m.responses = make(map[int64]*protostate.ABCIResponses)
	}

	m.responses[height] = responses
	return nil
}

func (m *MapStorage) GetResponses(height int64) (*protostate.ABCIResponses, error) {
	m.responsesMutex.RLock()
	defer m.responsesMutex.RUnlock()

	if m.responses == nil {
		m.responses = make(map[int64]*protostate.ABCIResponses)
	}

	if responses, ok := m.responses[height]; ok {
		return responses, nil
	}
	return nil, fmt.Errorf("responses for height %v not found", height)
}
