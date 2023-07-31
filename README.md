# CometMock

CometMock is a mock implementation of CometBFT.
It is meant to be used as a drop-in replacement for CometBFT in end-to-end tests.
Some of the reasons to use CometMock instead of CometBFT are:
* More reliable and faster block times: CometBFT runs a consensus algorithm, which involves many network communications, and non-determinism in the network layer can lead to varying block times that can make tests flaky.
CometMock instead directly communicates with applications via ABCI, mimicking the behaviour of many CometBFT instances coming to consensus, but with much fewer network communications.
* More control: When transactions are broadcasted, CometMock immediately includes them in the next block. CometMock also allows
    * causing downtime without the need to bother with the network or killing processes by
controlling which validators sign blocks,
    * fast-forwarding time, letting arbitrary time pass to the view of the application, without needing to actually wait,
    * fast-forwarding blocks, creating empty blocks rapidly to wait for events on the chain to happen.

On a technical level, CometMock communicates with applications via ABCI through GRPC or TSP (Tendermint Socket Protocol) calls. It calls BeginBlock, DeliverTx, EndBlock and Commit like CometBFT does during normal execution.

<<<<<<< HEAD
<<<<<<< HEAD
Currently, CometMock maintains releases compatible with CometBFT v0.37 and v0.34, see branches [v0.34.x](https://github.com/informalsystems/CometMock/tree/v0.34.x) and [v0.37.x](https://github.com/informalsystems/CometMock/tree/v0.37.x). It offers *many* of the RPC endpoints offered by Comet (see https://docs.cometbft.com/v0.34/rpc/ and https://docs.cometbft.com/v0.37/rpc/ for the respective version of the interface),
=======
Currently, CometMock maintains releases compatible with CometBFT v0.37 and v0.34. It offers *many* of the RPC endpoints offered by Comet (see https://docs.cometbft.com/v0.34/rpc/ and https://docs.cometbft.com/v0.37/rpc/ for the respective version of the interface),
>>>>>>> 3878fc0 (Update README.md)
=======
Currently, CometMock maintains releases compatible with CometBFT v0.37 and v0.34, see branches [v0.34.x](https://github.com/informalsystems/CometMock/tree/v0.34.x) and [v0.37.x](https://github.com/informalsystems/CometMock/tree/v0.37.x). It offers *many* of the RPC endpoints offered by Comet (see https://docs.cometbft.com/v0.34/rpc/ and https://docs.cometbft.com/v0.37/rpc/ for the respective version of the interface),
>>>>>>> 114afad (Add link to branches in README)
in particular it supports the subset used by Gorelayer (https://github.com/cosmos/relayer/).
See the endpoints offered here: [https://github.com/informalsystems/CometMock/cometmock/rpc_server/routes.go#L30C2-L53](https://github.com/informalsystems/CometMock/blob/main/cometmock/rpc_server/routes.go)

## Installation

Run `go install ./cometmock`, then you can run `cometmock` to see usage information.
CometMock was tested with `go version go1.20.3 darwin/arm64`.

## How to use

To run CometMock, start your (cosmos-sdk) application instances with the flags ```--with-tendermint=false, --transport=grpc```.
After the applications started, start CometMock like this
```
cometmock {app_address1,app_address2,...} {genesis_file} {cometmock_listen_address} {home_folder1,home_folder2,...} {connection_mode}
```

where 
* the `app_addresses` are the `--address` flags of the applications (this is by default `"tcp://0.0.0.0:26658"`
* the `genesis_file` is the genesis json that is also used by apps,
* the `cometmock_listen_address` can be freely chosen and will be the address that requests that would normally go to CometBFT rpc endpoints need to be directed to
* the `home_folders` are the home folders of the applications, in the same order as the `app_addresses`. This is required to use the private keys in the application folders to sign as appropriate validators.
* connection mode is the protocol over which CometMock should connect to the ABCI application, either `grpc` or `socket`. See the `--transport` flag for Cosmos SDK applications. For SDK applications, just make sure `--transport` and this argument match, i.e. either both `socket` or both `grpc`.

When calling the cosmos sdk cli, use as node address the `cometmock_listen_address`,
e.g. `simd q bank total --node {cometmock_listen_address}`.

### CometMock specific RPC endpoints

Here is a quick explanation and example usage of each of the endpoints that are custom to CometMock

* `advance_blocks(num_blocks)`: Runs `num_blocks` empty blocks in succession. This is way faster than waiting for blocks, e.g. roughly advancing hundreds of blocks takes a few seconds.
Be aware that this still scales linearly in the number of blocks advanced, so e.g. advancing a million blocks will still take a while.
Example usage:
```
curl -H 'Content-Type: application/json' -H 'Accept:application/json' --data '{"jsonrpc":"2.0","method":"advance_blocks","params":{"num_blocks": "20"},"id":1}' 127.0.0.1:22331
```
* `set_signing_status(private_key_address,status)`: Status can be either `up` (to make the validator sign blocks) or `down` (to make the validator stop signing blocks).
The `private_key_address` is the `address` field of the validators private key. You can find this under `your_node_home/config/priv_validator_key.json`.
That file looks like this: ```{
  "address": "201A6CD9B0CCB5A467F1E13589C92D9C6A76D3E0",
  "pub_key": {
    "type": "tendermint/PubKeyEd25519",
    "value": "946RMFmXUavi+lEypuCu9Ul2ecs+RMKBVhRR9D3FvCo="
  },
  "priv_key": {
    "type": "tendermint/PrivKeyEd25519",
    "value": "OUHGIoJ1uxVKwDLSwOF+GDbLx9ePgiaGwcy0e5roC2L3jpEwWZdRq+L6UTKm4K71SXZ5yz5EwoFWFFH0PcW8Kg=="
  }
}```
Here, the `address` field is what should be given to the command.
Example usage:
```
# Stop the validator from signing
curl -H 'Content-Type: application/json' -H 'Accept:application/json' --data '{"jsonrpc":"2.0","method":"set_signing_status","params":{"private_key_address": "'"$PRIV_VALIDATOR_ADDRESS"'", "status": "down"},"id":1}' 127.0.0.1:22331

# Advance enough blocks to get the valdator downtime-slashed
curl -H 'Content-Type: application/json' -H 'Accept:application/json' --data '{"jsonrpc":"2.0","method":"advance_blocks","params":{"num_blocks": "20"},"id":1}' 127.0.0.1:22331

# Make the validator sign again
curl -H 'Content-Type: application/json' -H 'Accept:application/json' --data '{"jsonrpc":"2.0","method":"set_signing_status","params":{"private_key_address": "'"$PRIV_VALIDATOR_ADDRESS"'", "status": "up"},"id":1}' 127.0.0.1:22331
```

* `advance_time(duration_in_seconds)`: Advances the local time of the blockchain by `duration_in_seconds` seconds. Under the hood, this is done by giving the application timestamps offset by the sum of time advancements that happened so far.
When you test with multiple chains, be aware that you should advance chains at the same time, otherwise e.g. IBC will break due to large differences in the times of the different chains.
This is constant time no matter the duration you advance by.
Example usage:
```
curl -H 'Content-Type: application/json' -H 'Accept:application/json' --data '{"jsonrpc":"2.0","method":"advance_time","params":{"duration_in_seconds": "36000000"},"id":1}' 127.0.0.1:22331
```

## Limitations

### Not all CometBFT RPC endpoints are implemented
Out of a desire to avoid unnecessary bloat, not all CometBFT RPC endpoints from https://docs.cometbft.com/v0.34/rpc/ are implemented.
If you want to use CometMock but an RPC endpoint you rely on isn't present, please create an issue.

### Cosmos SDK GRPC endpoints are not working
Cosmos SDK applications started with `--with-tendermint=false`
do not start their grpc server, see https://github.com/cosmos/cosmos-sdk/issues/16277.
This is a limitation of the Cosmos SDK related to using out-of-process consensus.

### --gas auto is not working
Related, using `--gas auto` calls a cosmos sdk grpc endpoint, so it won't be possible with CometMock.
It is recommended to manually specify a large enough gas amount.

### Hermes does not work with CometMock
In particular, the fact that the cosmos sdk grpc endpoints are incompatible with having
out-of-process consensus prevents CometMock from working with Hermes, since Hermes calls the SDK grpc endpoints.
If you need a relayer with CometMock, the go relayer https://github.com/cosmos/relayer 
is an alternative. The only caveat is that it typically calls the gas simulation, which doesn't work with CometMock.
Here is a fork of the gorelayer that removes the gas simulation in favor of a fixed value https://github.com/p-offtermatt/relayer/tree/v2.3.0-no-gas-sim.
see this commit for the changes https://github.com/p-offtermatt/relayer/commit/39bc4b82acf1f95b9a8d40a281c3f90178d72d00


## Disclaimer

CometMock is under heavy development and work-in-progress.
Use at your own risk. In the current state, testing with CometMock cannot fully replace proper end-to-end tests
with CometBFT.

## License Information

Copyright © 2023 Informal Systems Inc. and CometMock authors.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use the files in this repository except in compliance with the License. You may obtain a copy of the License at

```https://www.apache.org/licenses/LICENSE-2.0```

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific language governing permissions and limitations under the License.
