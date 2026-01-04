.PHONY: build proto test clean lint fmt run-devnet

# Build settings
BINARY_NAME:=ndagd
CLI_NAME:=ndagctl
BUILD_DIR:=build
PROTO_DIR:=proto
GO_PROTO_DIR:=pkg/pb

# Go settings
GOCMD:=go
GOBUILD:=$(GOCMD) build
GOTEST:=$(GOCMD) test
GOGET:=$(GOCMD) get
GOMOD:=$(GOCMD) mod
GOFMT:=gofmt

# Build flags
LDFLAGS:=-ldflags "-s -w"
BUILD_FLAGS:=$(LDFLAGS)

# Directories to create
DIRS:=cmd/ndagd cmd/ndagctl pkg/pb pkg/crypto pkg/consensus pkg/state pkg/storage \
	   pkg/network pkg/mempool pkg/api internal/ tests/ scripts/ configs/ docs/ docker/

all: dirs proto build

dirs:
	@mkdir -p $(DIRS)

# Install protoc and plugins
proto-deps:
	@echo "Installing protobuf dependencies..."
	$(GOCMD) install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	$(GOCMD) install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

# Generate protobuf files
proto: proto-deps
	@echo "Generating protobuf files..."
	@mkdir -p $(GO_PROTO_DIR)
	protoc --go_out=$(GO_PROTO_DIR) --go_opt=paths=source_relative \
	       --go-grpc_out=$(GO_PROTO_DIR) --go-grpc_opt=paths=source_relative \
	       $(PROTO_DIR)/*.proto

# Build the main daemon
build: $(BINARY_NAME) $(CLI_NAME)

$(BINARY_NAME): dirs
	@echo "Building $(BINARY_NAME)..."
	$(GOBUILD) $(BUILD_FLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/ndagd

# Build the CLI tool
$(CLI_NAME): dirs
	@echo "Building $(CLI_NAME)..."
	$(GOBUILD) $(BUILD_FLAGS) -o $(BUILD_DIR)/$(CLI_NAME) ./cmd/ndagctl

# Run tests
test:
	@echo "Running tests..."
	$(GOTEST) -v -race -coverprofile=coverage.out ./...

# Run unit tests only
unit-test:
	@echo "Running unit tests..."
	$(GOTEST) -v -short ./...

# Run integration tests
integration-test:
	@echo "Running integration tests..."
	$(GOTEST) -v -run Integration ./...

# Run fuzz tests
fuzz:
	@echo "Running fuzz tests..."
	$(GOTEST) -fuzz=. -fuzztime=30s ./...

# Lint code
lint:
	@echo "Running linter..."
	@golangci-lint run ./... || echo "golangci-lint not found, skipping..."

# Format code
fmt:
	@echo "Formatting code..."
	$(GOFMT) -s -w .

# Clean build artifacts
clean:
	@echo "Cleaning..."
	@rm -rf $(BUILD_DIR)
	@rm -f coverage.out

# Download dependencies
deps:
	@echo "Downloading dependencies..."
	$(GOMOD) download
	$(GOMOD) tidy

# Run devnet
run-devnet: build
	@echo "Starting devnet..."
	./scripts/devnet.sh

# Create genesis file
genesis:
	@echo "Creating genesis file..."
	./$(BUILD_DIR)/$(CLI_NAME) genesis create --network devnet --output configs/genesis.pb

# Install dependencies and setup
setup: deps proto build
	@echo "Setup complete!"

# Docker build
docker:
	@echo "Building Docker images..."
	docker build -t ndagcoin/ndagd:latest -f docker/Dockerfile.ndagd .
	docker build -t ndagcoin/ndagctl:latest -f docker/Dockerfile.ndagctl .

# Generate documentation
docs:
	@echo "Generating documentation..."
	@mkdir -p docs/generated
	@echo "# NDAG Coin Documentation" > docs/generated/README.md

# Run security checks
security:
	@echo "Running security checks..."
	@gosec ./... || echo "gosec not found, install with: go install github.com/securecodewarrior/gosec/v2/cmd/gosec@latest"

# Show help
help:
	@echo "NDAG Coin Build System"
	@echo "======================"
	@echo ""
	@echo "Available targets:"
	@echo "  all          - Setup, generate protos, and build"
	@echo "  setup        - Install dependencies and build"
	@echo "  build        - Build ndagd and ndagctl binaries"
	@echo "  proto        - Generate protobuf files"
	@echo "  test         - Run all tests with coverage"
	@echo "  unit-test    - Run unit tests only"
	@echo "  integration-test - Run integration tests"
	@echo "  fuzz         - Run fuzz tests"
	@echo "  lint         - Run linter"
	@echo "  fmt          - Format code"
	@echo "  clean        - Clean build artifacts"
	@echo "  deps         - Download dependencies"
	@echo "  run-devnet   - Start local devnet"
	@echo "  genesis      - Create genesis file"
	@echo "  docker       - Build Docker images"
	@echo "  docs         - Generate documentation"
	@echo "  security     - Run security checks"
	@echo "  help         - Show this help"

.DEFAULT_GOAL := help