package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// From the output of the AbciInfo command, extract the latest block height.
// The json bytes should look e.g. like this:
// {"jsonrpc":"2.0","id":1,"result":{"response":{"data":"interchain-security-p","last_block_height":"2566","last_block_app_hash":"R4Q3Si7+t7TIidl2oTHcQRDNEz+lP0IDWhU5OI89psg="}}}
func extractHeightFromInfo(jsonBytes []byte) (int, error) {
	// Use a generic map to represent the JSON structure
	var data map[string]interface{}

	if err := json.Unmarshal(jsonBytes, &data); err != nil {
		return -1, fmt.Errorf("failed to unmarshal JSON %s \n error was %v", string(jsonBytes), err)
	}

	// Navigate the map and use type assertions to get the last_block_height
	result, ok := data["result"].(map[string]interface{})
	if !ok {
		return -1, fmt.Errorf("failed to navigate abci_info output structure trying to access result: json was %s", string(jsonBytes))
	}

	response, ok := result["response"].(map[string]interface{})
	if !ok {
		return -1, fmt.Errorf("failed to navigate abci_info output structure trying to access response: json was %s", string(jsonBytes))
	}

	lastBlockHeight, ok := response["last_block_height"].(string)
	if !ok {
		return -1, fmt.Errorf("failed to navigate abci_info output structure trying to access last_block_height: json was %s", string(jsonBytes))
	}

	return strconv.Atoi(lastBlockHeight)
}

// Queries simd for the latest block.
func QueryBlock() (string, error) {
	// execute the query command
	cmd := exec.Command("bash", "-c", "simd q block --node tcp://127.0.0.1:22331")
	out, err := runCommandWithOutput(cmd)
	if err != nil {
		return "", fmt.Errorf("error running query command: %v", err)
	}

	return out, nil
}

type BlockInfo struct {
	Block struct {
		Header struct {
			Height string `json:"height"`
			Time   string `json:"time"`
		} `json:"header"`
	} `json:"block"`
}

func GetHeightFromBlock(blockString string) (int, error) {
	var block BlockInfo
	err := json.Unmarshal([]byte(blockString), &block)
	if err != nil {
		return 0, err
	}

	res, err := strconv.Atoi(block.Block.Header.Height)
	if err != nil {
		return 0, err
	}

	return res, nil
}

func GetTimeFromBlock(blockBytes string) (time.Time, error) {
	var block BlockInfo
	err := json.Unmarshal([]byte(blockBytes), &block)
	if err != nil {
		return time.Time{}, err
	}

	res, err := time.Parse(time.RFC3339, block.Block.Header.Time)
	if err != nil {
		return time.Time{}, err
	}

	return res, nil
}

func GetHeightAndTime() (int, time.Time, error) {
	blockBytes, err := QueryBlock()
	if err != nil {
		return 0, time.Time{}, err
	}

	height, err := GetHeightFromBlock(blockBytes)
	if err != nil {
		return 0, time.Time{}, err
	}

	timestamp, err := GetTimeFromBlock(blockBytes)
	if err != nil {
		return 0, time.Time{}, err
	}

	return height, timestamp, nil
}

// Queries the size of the community pool.
// For this, it will just check the number of tokens of the first denom in the community pool.
func getCommunityPoolSize() (int64, error) {
	// execute the query command
	cmd := exec.Command("bash", "-c", "simd q distribution community-pool --output json --node tcp://127.0.0.1:22331 | jq -r '.pool[0].amount'")
	out, err := runCommandWithOutput(cmd)
	if err != nil {
		return -1, fmt.Errorf("error running query command: %v", err)
	}

	res, err := strconv.ParseFloat(strings.TrimSpace(out), 64)
	if err != nil {
		return -1, fmt.Errorf("error parsing community pool size: %v, input was %v", err, strings.TrimSpace(out))
	}

	return int64(res), nil
}

func sendToCommunityPool(amount int, sender string) error {
	// execute the tx command
	stringCmd := fmt.Sprintf("simd tx distribution fund-community-pool %vstake --chain-id provider --from %v-key --keyring-backend test --node tcp://127.0.0.1:22331 --home ~/nodes/provider/provider-%v -y", amount, sender, sender)
	cmd := exec.Command("bash", "-c", stringCmd)
	_, err := runCommandWithOutput(cmd)
	return err
}

func runCommandWithOutput(cmd *exec.Cmd) (string, error) {
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("error running command: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}

	return stdout.String(), nil
}

func AdvanceTime(duration time.Duration) error {
	stringCmd := fmt.Sprintf("curl -H 'Content-Type: application/json' -H 'Accept:application/json' --data '{\"jsonrpc\":\"2.0\",\"method\":\"advance_time\",\"params\":{\"duration_in_seconds\": \"%v\"},\"id\":1}' 127.0.0.1:22331", duration.Seconds())

	cmd := exec.Command("bash", "-c", stringCmd)
	_, err := runCommandWithOutput(cmd)
	return err
}

func AdvanceBlocks(numBlocks int) error {
	stringCmd := fmt.Sprintf("curl -H 'Content-Type: application/json' -H 'Accept:application/json' --data '{\"jsonrpc\":\"2.0\",\"method\":\"advance_blocks\",\"params\":{\"num_blocks\": \"%v\"},\"id\":1}' 127.0.0.1:22331", numBlocks)

	cmd := exec.Command("bash", "-c", stringCmd)
	_, err := runCommandWithOutput(cmd)
	return err
}
