#!/bin/bash

# After the testnet was started, this script can restart it.
# It does so by killing the existing testnet,
# overwriting the node home directories with backups made
# right after initializatio, and then starting the testnet again.


BINARY_NAME=$1

set -eux

ROOT_DIR=${HOME}/nodes/provider
BACKUP_DIR=${ROOT_DIR}_bkup

if [ -z "$BINARY_NAME" ]; then
    echo "Usage: $0 <binary_name> [cometmock_args]"
    exit 1
fi

# Kill the testnet
pkill -f ^$BINARY_NAME &> /dev/null || true
pkill -f ^cometmock &> /dev/null || true

# Restore the backup
rm -rf ${ROOT_DIR}
cp -r ${BACKUP_DIR} ${ROOT_DIR}