#!/bin/bash

## This script sets up the local testnet and starts it.
## To run this, both the application binary and cometmock must be installed.
set -eux 

parent_path=$( cd "$(dirname "${BASH_SOURCE[0]}")" ; pwd -P )
pushd "$parent_path"

BINARY_NAME=$1

COMETMOCK_ARGS=$2

# set up the net
./local-testnet-singlechain-setup.sh $BINARY_NAME "$COMETMOCK_ARGS"
./local-testnet-singlechain-start.sh
