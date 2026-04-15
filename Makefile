.PHONY: all build proto clean run-server run-client test test-client test-edge test-integration test-cover help

# Variables
BINARY_DIR=bin
FILESERVER_BINARY=$(BINARY_DIR)/fileserver
CLIENT_BINARY=$(BINARY_DIR)/client
METASERVER_BINARY=$(BINARY_DIR)/metaserver
API_DIR=api
GO=go
PROTOC=protoc
CERT_DIR=internal/certs
MDS_IP ?= 127.0.0.1

all: build

# Build all binaries
build: certs $(BINARY_DIR)
	@echo "Building file server..."
	@$(GO) build -o $(FILESERVER_BINARY) cmd/fileserver/main.go
	@echo "Building client..."
	@$(GO) build -o $(CLIENT_BINARY) cmd/client/main.go
	@echo "Building meta server..."
	@$(GO) build -o $(METASERVER_BINARY) cmd/metaserver/main.go
	@echo "Build complete!"

# Create binary directory
$(BINARY_DIR):
	@mkdir -p $(BINARY_DIR)

# Generate protobuf code
proto:
	@echo "Generating protobuf code..."
	@$(PROTOC) --go_out=. --go_opt=paths=source_relative \
		--go-grpc_out=. --go-grpc_opt=paths=source_relative \
		$(API_DIR)/fileserver/fileserver.proto
	@$(PROTOC) --go_out=. --go_opt=paths=source_relative \
		--go-grpc_out=. --go-grpc_opt=paths=source_relative \
		$(API_DIR)/callback/callback.proto
	@$(PROTOC) --go_out=. --go_opt=paths=source_relative \
		--go-grpc_out=. --go-grpc_opt=paths=source_relative \
		$(API_DIR)/metaserver/metaserver.proto
	@echo "Protobuf generation complete!"

# Clean build artifacts
clean:
	@echo "Cleaning build artifacts..."
	@rm -rf $(BINARY_DIR)
	@rm -rf fileserver_data
	@echo "Clean complete!"

# Run file server
run-server: build
	@echo "Starting file server..."
	@./$(FILESERVER_BINARY) -id=fs1 -port=50051 -data=./fileserver_data

exec-server:
	@./$(FILESERVER_BINARY) -id=fs1 -port=50051 -data=./fileserver_data

# Run meta server
run-metaserver: build
	@echo "Starting meta server..."
	@./$(METASERVER_BINARY) -port=50052

exec-metaserver:
	@./$(METASERVER_BINARY) -port=50052

# Run client (usage: make run-client USER=alice IP_ADDR=127.0.0.1)
USER ?= alice
IP_ADDR ?= 127.0.0.1
run-client: build
	@echo "Starting client for user $(USER) connecting to $(IP_ADDR)..."
	@./$(CLIENT_BINARY) -username=$(USER) -ip_addr=$(IP_ADDR)

exec-client:
	@./$(CLIENT_BINARY) -username=$(USER) -ip_addr=$(IP_ADDR)

# Install dependencies
deps:
	@echo "Installing dependencies..."
	@$(GO) mod download
	@$(GO) mod tidy
	@echo "Dependencies installed!"

# Format code
fmt:
	@echo "Formatting code..."
	@$(GO) fmt ./...
	@echo "Format complete!"

# Run go vet
vet:
	@echo "Running go vet..."
	@$(GO) vet ./...
	@echo "Vet complete!"

# Run unit tests
test: certs
	@echo "Running unit tests..."
	@$(GO) test ./... -count=1 -v
	@echo "Tests complete!"

# Run client-focused tests
test-client: certs
	@echo "Running client test suite..."
	@$(GO) test ./internal/client -count=1 -v
	@echo "Client tests complete!"

# Run edge-case tests
test-edge: certs
	@echo "Running edge-case test suite..."
	@$(GO) test ./internal/fileserver ./internal/metaserver -count=1 -v
	@echo "Edge-case tests complete!"

# Run integration and end-to-end tests
test-integration: certs
	@echo "Running integration/e2e test suite..."
	@$(GO) test ./integration -count=1 -v
	@echo "Integration tests complete!"

# Run unit tests with coverage
test-cover: certs
	@echo "Running unit tests with coverage..."
	@$(GO) test ./... -count=1 -coverprofile=coverage.out -covermode=atomic
	@$(GO) tool cover -func=coverage.out
	@echo "Coverage report written to coverage.out"

# Help
help:
	@echo "Available targets:"
	@echo "  make build        - Build all binaries"
	@echo "  make proto        - Generate protobuf code"
	@echo "  make certs        - Generate TLS certificates under $(CERT_DIR)"
	@echo "  make certs MDS_IP=192.168.1.10 - Generate certs with SAN for given MetaServer IP"
	@echo "  make clean        - Remove build artifacts"
	@echo "  make run-server   - Build and run file server"
	@echo "  make run-client   - Build and run client (default user: alice)"
	@echo "  make run-client USER=bob - Run client with specific user"
	@echo "  make deps         - Install and tidy dependencies"
	@echo "  make fmt          - Format code"
	@echo "  make vet          - Run go vet"
	@echo "  make test         - Run unit tests"
	@echo "  make test-client  - Run client test suite"
	@echo "  make test-edge    - Run edge-case test suite"
	@echo "  make test-integration - Run integration/e2e tests"
	@echo "  make test-cover   - Run unit tests with coverage"
	@echo "  make help         - Show this help message"

# Generate TLS certificates
certs:
	@echo "Generating CA and base server certificate for host $(MDS_IP)..."
	@go run scripts/gen-certs/main.go $(MDS_IP)
	@powershell -NoProfile -Command "Copy-Item -Force '$(CERT_DIR)/server.crt' '$(CERT_DIR)/metaserver.crt'; Copy-Item -Force '$(CERT_DIR)/server.key' '$(CERT_DIR)/metaserver.key'; Copy-Item -Force '$(CERT_DIR)/server.crt' '$(CERT_DIR)/fileserver.crt'; Copy-Item -Force '$(CERT_DIR)/server.key' '$(CERT_DIR)/fileserver.key'"
	@echo "Certificates generated in $(CERT_DIR):"
	@echo "  ca.crt, ca.key"
	@echo "  metaserver.crt, metaserver.key"
	@echo "  fileserver.crt, fileserver.key"
	@echo "Note: fileserver cert/key are bootstrap material; in TLS mode, FileServer auto-requests/renews its serving cert from MetaServer."
