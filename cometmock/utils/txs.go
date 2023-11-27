package utils

import (
	"bytes"

	cmttypes "github.com/cometbft/cometbft/types"
)

// Contains returns true if txs contains tx, false otherwise.
func Contains(txs cmttypes.Txs, tx cmttypes.Tx) bool {
	for _, ttx := range txs {
		if bytes.Equal([]byte(ttx), []byte(tx)) {
			return true
		}
	}
	return false
}
