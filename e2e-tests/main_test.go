package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

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

// From the output of the AbciInfo command, extract the latest block height.
// The json bytes should look e.g. like this:
// {"jsonrpc":"2.0","id":1,"result":{"response":{"data":"interchain-security-p","last_block_height":"2566","last_block_app_hash":"R4Q3Si7+t7TIidl2oTHcQRDNEz+lP0IDWhU5OI89psg="}}}
func extractHeightFromInfo(jsonBytes []byte) (int, error) {
	// Use a generic map to represent the JSON structure
	var data map[string]interface{}

	if err := json.Unmarshal(jsonBytes, &data); err != nil {
		return -1, fmt.Errorf("Failed to unmarshal JSON %s \n error was %v", string(jsonBytes), err)
	}

	// Navigate the map and use type assertions to get the last_block_height
	result, ok := data["result"].(map[string]interface{})
	if !ok {
		return -1, fmt.Errorf("Failed to navigate abci_info output structure trying to access result: json was %s", string(jsonBytes))
	}

	response, ok := result["response"].(map[string]interface{})
	if !ok {
		return -1, fmt.Errorf("Failed to navigate abci_info output structure trying to access response: json was %s", string(jsonBytes))
	}

	lastBlockHeight, ok := response["last_block_height"].(string)
	if !ok {
		return -1, fmt.Errorf("Failed to navigate abci_info output structure trying to access last_block_height: json was %s", string(jsonBytes))
	}

	return strconv.Atoi(lastBlockHeight)
}

// Queries the size of the community pool.
// For this, it will just check the number of tokens of the first denom in the community pool.
func getCommunityPoolSize() (*big.Int, error) {
	// execute the query command
	cmd := exec.Command("bash", "-c", "simd q distribution community-pool --output json --node tcp://127.0.0.1:22331 | jq -r '.pool[0].amount'")
	out, err := runCommandWithOutput(cmd)
	if err != nil {
		return big.NewInt(-1), fmt.Errorf("Error running query command: %v", err)
	}

	res := new(big.Int)

	res, ok := res.SetString(strings.TrimSpace(out), 10)
	if !ok {
		return big.NewInt(-1), fmt.Errorf("Error parsing community pool size: %v", err)
	}
	return res, err
}

func sendToCommunityPool(amount int) error {
	// execute the tx command
	stringCmd := fmt.Sprintf("simd tx distribution fund-community-pool %vstake --chain-id provider --from coordinator-key --keyring-backend test --node tcp://127.0.0.1:22331 --home ~/nodes/provider/provider-coordinator -y", amount)
	cmd := exec.Command("bash", "-c", stringCmd)
	_, err := runCommandWithOutput(cmd)
	return err
}

func StartChain(
	t *testing.T,
	cometmockArgs ...string,
) error {
	// execute the local-testnet-singlechain.sh script
	t.Log("Running local-testnet-singlechain.sh")
	cmd := exec.Command("./local-testnet-singlechain-restart.sh", "simd", "")
	_, err := runCommandWithOutput(cmd)
	if err != nil {
		return fmt.Errorf("Error running local-testnet-singlechain.sh: %v", err)
	}

	cmd = exec.Command("./local-testnet-singlechain-start.sh", "simd", "")
	_, err = runCommandWithOutput(cmd)
	if err != nil {
		return fmt.Errorf("Error running local-testnet-singlechain.sh: %v", err)
	}

	t.Log("Done starting testnet")

	// wait until we are producing blocks
	for {
		// --type height 0 gets the latest height
		out, err := exec.Command("bash", "-c", "simd q block --type height 0 --output json --node tcp://127.0.0.1:22331 | jq -r '.header.height'").Output()

		if err == nil {
			t.Log("We are producing blocks: ", string(out))
			break
		}
		t.Log("Waiting for blocks to be produced, latest output: ", string(out))
		time.Sleep(1 * time.Second)
	}
	time.Sleep(5 * time.Second)
	return nil
}

// Tests happy path functionality for Abci Info.
func TestAbciInfo(t *testing.T) {
	// start the chain
	err := StartChain(t)
	if err != nil {
		t.Fatalf("Error starting chain: %v", err)
	}

	// call the abci_info command by calling curl on the REST endpoint
	// curl -H 'Content-Type: application/json' -H 'Accept:application/json' --data '{"jsonrpc":"2.0","method":"abci_info","id":1}' 127.0.0.1:22331
	args := []string{"bash", "-c", "curl -H 'Content-Type: application/json' -H 'Accept:application/json' --data '{\"jsonrpc\":\"2.0\",\"method\":\"abci_info\",\"id\":1}' 127.0.0.1:22331"}
	cmd := exec.Command(args[0], args[1:]...)
	out, err := runCommandWithOutput(cmd)
	if err != nil {
		t.Fatalf("Error running curl\ncommand: %v\noutput: %v\nerror: %v", cmd, string(out), err)
	}

	// extract the latest block height from the output
	height, err := extractHeightFromInfo([]byte(out))
	if err != nil {
		t.Fatalf("Error extracting block height from abci_info output: %v", err)
	}

	// wait a bit to make sure the block height has increased
	time.Sleep(2 * time.Second)

	// call the abci_info command again
	cmd2 := exec.Command(args[0], args[1:]...)
	out2, err := runCommandWithOutput(cmd2)
	if err != nil {
		t.Fatalf("Error running curl\ncommand: %v\noutput: %v\nerror: %v", cmd2, string(out2), err)
	}

	// extract the latest block height from the output
	height2, err := extractHeightFromInfo([]byte(out2))
	if err != nil {
		t.Fatalf("Error extracting block height from abci_info output: %v", err)
	}

	// check that the block height has increased
	if height2 <= height {
		t.Fatalf("Expected block height to increase, but it did not. First height was %v, second height was %v", height, height2)
	}
}

func TestAbciQuery(t *testing.T) {
	// start the chain
	err := StartChain(t)
	if err != nil {
		t.Fatalf("Error starting chain: %v", err)
	}

	// call the abci_query command by submitting a query that hits the AbciQuery endpoint
	// for simplicity, we query for the staking params here - any query would work,
	// but ones without arguments are easier to construct
	args := []string{"bash", "-c", "simd q staking params --node tcp://127.0.0.1:22331 --output json"}
	cmd := exec.Command(args[0], args[1:]...)
	out, err := runCommandWithOutput(cmd)
	if err != nil {
		t.Fatalf("Error running command: %v\noutput: %v\nerror: %v", cmd, string(out), err)
	}

	// check that the output is valid JSON
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(out), &data); err != nil {
		t.Fatalf("Failed to unmarshal JSON %s \n error was %v", string(out), err)
	}

	// check that the output contains the expected params field. its contents are not important
	_, ok := data["params"]
	if !ok {
		t.Fatalf("Expected output to contain params field, but it did not. Output was %s", string(out))
	}
}

func TestTx(t *testing.T) {
	err := StartChain(t)
	if err != nil {
		t.Fatalf("Error starting chain: %v", err)
	}

	// check the current amount in the community pool
	communityPoolSize, err := getCommunityPoolSize()
	require.NoError(t, err)

	// send some tokens to the community pool
	err = sendToCommunityPool(50000000000)
	require.NoError(t, err)

	// check that the amount in the community pool has increased
	communityPoolSize2, err := getCommunityPoolSize()
	require.NoError(t, err)

	// cannot check for equality because the community pool gets dust over time
	require.True(t, communityPoolSize2.Cmp(communityPoolSize.Add(communityPoolSize, big.NewInt(50000000000))) == +1)
}

func TestTxAutoIncludeOff(t *testing.T) {
	err := StartChain(t)
	if err != nil {
		t.Fatalf("Error starting chain: %v", err)
	}
}
