#!/bin/bash
## This script sets up the environment to run the single chain local testnet.
## Importantly, it does not actually start nodes (or cometmock) - instead,
## it will produce two scripts, start_apps.sh and start_cometmock.sh.
## After this script is done setting up, simply run these two scripts to run the
## testnet. 
## The reason for this is that we want to be able to make the testnet setup
## differentiated from the actual run to allow for better caching in Docker.

set -eux 

BINARY_NAME=$1

COMETMOCK_ARGS=$2

# User balance of stake tokens 
USER_COINS="100000000000stake"
# Amount of stake tokens staked
STAKE="100000000stake"
# Node IP address
NODE_IP="127.0.0.1"

# Home directory
HOME_DIR=$HOME

# Validator moniker
MONIKERS=("coordinator" "alice" "bob")
LEAD_VALIDATOR_MONIKER="coordinator"

PROV_NODES_ROOT_DIR=${HOME_DIR}/nodes/provider
CONS_NODES_ROOT_DIR=${HOME_DIR}/nodes/consumer

# Base port. Ports assigned after these ports sequentially by nodes.
RPC_LADDR_BASEPORT=29170
P2P_LADDR_BASEPORT=29180
GRPC_LADDR_BASEPORT=29190
NODE_ADDRESS_BASEPORT=29200
PPROF_LADDR_BASEPORT=29210
CLIENT_BASEPORT=29220

# keeps a comma separated list of node addresses for provider and consumer
PROVIDER_NODE_LISTEN_ADDR_STR=""
CONSUMER_NODE_LISTEN_ADDR_STR=""

# Strings that keep the homes of provider nodes and homes of consumer nodes
PROV_NODES_HOME_STR=""
CONS_NODES_HOME_STR=""

PROVIDER_COMETMOCK_ADDR=tcp://$NODE_IP:22331
CONSUMER_COMETMOCK_ADDR=tcp://$NODE_IP:22332

# Clean start
pkill -f ^$BINARY_NAME &> /dev/null || true
pkill -f ^cometmock &> /dev/null || true
sleep 1
rm -rf ${PROV_NODES_ROOT_DIR}
rm -rf ${CONS_NODES_ROOT_DIR}

# Let lead validator create genesis file
LEAD_VALIDATOR_PROV_DIR=${PROV_NODES_ROOT_DIR}/provider-${LEAD_VALIDATOR_MONIKER}
LEAD_VALIDATOR_CONS_DIR=${CONS_NODES_ROOT_DIR}/consumer-${LEAD_VALIDATOR_MONIKER}
LEAD_PROV_KEY=${LEAD_VALIDATOR_MONIKER}-key
LEAD_PROV_LISTEN_ADDR=tcp://${NODE_IP}:${RPC_LADDR_BASEPORT}

for index in "${!MONIKERS[@]}"
do
    MONIKER=${MONIKERS[$index]}
    # validator key
    PROV_KEY=${MONIKER}-key

    # home directory of this validator on provider
    PROV_NODE_DIR=${PROV_NODES_ROOT_DIR}/provider-${MONIKER}

    # home directory of this validator on consumer
    CONS_NODE_DIR=${CONS_NODES_ROOT_DIR}/consumer-${MONIKER}

    # Build genesis file and node directory structure
    $BINARY_NAME init $MONIKER --chain-id provider --home ${PROV_NODE_DIR}
    jq ".app_state.gov.params.voting_period = \"100000s\" | .app_state.staking.params.unbonding_time = \"86400s\" | .app_state.slashing.params.signed_blocks_window=\"1000\" " \
    ${PROV_NODE_DIR}/config/genesis.json > \
    ${PROV_NODE_DIR}/edited_genesis.json && mv ${PROV_NODE_DIR}/edited_genesis.json ${PROV_NODE_DIR}/config/genesis.json


    sleep 1

    # Create account keypair
    $BINARY_NAME keys add $PROV_KEY --home ${PROV_NODE_DIR} --keyring-backend test --output json > ${PROV_NODE_DIR}/${PROV_KEY}.json 2>&1
    sleep 1

    # copy genesis in, unless this validator is the lead validator
    if [ $MONIKER != $LEAD_VALIDATOR_MONIKER ]; then
        cp ${LEAD_VALIDATOR_PROV_DIR}/config/genesis.json ${PROV_NODE_DIR}/config/genesis.json
    fi

    # Add stake to user
    PROV_ACCOUNT_ADDR=$(jq -r '.address' ${PROV_NODE_DIR}/${PROV_KEY}.json)
    $BINARY_NAME genesis add-genesis-account $PROV_ACCOUNT_ADDR $USER_COINS --home ${PROV_NODE_DIR} --keyring-backend test
    sleep 1

    # copy genesis out, unless this validator is the lead validator
    if [ $MONIKER != $LEAD_VALIDATOR_MONIKER ]; then
        cp ${PROV_NODE_DIR}/config/genesis.json ${LEAD_VALIDATOR_PROV_DIR}/config/genesis.json
    fi

    PPROF_LADDR=${NODE_IP}:$(($PPROF_LADDR_BASEPORT + $index))
    P2P_LADDR_PORT=$(($P2P_LADDR_BASEPORT + $index))

    # adjust configs of this node
    sed -i -r 's/timeout_commit = "5s"/timeout_commit = "3s"/g' ${PROV_NODE_DIR}/config/config.toml
    sed -i -r 's/timeout_propose = "3s"/timeout_propose = "1s"/g' ${PROV_NODE_DIR}/config/config.toml

    # make address book non-strict. necessary for this setup
    sed -i -r 's/addr_book_strict = true/addr_book_strict = false/g' ${PROV_NODE_DIR}/config/config.toml

    # avoid port double binding
    sed -i -r  "s/pprof_laddr = \"localhost:6060\"/pprof_laddr = \"${PPROF_LADDR}\"/g" ${PROV_NODE_DIR}/config/config.toml

    # allow duplicate IP addresses (all nodes are on the same machine)
    sed -i -r  's/allow_duplicate_ip = false/allow_duplicate_ip = true/g' ${PROV_NODE_DIR}/config/config.toml
done

for MONIKER in "${MONIKERS[@]}"
do
    # validator key
    PROV_KEY=${MONIKER}-key

    # home directory of this validator on provider
    PROV_NODE_DIR=${PROV_NODES_ROOT_DIR}/provider-${MONIKER}

    # copy genesis in, unless this validator is the lead validator
    if [ $MONIKER != $LEAD_VALIDATOR_MONIKER ]; then
        cp ${LEAD_VALIDATOR_PROV_DIR}/config/genesis.json* ${PROV_NODE_DIR}/config/genesis.json
    fi

    # Stake 1/1000 user's coins
    $BINARY_NAME genesis gentx $PROV_KEY $STAKE --chain-id provider --home ${PROV_NODE_DIR} --keyring-backend test --moniker $MONIKER
    sleep 1

    # Copy gentxs to the lead validator for possible future collection. 
    # Obviously we don't need to copy the first validator's gentx to itself
    if [ $MONIKER != $LEAD_VALIDATOR_MONIKER ]; then
        cp ${PROV_NODE_DIR}/config/gentx/* ${LEAD_VALIDATOR_PROV_DIR}/config/gentx/
    fi
done

# Collect genesis transactions with lead validator
$BINARY_NAME genesis collect-gentxs --home ${LEAD_VALIDATOR_PROV_DIR} --gentx-dir ${LEAD_VALIDATOR_PROV_DIR}/config/gentx/

sleep 1

START_COMMANDS=""
for index in "${!MONIKERS[@]}"
do
    MONIKER=${MONIKERS[$index]}

    PERSISTENT_PEERS=""

    for peer_index in "${!MONIKERS[@]}"
    do
        if [ $index == $peer_index ]; then
            continue
        fi
        PEER_MONIKER=${MONIKERS[$peer_index]}

        PEER_PROV_NODE_DIR=${PROV_NODES_ROOT_DIR}/provider-${PEER_MONIKER}

        PEER_NODE_ID=$($BINARY_NAME tendermint show-node-id --home ${PEER_PROV_NODE_DIR})

        PEER_P2P_LADDR_PORT=$(($P2P_LADDR_BASEPORT + $peer_index))
        PERSISTENT_PEERS="$PERSISTENT_PEERS,$PEER_NODE_ID@${NODE_IP}:${PEER_P2P_LADDR_PORT}"
    done

    # remove trailing comma from persistent peers
    PERSISTENT_PEERS=${PERSISTENT_PEERS:1}

    # validator key
    PROV_KEY=${MONIKER}-key

    # home directory of this validator on provider
    PROV_NODE_DIR=${PROV_NODES_ROOT_DIR}/provider-${MONIKER}

    # home directory of this validator on consumer
    CONS_NODE_DIR=${PROV_NODES_ROOT_DIR}/consumer-${MONIKER}

    # copy genesis in, unless this validator is already the lead validator and thus it already has its genesis
    if [ $MONIKER != $LEAD_VALIDATOR_MONIKER ]; then
        cp ${LEAD_VALIDATOR_PROV_DIR}/config/genesis.json ${PROV_NODE_DIR}/config/genesis.json
    fi

    # enable vote extensions by setting .consesnsus.params.abci.vote_extensions_enable_height to 1, but 1 does not work currently - set it to 2 instead. see https://github.com/cosmos/cosmos-sdk/issues/18029#issuecomment-1754598598
    jq ".consensus.params.abci.vote_extensions_enable_height = \"2\"" ${PROV_NODE_DIR}/config/genesis.json > ${PROV_NODE_DIR}/edited_genesis.json && mv ${PROV_NODE_DIR}/edited_genesis.json ${PROV_NODE_DIR}/config/genesis.json

    RPC_LADDR_PORT=$(($RPC_LADDR_BASEPORT + $index))
    P2P_LADDR_PORT=$(($P2P_LADDR_BASEPORT + $index))
    GRPC_LADDR_PORT=$(($GRPC_LADDR_BASEPORT + $index))
    NODE_ADDRESS_PORT=$(($NODE_ADDRESS_BASEPORT + $index))

    PROVIDER_NODE_LISTEN_ADDR_STR="${NODE_IP}:${NODE_ADDRESS_PORT},$PROVIDER_NODE_LISTEN_ADDR_STR"
    PROV_NODES_HOME_STR="${PROV_NODE_DIR},$PROV_NODES_HOME_STR"

    cp -r ${PROV_NODES_ROOT_DIR} ${PROV_NODES_ROOT_DIR}_bkup

    # Start gaia
    echo $BINARY_NAME start \
        --home ${PROV_NODE_DIR} \
        --transport=grpc --with-tendermint=false \
        --p2p.persistent_peers ${PERSISTENT_PEERS} \
        --rpc.laddr tcp://${NODE_IP}:${RPC_LADDR_PORT} \
        --grpc.address ${NODE_IP}:${GRPC_LADDR_PORT} \
        --address tcp://${NODE_IP}:${NODE_ADDRESS_PORT} \
        --p2p.laddr tcp://${NODE_IP}:${P2P_LADDR_PORT} \
        --grpc-web.enable=false "&> ${PROV_NODE_DIR}/logs &" | tee -a start_apps.sh

    sleep 5
done

PROVIDER_NODE_LISTEN_ADDR_STR=${PROVIDER_NODE_LISTEN_ADDR_STR::${#PROVIDER_NODE_LISTEN_ADDR_STR}-1}
PROV_NODES_HOME_STR=${PROV_NODES_HOME_STR::${#PROV_NODES_HOME_STR}-1}

echo "Testnet applications are set up! Starting CometMock..."
echo cometmock $PROVIDER_NODE_LISTEN_ADDR_STR ${LEAD_VALIDATOR_PROV_DIR}/config/genesis.json $PROVIDER_COMETMOCK_ADDR $PROV_NODES_HOME_STR grpc $COMETMOCK_ARGS | tee -a start_cometmock.sh

chmod +x start_apps.sh
chmod +x start_cometmock.sh

# cometmock $PROVIDER_NODE_LISTEN_ADDR_STR ${LEAD_VALIDATOR_PROV_DIR}/config/genesis.json $PROVIDER_COMETMOCK_ADDR $PROV_NODES_HOME_STR grpc $COMETMOCK_ARGS &> ${LEAD_VALIDATOR_PROV_DIR}/cometmock_log &