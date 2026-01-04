#!/bin/bash

# NDAG Coin Development Network Launcher
# Launches a local development network with multiple nodes

set -e

# Colors
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Configuration
NUM_NODES=3
NETWORK="devnet"
BASE_PORT=9000
BASE_API_PORT=9090

# Directory setup
BASE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BUILD_DIR="$BASE_DIR/build"
CONFIGS_DIR="$BASE_DIR/configs"
DEVNET_DIR="$BASE_DIR/.devnet"

# Create directories
echo -e "${BLUE}Setting up development network...${NC}"
mkdir -p "$DEVNET_DIR"
mkdir -p "$DEVNET_DIR/logs"
mkdir -p "$DEVNET_DIR/data"

# Check if binaries exist
if [[ ! -f "$BUILD_DIR/ndagd" ]]; then
    echo -e "${YELLOW}Building NDAG Coin binaries...${NC}"
    cd "$BASE_DIR"
    make build
fi

if [[ ! -f "$BUILD_DIR/ndagd" ]]; then
    echo -e "${RED}Error: ndagd binary not found${NC}"
    exit 1
fi

# Generate genesis if it doesn't exist
if [[ ! -f "$DEVNET_DIR/genesis.pb" ]]; then
    echo -e "${BLUE}Generating genesis file...${NC}"
    
    # Create genesis config
    cat > "$DEVNET_DIR/genesis.toml" <<EOF
[network]
network_id = "ndag-devnet"
chain_id = "ndag-devnet-v1"
data_dir = "$DEVNET_DIR"

[consensus]
round_duration = "1s"
epoch_duration = "1h"
min_validator_stake = 1000000
min_gas_price = 100

[genesis]
initial_supply = 100000000000000
EOF
    
    # Create genesis block
    "$BUILD_DIR/ndagctl" genesis create \
        --network devnet \
        --config "$DEVNET_DIR/genesis.toml" \
        --output "$DEVNET_DIR/genesis.pb"
    
    echo -e "${GREEN}Genesis created successfully${NC}"
fi

# Generate validator keys
echo -e "${BLUE}Generating validator keys...${NC}"
VALIDATOR_KEYS=()
for i in $(seq 1 $NUM_NODES); do
    KEY_FILE="$DEVNET_DIR/validator-$i.key"
    if [[ ! -f "$KEY_FILE" ]]; then
        echo "Generating validator key $i..."
        "$BUILD_DIR/ndagctl" wallet generate \
            --name "validator-$i" \
            --output "$KEY_FILE" \
            --password "devnet123"
    fi
    VALIDATOR_KEYS+=("$KEY_FILE")
done

# Start nodes
echo -e "${BLUE}Starting $NUM_NODES validator nodes...${NC}"
PIDS=()

for i in $(seq 1 $NUM_NODES); do
    NODE_DIR="$DEVNET_DIR/node-$i"
    mkdir -p "$NODE_DIR"
    mkdir -p "$NODE_DIR/data"
    
    PORT=$((BASE_PORT + i))
    API_PORT=$((BASE_API_PORT + i))
    
    # Create node configuration
    cat > "$NODE_DIR/config.toml" <<EOF
[network]
network_id = "ndag-devnet"
chain_id = "ndag-devnet-v1"
data_dir = "$NODE_DIR"

[p2p]
enabled = true
listen_address = "/ip4/127.0.0.1/tcp/$PORT"
bootstrap_peers = [
EOF

    # Add bootstrap peers (all previous nodes)
    for j in $(seq 1 $((i-1))); do
        BOOTSTRAP_PORT=$((BASE_PORT + j))
        cat >> "$NODE_DIR/config.toml" <<EOF
    "/ip4/127.0.0.1/tcp/$BOOTSTRAP_PORT/p2p/node-$j",
EOF
    done
    
    # Complete the config
    cat >> "$NODE_DIR/config.toml" <<EOF
]
max_peers = 10
min_peers = 2

[consensus]
enabled = true
round_duration = "1s"
epoch_duration = "10m"
min_validator_stake = 1000000
min_gas_price = 100

[api]
enabled = true
grpc_listen_address = ":$API_PORT"
enable_rest = true
rest_listen_address = ":$((API_PORT + 100))"

[storage]
backend = "leveldb"
path = "data"
enable_pruning = true

[validator]
enabled = true
key_file = "${VALIDATOR_KEYS[$((i-1))]}"
commission_rate = 10000000
min_self_stake = 1000000

[logging]
level = "info"
format = "text"

[genesis]
genesis_file = "$DEVNET_DIR/genesis.pb"
initial_supply = 100000000000000
EOF
    
    echo -e "${GREEN}Starting node $i on port $PORT...${NC}"
    
    # Start node in background
    nohup "$BUILD_DIR/ndagd" start \
        --config "$NODE_DIR/config.toml" \
        > "$DEVNET_DIR/logs/node-$i.log" 2>&1 &
    
    PID=$!
    PIDS+=("$PID")
    
    echo "Node $i started with PID $PID"
    echo "Logs: $DEVNET_DIR/logs/node-$i.log"
    
    # Wait a bit between starting nodes
    sleep 1
done

# Wait for nodes to start
echo -e "${BLUE}Waiting for nodes to initialize...${NC}"
sleep 5

# Display network info
echo -e "${GREEN}Development network started successfully!${NC}"
echo
echo "Network Information:"
echo "==================="
echo "Network: $NETWORK"
echo "Nodes: $NUM_NODES"
echo "Base P2P Port: $BASE_PORT"
echo "Base API Port: $BASE_API_PORT"
echo
echo "Node Status:"
for i in $(seq 1 $NUM_NODES); do
    PORT=$((BASE_API_PORT + i))
    echo "  Node $i: P2P=$((BASE_PORT + i)), API=$PORT, gRPC=$((PORT + 100))"
done

echo
echo "Log Files:"
for i in $(seq 1 $NUM_NODES); do
    echo "  Node $i: $DEVNET_DIR/logs/node-$i.log"
done

echo
echo "Process IDs:"
for i in $(seq 1 $NUM_NODES); do
    echo "  Node $i: ${PIDS[$((i-1))]}"
done

echo
echo -e "${YELLOW}To monitor logs:${NC}"
echo "tail -f $DEVNET_DIR/logs/node-*.log"

echo
echo -e "${YELLOW}To stop the network:${NC}"
echo "pkill -f ndagd"

echo
echo -e "${YELLOW}Example commands:${NC}"
echo "# Check node status"
echo "grpcurl -plaintext localhost:9091 ndagcoin.pb.NodeService.GetNodeStatus"

echo
echo "# Send transaction"
echo "$BUILD_DIR/ndagctl tx transfer --from validator-1 --to <address> --amount 1000"

echo
echo -e "${YELLOW}Press Ctrl+C to stop all nodes${NC}"

# Cleanup function
cleanup() {
    echo
echo -e "${YELLOW}Stopping devnet...${NC}"
    for pid in "${PIDS[@]}"; do
        kill "$pid" 2>/dev/null || true
    done
    
    # Wait for processes to exit
    for pid in "${PIDS[@]}"; do
        while kill -0 "$pid" 2>/dev/null; do
            sleep 0.1
        done
    done
    
    echo -e "${GREEN}Devnet stopped${NC}"
    exit 0
}

# Trap cleanup function
trap cleanup SIGINT SIGTERM

# Keep script running
wait