# CometMock

To run CometMock:
`docker stop simapp; docker rm simapp; docker run --add-host=host.docker.internal:host-gateway --name simapp -p 26658:26658 -ti informalofftermatt/testnet:tendermock simd start --transport=grpc --with-tendermint=false --grpc-only --rpc.laddr=tcp://host.docker.internal:99999`
`docker stop simapp2; docker rm simapp2; docker run --add-host=host.docker.internal:host-gateway --name simapp2 -p 36658:26658 -ti informalofftermatt/testnet:tendermock simd start --transport=grpc --with-tendermint=false --grpc-only --rpc.laddr=tcp://host.docker.internal:99999`
`go run ./cometmock localhost:36658,localhost:26658 genesis.json tcp://localhost:26657`