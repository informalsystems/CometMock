package main

import (
	"encoding/json"
	"fmt"
	"math/big"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func StartChain(
	t *testing.T,
	cometmockArgs string,
) error {
	// execute the local-testnet-singlechain.sh script
	t.Log("Running local-testnet-singlechain.sh")
	cmd := exec.Command("./local-testnet-singlechain-restart.sh", "simd")
	_, err := runCommandWithOutput(cmd)
	if err != nil {
		return fmt.Errorf("Error running local-testnet-singlechain.sh: %v", err)
	}

	cmd = exec.Command("./local-testnet-singlechain-start.sh", cometmockArgs)
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
	err := StartChain(t, "")
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
	err := StartChain(t, "")
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
	err := StartChain(t, "")
	if err != nil {
		t.Fatalf("Error starting chain: %v", err)
	}

	// check the current amount in the community pool
	communityPoolSize, err := getCommunityPoolSize()
	require.NoError(t, err)

	// send some tokens to the community pool
	err = sendToCommunityPool(50000000000, "coordinator")
	require.NoError(t, err)

	// check that the amount in the community pool has increased
	communityPoolSize2, err := getCommunityPoolSize()
	require.NoError(t, err)

	// cannot check for equality because the community pool gets dust over time
	require.True(t, communityPoolSize2.Cmp(communityPoolSize.Add(communityPoolSize, big.NewInt(50000000000))) == +1)
}

// TestBlockTime checks that the basic behaviour with a specified block-time is as expected,
// i.e. the time increases by the specified block time for each block.
func TestBlockTime(t *testing.T) {
	err := StartChain(t, "--block-time=5000")
	if err != nil {
		t.Fatalf("Error starting chain: %v", err)
	}

	// get a block with height+time
	blockString, err := QueryBlock()
	require.NoError(t, err)

	// get the height and time from the block
	height, err := GetHeightFromBlock(blockString)
	require.NoError(t, err)

	blockTime, err := GetTimeFromBlock(blockString)
	require.NoError(t, err)

	// wait for a couple of blocks to be produced
	time.Sleep(10 * time.Second)

	// get the new height and time
	blockString2, err := QueryBlock()
	require.NoError(t, err)

	height2, err := GetHeightFromBlock(blockString2)
	require.NoError(t, err)

	blockTime2, err := GetTimeFromBlock(blockString2)
	require.NoError(t, err)

	blockDifference := height2 - height
	// we expect that at least one block was produced, otherwise there is a problem
	require.True(t, blockDifference >= 1)

	// get the expected time diff between blocks, as block time was set to 5000 millis = 5 seconds
	expectedTimeDifference := time.Duration(blockDifference) * 5 * time.Second

	timeDifference := blockTime2.Sub(blockTime)

	require.Equal(t, expectedTimeDifference, timeDifference)
}

// TestAutoBlockProductionOff checks that the basic behaviour with
// block-production-interval is as expected, i.e. blocks only
// appear when it is manually instructed.
func TestNoAutoBlockProduction(t *testing.T) {
	err := StartChain(t, "--block-production-interval=-1 --block-time=0")
	if err != nil {
		t.Fatalf("Error starting chain: %v", err)
	}

	height, blockTime, err := GetHeightAndTime()
	require.NoError(t, err)

	// wait a few seconds to detect it blocks are produced automatically
	time.Sleep(10 * time.Second)

	// get the new height and time
	height2, blockTime2, err := GetHeightAndTime()
	require.NoError(t, err)

	// no blocks should have been produced
	require.Equal(t, height, height2)
	require.Equal(t, blockTime, blockTime2)

	// advance time by 5 seconds
	err = AdvanceTime(5 * time.Second)
	require.NoError(t, err)

	// get the height and time again, they should not have changed yet
	height3, blockTime3, err := GetHeightAndTime()
	require.NoError(t, err)

	require.Equal(t, height, height3)
	require.Equal(t, blockTime, blockTime3)

	// produce a block
	err = AdvanceBlocks(1)
	require.NoError(t, err)

	// get the height and time again, they should have changed
	height4, blockTime4, err := GetHeightAndTime()
	require.NoError(t, err)

	require.Equal(t, height+1, height4)
	require.Equal(t, blockTime.Add(5*time.Second), blockTime4)
}

// TestNoAutoTx checks that without auto-tx, transactions are not included
// in blocks automatically.
func TestNoAutoTx(t *testing.T) {
	err := StartChain(t, "--block-production-interval=-1 --auto-tx=false")
	if err != nil {
		t.Fatalf("Error starting chain: %v", err)
	}

	// produce a couple of blocks to initialize the community pool
	err = AdvanceBlocks(10)
	require.NoError(t, err)

	height, blockTime, err := GetHeightAndTime()
	require.NoError(t, err)

	communityPoolBefore, err := getCommunityPoolSize()
	require.NoError(t, err)

	// broadcast txs
	err = sendToCommunityPool(50000000000, "coordinator")
	require.NoError(t, err)
	err = sendToCommunityPool(50000000000, "bob")
	require.NoError(t, err)

	// get the new height and time
	height2, blockTime2, err := GetHeightAndTime()
	require.NoError(t, err)

	// no blocks should have been produced
	require.Equal(t, height, height2)
	require.Equal(t, blockTime, blockTime2)

	// produce a block
	err = AdvanceBlocks(1)
	require.NoError(t, err)

	// get the height and time again, they should have changed
	height3, blockTime3, err := GetHeightAndTime()
	require.NoError(t, err)

	require.Equal(t, height+1, height3)
	// exact time does not matter, just that it is after the previous block
	require.True(t, blockTime.Before(blockTime3))

	// check that the community pool was increased
	communityPoolAfter, err := getCommunityPoolSize()
	require.NoError(t, err)

	// cannot check for equality because the community pool gets dust over time
	require.True(t, communityPoolAfter.Cmp(communityPoolBefore.Add(communityPoolBefore, big.NewInt(100000000000))) == +1)
}
