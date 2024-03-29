#!/bin/bash
set -eux 

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
pkill -f ^interchain-security-pd &> /dev/null || true
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
    interchain-security-pd init $MONIKER --chain-id provider --home ${PROV_NODE_DIR}
    jq ".app_state.gov.params.voting_period = \"10s\" | .app_state.staking.params.unbonding_time = \"86400s\"" \
    ${PROV_NODE_DIR}/config/genesis.json > \
    ${PROV_NODE_DIR}/edited_genesis.json && mv ${PROV_NODE_DIR}/edited_genesis.json ${PROV_NODE_DIR}/config/genesis.json

    sleep 1

    # Create account keypair
    interchain-security-pd keys add $PROV_KEY --home ${PROV_NODE_DIR} --keyring-backend test --output json > ${PROV_NODE_DIR}/${PROV_KEY}.json 2>&1
    sleep 1

    # copy genesis in, unless this validator is the lead validator
    if [ $MONIKER != $LEAD_VALIDATOR_MONIKER ]; then
        cp ${LEAD_VALIDATOR_PROV_DIR}/config/genesis.json ${PROV_NODE_DIR}/config/genesis.json
    fi

    # Add stake to user
    PROV_ACCOUNT_ADDR=$(jq -r '.address' ${PROV_NODE_DIR}/${PROV_KEY}.json)
    interchain-security-pd genesis add-genesis-account $PROV_ACCOUNT_ADDR $USER_COINS --home ${PROV_NODE_DIR} --keyring-backend test
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
    interchain-security-pd genesis gentx $PROV_KEY $STAKE --chain-id provider --home ${PROV_NODE_DIR} --keyring-backend test --moniker $MONIKER
    sleep 1

    # Copy gentxs to the lead validator for possible future collection. 
    # Obviously we don't need to copy the first validator's gentx to itself
    if [ $MONIKER != $LEAD_VALIDATOR_MONIKER ]; then
        cp ${PROV_NODE_DIR}/config/gentx/* ${LEAD_VALIDATOR_PROV_DIR}/config/gentx/
    fi
done

# Collect genesis transactions with lead validator
interchain-security-pd genesis collect-gentxs --home ${LEAD_VALIDATOR_PROV_DIR} --gentx-dir ${LEAD_VALIDATOR_PROV_DIR}/config/gentx/

sleep 1


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

        PEER_NODE_ID=$(interchain-security-pd tendermint show-node-id --home ${PEER_PROV_NODE_DIR})

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

    RPC_LADDR_PORT=$(($RPC_LADDR_BASEPORT + $index))
    P2P_LADDR_PORT=$(($P2P_LADDR_BASEPORT + $index))
    GRPC_LADDR_PORT=$(($GRPC_LADDR_BASEPORT + $index))
    NODE_ADDRESS_PORT=$(($NODE_ADDRESS_BASEPORT + $index))

    PROVIDER_NODE_LISTEN_ADDR_STR="${NODE_IP}:${NODE_ADDRESS_PORT},$PROVIDER_NODE_LISTEN_ADDR_STR"
    PROV_NODES_HOME_STR="${PROV_NODE_DIR},$PROV_NODES_HOME_STR"

    # Start gaia
    interchain-security-pd start \
        --home ${PROV_NODE_DIR} \
        --transport=grpc --with-tendermint=false \
        --p2p.persistent_peers ${PERSISTENT_PEERS} \
        --rpc.laddr tcp://${NODE_IP}:${RPC_LADDR_PORT} \
        --grpc.address ${NODE_IP}:${GRPC_LADDR_PORT} \
        --address tcp://${NODE_IP}:${NODE_ADDRESS_PORT} \
        --p2p.laddr tcp://${NODE_IP}:${P2P_LADDR_PORT} \
        --grpc-web.enable=false &> ${PROV_NODE_DIR}/logs &

    sleep 5
done

PROVIDER_NODE_LISTEN_ADDR_STR=${PROVIDER_NODE_LISTEN_ADDR_STR::${#PROVIDER_NODE_LISTEN_ADDR_STR}-1}
PROV_NODES_HOME_STR=${PROV_NODES_HOME_STR::${#PROV_NODES_HOME_STR}-1}

cometmock $PROVIDER_NODE_LISTEN_ADDR_STR ${LEAD_VALIDATOR_PROV_DIR}/config/genesis.json $PROVIDER_COMETMOCK_ADDR $PROV_NODES_HOME_STR grpc &> ${LEAD_VALIDATOR_PROV_DIR}/cometmock_log &

sleep 5

# Build consumer chain proposal file
tee ${LEAD_VALIDATOR_PROV_DIR}/consumer-proposal.json<<EOF
{
    "title": "Create a chain",
    "description": "Gonna be a great chain",
    "chain_id": "consumer",
    "initial_height": {
        "revision_height": 1
    },
    "genesis_hash": "Z2VuX2hhc2g=",
    "binary_hash": "YmluX2hhc2g=",
    "spawn_time": "2023-03-11T09:02:14.718477-08:00",
    "deposit": "10000001stake",
    "consumer_redistribution_fraction": "0.75",
    "blocks_per_distribution_transmission": 1000,
    "historical_entries": 10000,
    "unbonding_period": 864000000000000,
    "ccv_timeout_period": 259200000000000,
    "transfer_timeout_period": 1800000000000,
    "summary":        "a summary",
    "metadata": "meta"
}
EOF

interchain-security-pd keys show $LEAD_PROV_KEY --keyring-backend test --home ${LEAD_VALIDATOR_PROV_DIR}

# Submit consumer chain proposal; use 100* standard gas to ensure we have enough
interchain-security-pd tx gov submit-legacy-proposal consumer-addition ${LEAD_VALIDATOR_PROV_DIR}/consumer-proposal.json --chain-id provider --from $LEAD_PROV_KEY --home ${LEAD_VALIDATOR_PROV_DIR} --node $PROVIDER_COMETMOCK_ADDR  --keyring-backend test -b sync -y --gas 20000000

sleep 1

# Vote yes to proposal
for index in "${!MONIKERS[@]}"
do
    MONIKER=${MONIKERS[$index]}
    PROV_KEY=${MONIKER}-key
    RPC_LADDR_PORT=$(($RPC_LADDR_BASEPORT + $index))
    RPC_LADDR=tcp://${NODE_IP}:${RPC_LADDR_PORT}

    PROV_NODE_DIR=${PROV_NODES_ROOT_DIR}/provider-${MONIKER}
    interchain-security-pd tx gov vote 1 yes --from $PROV_KEY --chain-id provider --home ${PROV_NODE_DIR} --node $PROVIDER_COMETMOCK_ADDR -b sync -y --keyring-backend test
done

# sleep 3

# # ## CONSUMER CHAIN ##

# # Clean start
pkill -f ^interchain-security-cd &> /dev/null || true
sleep 1
rm -rf ${CONS_NODES_ROOT_DIR}

for index in "${!MONIKERS[@]}"
do
    MONIKER=${MONIKERS[$index]}
    # validator key
    PROV_KEY=${MONIKER}-key

    PROV_NODE_DIR=${PROV_NODES_ROOT_DIR}/provider-${MONIKER}

    # home directory of this validator on consumer
    CONS_NODE_DIR=${CONS_NODES_ROOT_DIR}/consumer-${MONIKER}

    # Build genesis file and node directory structure
    interchain-security-cd init $MONIKER --chain-id consumer  --home ${CONS_NODE_DIR}

    sleep 1

    # Create account keypair
    interchain-security-cd keys add $PROV_KEY --home  ${CONS_NODE_DIR} --keyring-backend test --output json > ${CONS_NODE_DIR}/${PROV_KEY}.json 2>&1
    sleep 1

    # copy genesis in, unless this validator is the lead validator
    if [ $MONIKER != $LEAD_VALIDATOR_MONIKER ]; then
        cp ${LEAD_VALIDATOR_CONS_DIR}/config/genesis.json ${CONS_NODE_DIR}/config/genesis.json
    fi

    # Add stake to user
    CONS_ACCOUNT_ADDR=$(jq -r '.address' ${CONS_NODE_DIR}/${PROV_KEY}.json)
    interchain-security-cd genesis add-genesis-account $CONS_ACCOUNT_ADDR $USER_COINS --home ${CONS_NODE_DIR}
    sleep 10

    ### this probably doesnt have to be done for each node
    # Add consumer genesis states to genesis file
    RPC_LADDR_PORT=$(($RPC_LADDR_BASEPORT + $index))
    RPC_LADDR=tcp://${NODE_IP}:${RPC_LADDR_PORT}
    interchain-security-pd query provider consumer-genesis consumer --home ${PROV_NODE_DIR} --node $PROVIDER_COMETMOCK_ADDR -o json > consumer_gen.json
    jq -s '.[0].app_state.ccvconsumer = .[1] | .[0]' ${CONS_NODE_DIR}/config/genesis.json consumer_gen.json > ${CONS_NODE_DIR}/edited_genesis.json \
    && mv ${CONS_NODE_DIR}/edited_genesis.json ${CONS_NODE_DIR}/config/genesis.json
    rm consumer_gen.json
    ###

    # copy genesis out, unless this validator is the lead validator
    if [ $MONIKER != $LEAD_VALIDATOR_MONIKER ]; then
        cp ${CONS_NODE_DIR}/config/genesis.json ${LEAD_VALIDATOR_CONS_DIR}/config/genesis.json
    fi

    PPROF_LADDR=${NODE_IP}:$(($PPROF_LADDR_BASEPORT + ${#MONIKERS[@]} + $index))
    P2P_LADDR_PORT=$(($P2P_LADDR_BASEPORT + ${#MONIKERS[@]} + $index))

    # adjust configs of this node
    sed -i -r 's/timeout_commit = "5s"/timeout_commit = "3s"/g' ${CONS_NODE_DIR}/config/config.toml
    sed -i -r 's/timeout_propose = "3s"/timeout_propose = "1s"/g' ${CONS_NODE_DIR}/config/config.toml

    # make address book non-strict. necessary for this setup
    sed -i -r 's/addr_book_strict = true/addr_book_strict = false/g' ${CONS_NODE_DIR}/config/config.toml

    # avoid port double binding
    sed -i -r  "s/pprof_laddr = \"localhost:6060\"/pprof_laddr = \"${PPROF_LADDR}\"/g" ${CONS_NODE_DIR}/config/config.toml

    # allow duplicate IP addresses (all nodes are on the same machine)
    sed -i -r  's/allow_duplicate_ip = false/allow_duplicate_ip = true/g' ${CONS_NODE_DIR}/config/config.toml

    # Create validator states
    echo '{"height": "0","round": 0,"step": 0}' > ${CONS_NODE_DIR}/data/priv_validator_state.json

    # Copy validator key files
    cp ${PROV_NODE_DIR}/config/priv_validator_key.json ${CONS_NODE_DIR}/config/priv_validator_key.json
    cp ${PROV_NODE_DIR}/config/node_key.json ${CONS_NODE_DIR}/config/node_key.json

    # Set default client port
    CLIENT_PORT=$(($CLIENT_BASEPORT + ${#MONIKERS[@]} + $index))
    sed -i -r "/node =/ s/= .*/= \"tcp:\/\/${NODE_IP}:${CLIENT_PORT}\"/" ${CONS_NODE_DIR}/config/client.toml

done

sleep 1


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

        PEER_CONS_NODE_DIR=${CONS_NODES_ROOT_DIR}/consumer-${PEER_MONIKER}

        PEER_NODE_ID=$(interchain-security-pd tendermint show-node-id --home ${PEER_CONS_NODE_DIR})

        PEER_P2P_LADDR_PORT=$(($P2P_LADDR_BASEPORT + ${#MONIKERS[@]} + $peer_index))
        PERSISTENT_PEERS="$PERSISTENT_PEERS,$PEER_NODE_ID@${NODE_IP}:${PEER_P2P_LADDR_PORT}"
    done

    # remove trailing comma from persistent peers
    PERSISTENT_PEERS=${PERSISTENT_PEERS:1}

    # validator key
    PROV_KEY=${MONIKER}-key

    # home directory of this validator on provider
    PROV_NODE_DIR=${PROV_NODES_ROOT_DIR}/provider-${MONIKER}

    # home directory of this validator on consumer
    CONS_NODE_DIR=${CONS_NODES_ROOT_DIR}/consumer-${MONIKER}

    # copy genesis in, unless this validator is already the lead validator and thus it already has its genesis
    if [ $MONIKER != $LEAD_VALIDATOR_MONIKER ]; then
        cp ${LEAD_VALIDATOR_CONS_DIR}/config/genesis.json ${CONS_NODE_DIR}/config/genesis.json
    fi

    RPC_LADDR_PORT=$(($RPC_LADDR_BASEPORT + ${#MONIKERS[@]} + $index))
    P2P_LADDR_PORT=$(($P2P_LADDR_BASEPORT + ${#MONIKERS[@]} + $index))
    GRPC_LADDR_PORT=$(($GRPC_LADDR_BASEPORT + ${#MONIKERS[@]} + $index))
    NODE_ADDRESS_PORT=$(($NODE_ADDRESS_BASEPORT + ${#MONIKERS[@]} + $index))

    CONSUMER_NODE_LISTEN_ADDR_STR="${NODE_IP}:${NODE_ADDRESS_PORT},$CONSUMER_NODE_LISTEN_ADDR_STR"
    # Add the home directory to the CONS_NODES_HOME_STR
    CONS_NODES_HOME_STR="${CONS_NODE_DIR},${CONS_NODES_HOME_STR}"

    # Start gaia
    interchain-security-cd start \
        --home ${CONS_NODE_DIR} \
        --transport=grpc --with-tendermint=false \
        --p2p.persistent_peers ${PERSISTENT_PEERS} \
        --rpc.laddr tcp://${NODE_IP}:${RPC_LADDR_PORT} \
        --grpc.address ${NODE_IP}:${GRPC_LADDR_PORT} \
        --address tcp://${NODE_IP}:${NODE_ADDRESS_PORT} \
        --p2p.laddr tcp://${NODE_IP}:${P2P_LADDR_PORT} \
        --grpc-web.enable=false &> ${CONS_NODE_DIR}/logs &

    sleep 6
done

# remove trailing comma from consumer node listen addr str
CONSUMER_NODE_LISTEN_ADDR_STR=${CONSUMER_NODE_LISTEN_ADDR_STR::${#CONSUMER_NODE_LISTEN_ADDR_STR}-1}
CONS_NODES_HOME_STR=${CONS_NODES_HOME_STR::${#CONS_NODES_HOME_STR}-1}

cometmock $CONSUMER_NODE_LISTEN_ADDR_STR ${LEAD_VALIDATOR_CONS_DIR}/config/genesis.json $CONSUMER_COMETMOCK_ADDR $CONS_NODES_HOME_STR grpc &> ${LEAD_VALIDATOR_CONS_DIR}/cometmock_log &

sleep 3

rm -r ~/.relayer

# initialize gorelayer
rly config init

# add chain configs

echo "{
    \"type\": \"cosmos\",
    \"value\": {
        \"key\": \"default\",
        \"chain-id\": \"provider\",
        \"rpc-addr\": \"${PROVIDER_COMETMOCK_ADDR}\",
        \"account-prefix\": \"cosmos\",
        \"keyring-backend\": \"test\",
        \"gas-adjustment\": 1.2,
        \"gas-prices\": \"0.01stake\",
        \"debug\": true,
        \"timeout\": \"20s\",
        \"output-format\": \"json\",
        \"sign-mode\": \"direct\"
    }
}" > go_rly_provider.json

echo "{
    \"type\": \"cosmos\",
    \"value\": {
        \"key\": \"default\",
        \"chain-id\": \"consumer\",
        \"rpc-addr\": \"${CONSUMER_COMETMOCK_ADDR}\",
        \"account-prefix\": \"cosmos\",
        \"keyring-backend\": \"test\",
        \"gas-adjustment\": 1.2,
        \"gas-prices\": \"0.01stake\",
        \"debug\": true,
        \"timeout\": \"20s\",
        \"output-format\": \"json\",
        \"sign-mode\": \"direct\"
    }
}" > go_rly_consumer.json

# add chains
rly chains add --file go_rly_provider.json provider
rly chains add --file go_rly_consumer.json consumer

# gorelayer
rly keys delete consumer default -y || true
rly keys delete provider default -y || true

# take keys from provider and consumer and add them to gorelayer
rly keys restore provider default "$(cat ${LEAD_VALIDATOR_PROV_DIR}/${LEAD_VALIDATOR_MONIKER}-key.json | jq -r '.mnemonic')"
rly keys restore consumer default "$(cat ${LEAD_VALIDATOR_CONS_DIR}/${LEAD_VALIDATOR_MONIKER}-key.json | jq -r '.mnemonic')"

rly paths add consumer provider testpath --file go_rly_ics_path_config.json
rly tx clients testpath
rly tx connection testpath
rly tx channel testpath --src-port consumer --dst-port provider --version 1 --order ordered --debug
rly start
