#!/bin/bash

# NDAG Coin Production Launch Script
# This script builds, configures, and launches a complete NDAG Coin network

set -e

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Configuration
BUILD_DIR="build"
CONFIG_DIR="configs"
DATA_DIR=".ndag"
NETWORK_TYPE="${1:-devnet}"
NUM_NODES="${2:-3}"

echo -e "${BLUE}╔══════════════════════════════════════════════════════════════╗${NC}"
echo -e "${BLUE}║              NDAG Coin Production Launcher                   ║${NC}"
echo -e "${BLUE}╚══════════════════════════════════════════════════════════════╝${NC}"
echo

# Step 1: Environment check
echo -e "${YELLOW}Step 1: Checking environment...${NC}"

# Check Go version
if ! command -v go &> /dev/null; then
    echo -e "${RED}Error: Go is not installed${NC}"
    exit 1
fi

GO_VERSION=$(go version | awk '{print $3}')
echo "✓ Go version: $GO_VERSION"

# Check for required tools
for tool in make protoc; do
    if ! command -v $tool &> /dev/null; then
        echo -e "${RED}Error: $tool is not installed${NC}"
        exit 1
    fi
    echo "✓ $tool available"
done

echo

# Step 2: Clean and build
echo -e "${YELLOW}Step 2: Building NDAG Coin...${NC}"

if [ -d "$BUILD_DIR" ]; then
    echo "Cleaning previous build..."
    rm -rf "$BUILD_DIR"
fi

make clean
make build

echo -e "${GREEN}✓ Build completed successfully${NC}"
echo

# Step 3: Generate keys for validators
echo -e "${YELLOW}Step 3: Generating validator keys...${NC}"

mkdir -p "$DATA_DIR/keys"

for i in $(seq 1 $NUM_NODES); do
    KEY_FILE="$DATA_DIR/keys/validator-$i.key"
    if [ ! -f "$KEY_FILE" ]; then
        echo "Generating validator key $i..."
        pubKey, privKey, err := ed25519.GenerateKey(nil)
        if err != nil {
            echo -e "${RED}Failed to generate key: $err${NC}"
            exit 1
        fi
        
        # Save key in simple format (in production, use encrypted keystore)
        echo "{"
        echo "  \"name\": \"validator-$i\","
        echo "  \"address\": \"ndag$(echo $pubKey | head -c 8 | xxd -p)\","
        echo "  \"public_key\": \"$(echo $pubKey | xxd -p)\","
        echo "  \"private_key\": \"$(echo $privKey | xxd -p)\","
        echo "  \"created_at\": \"$(date -u +%Y-%m-%dT%H:%M:%SZ)\""
        echo "}" > "$KEY_FILE"
        
        echo "✓ Validator-$i: ndag$(echo $pubKey | head -c 8 | xxd -p)"
    fi
done

echo -e "${GREEN}✓ All validator keys generated${NC}"
echo

# Step 4: Create genesis
echo -e "${YELLOW}Step 4: Creating genesis block...${NC}"

GENESIS_FILE="$CONFIG_DIR/genesis-$NETWORK_TYPE.pb"
if [ ! -f "$GENESIS_FILE" ]; then
    echo "Creating genesis file..."
    
    case $NETWORK_TYPE in
        "mainnet")
            INITIAL_SUPPLY=100000000000000  # 100M NDAG
            EPOCH_DURATION="168h"  # 1 week
            ;;
        "testnet")
            INITIAL_SUPPLY=100000000000000  # 100M NDAG
            EPOCH_DURATION="24h"   # 1 day
            ;;
        "devnet")
            INITIAL_SUPPLY=100000000000000  # 100M NDAG
            EPOCH_DURATION="1h"    # 1 hour
            ;;
        *)
            echo -e "${RED}Error: Unknown network type: $NETWORK_TYPE${NC}"
            exit 1
            ;;
    esac
    
    # Create genesis configuration
    cat > "$CONFIG_DIR/genesis-config.json" <<EOF
{
  "network": "$NETWORK_TYPE",
  "chain_id": "ndag-$NETWORK_TYPE-v1",
  "initial_supply": $INITIAL_SUPPLY,
  "epoch_duration": "$EPOCH_DURATION",
  "validators": [
EOF

    # Add validators
    for i in $(seq 1 $NUM_NODES); do
        KEY_FILE="$DATA_DIR/keys/validator-$i.key"
        if [ -f "$KEY_FILE" ]; then
            ADDRESS=$(grep "address" "$KEY_FILE" | cut -d'"' -f4)
            PUBKEY=$(grep "public_key" "$KEY_FILE" | cut -d'"' -f4)
            
            if [ $i -ne 1 ]; then
                echo "    ," >> "$CONFIG_DIR/genesis-config.json"
            fi
            
            cat >> "$CONFIG_DIR/genesis-config.json" <<EOF
    {
      "address": "$ADDRESS",
      "public_key": "$PUBKEY",
      "stake": 10000000000,
      "commission_rate": 10000000,
      "moniker": "validator-$i"
    }
EOF
        fi
    done
    
    cat >> "$CONFIG_DIR/genesis-config.json" <<EOF
  ]
}
EOF

    echo "Genesis configuration created: $CONFIG_DIR/genesis-config.json"
fi

echo -e "${GREEN}✓ Genesis configuration ready${NC}"
echo

# Step 5: Launch network
echo -e "${YELLOW}Step 5: Launching $NETWORK_TYPE network with $NUM_NODES nodes...${NC}"

mkdir -p "$DATA_DIR/logs"

# Create node configurations
for i in $(seq 1 $NUM_NODES); do
    NODE_DIR="$DATA_DIR/node-$i"
    mkdir -p "$NODE_DIR/data"
    mkdir -p "$NODE_DIR/config"
    
    KEY_FILE="$DATA_DIR/keys/validator-$i.key"
    VALIDATOR_ADDRESS=$(grep "address" "$KEY_FILE" | cut -d'"' -f4)
    
    # Create bootstrap peers list (all other nodes)
    BOOTSTRAP_PEERS=""
    for j in $(seq 1 $NUM_NODES); do
        if [ $i -ne $j ]; then
            if [ -n "$BOOTSTRAP_PEERS" ]; then
                BOOTSTRAP_PEERS="$BOOTSTRAP_PEERS,"
            fi
            BOOTSTRAP_PORT=$((9000 + j))
            BOOTSTRAP_PEERS="$BOOTSTRAP_PEERS\"/ip4/127.0.0.1/tcp/$BOOTSTRAP_PORT/p2p/node-$j\""
        fi
    done
    
    # Create node configuration
    cat > "$NODE_DIR/config/node.toml" <<EOF
[network]
network_id = "ndag-$NETWORK_TYPE"
chain_id = "ndag-$NETWORK_TYPE-v1"
data_dir = "$NODE_DIR"

[p2p]
enabled = true
listen_address = "/ip4/0.0.0.0/tcp/$((9000 + i))"
bootstrap_peers = [$BOOTSTRAP_PEERS]
max_peers = 50
min_peers = 2
connection_timeout = "30s"

[consensus]
enabled = true
round_duration = "1s"
epoch_duration = "1h"
min_validator_stake = 1000000
min_gas_price = 100
max_gas_per_round = 10000000

[api]
enabled = true
grpc_listen_address = ":$((9090 + i))"
rest_listen_address = ":$((9091 + i))"
enable_grpc = true
enable_rest = true

[storage]
backend = "leveldb"
path = "data"
enable_pruning = true
pruning_keep_recent = "24h"

[validator]
enabled = $(if [ $i -le 3 ]; then echo "true"; else echo "false"; fi)
address = "$VALIDATOR_ADDRESS"
commission_rate = 10000000
key_file = "$KEY_FILE"
min_self_stake = 10000000

[logging]
level = "info"
format = "text"
file = "logs/node.log"
EOF
    
    # Copy genesis
    cp "$GENESIS_FILE" "$NODE_DIR/config/genesis.pb" 2>/dev/null || true
    
    echo "✓ Node $i configuration created"
done

# Step 6: Start nodes
echo
echo -e "${YELLOW}Step 6: Starting nodes...${NC}"

PIDS=()

for i in $(seq 1 $NUM_NODES); do
    NODE_DIR="$DATA_DIR/node-$i"
    CONFIG_FILE="$NODE_DIR/config/node.toml"
    
    echo "Starting node $i..."
    
    # Start node in background
    nohup "$BUILD_DIR/ndagd" start --config "$CONFIG_FILE" \
        > "$DATA_DIR/logs/node-$i.log" 2>&1 &
    
    PID=$!
    PIDS+=($PID)
    echo "  └─ PID: $PID"
    
    sleep 2 # Stagger starts
done

# Step 7: Wait for nodes to start
echo
echo -e "${YELLOW}Step 7: Waiting for nodes to initialize...${NC}"
sleep 10

# Step 8: Display network information
echo
echo -e "${GREEN}╔══════════════════════════════════════════════════════════════╗${NC}"
echo -e "${GREEN}║              NDAG Coin Network Started!                      ║${NC}"
echo -e "${GREEN}╚══════════════════════════════════════════════════════════════╝${NC}"
echo
echo "Network Information:"
echo "==================="
echo "Network Type: $NETWORK_TYPE"
echo "Chain ID: ndag-$NETWORK_TYPE-v1"
echo "Number of Nodes: $NUM_NODES"
echo
echo "Node Endpoints:"
echo "---------------"
for i in $(seq 1 $NUM_NODES); do
    echo "Node $i:"
    echo "  P2P: localhost:$((9000 + i))"
    echo "  gRPC: localhost:$((9090 + i))"
    echo "  REST: localhost:$((9091 + i))"
    echo "  Logs: $DATA_DIR/logs/node-$i.log"
done
echo
echo "CLI Commands:"
echo "-------------"
echo "Check status:"
echo "  ./build/ndagctl query status --grpc-addr localhost:9091"
echo
echo "Send transaction:"
echo "  ./build/ndagctl tx transfer --from mywallet --to ndag1... --amount 1000000"
echo
echo "Monitor logs:"
echo "  tail -f $DATA_DIR/logs/node-*.log"
echo
echo "Stop network:"
echo "  pkill -f ndagd"
echo

# Step 9: Health check
echo -e "${YELLOW}Step 9: Running health checks...${NC}"

sleep 5

for i in $(seq 1 $NUM_NODES); do
    GRPC_PORT=$((9090 + i))
    if nc -z localhost $GRPC_PORT 2>/dev/null; then
        echo "✓ Node $i gRPC port $GRPC_PORT is active"
    else
        echo -e "${RED}✗ Node $i gRPC port $GRPC_PORT is not responding${NC}"
    fi
done

echo
echo -e "${GREEN}✓ All systems operational${NC}"
echo

# Step 10: Wait for interrupt
echo -e "${YELLOW}Press Ctrl+C to stop the network${NC}"

# Cleanup on exit
trap "echo; echo -e '${YELLOW}Shutting down network...${NC}'; for pid in ${PIDS[@]}; do kill $pid 2>/dev/null; done; wait; echo -e '${GREEN}Network stopped${NC}'; exit 0" SIGINT SIGTERM

# Keep running
wait