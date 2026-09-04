.PHONY: all build proto clean run-server run-client test test-client test-edge test-integration test-cover help

# Variables
BINARY_DIR=bin
ifeq ($(OS),Windows_NT)
    BINARY_EXT=.exe
    CLEAN_CMD=powershell -Command "Remove-Item -Recurse -Force bin -ErrorAction Ignore; Remove-Item -Recurse -Force fileserver_data -ErrorAction Ignore"
else
    BINARY_EXT=
    CLEAN_CMD=rm -rf bin fileserver_data
endif
FILESERVER_BINARY=$(BINARY_DIR)/fileserver$(BINARY_EXT)
CLIENT_BINARY=$(BINARY_DIR)/client$(BINARY_EXT)
METASERVER_BINARY=$(BINARY_DIR)/metaserver$(BINARY_EXT)
ADMIN_BINARY=$(BINARY_DIR)/admin$(BINARY_EXT)
API_DIR=api
GO=go
PROTOC=protoc

all: build

# Build all binaries
build: certs
	@echo "Building file server..."
	@$(GO) build -o $(FILESERVER_BINARY) cmd/fileserver/main.go
	@echo "Building client..."
	@$(GO) build -o $(CLIENT_BINARY) cmd/client/main.go
	@echo "Building meta server..."
	@$(GO) build -o $(METASERVER_BINARY) cmd/metaserver/main.go
	@echo "Building admin server..."
	@$(GO) build -o $(ADMIN_BINARY) cmd/admin/main.go
	@echo "Build complete!"


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
	-@$(CLEAN_CMD)
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

# Run admin console
run-admin: build
	@echo "Starting admin console..."
	@./$(ADMIN_BINARY) -port=8080 -state_file=./metaserver_state.json

exec-admin:
	@./$(ADMIN_BINARY) -port=8080 -state_file=./metaserver_state.json

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

# Run admin console tests
test-admin:
	@echo "Running admin test suite..."
	@$(GO) test ./internal/admin -count=1 -v
	@echo "Admin tests complete!"

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
	@echo "  make certs        - Generate TLS certificates"
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
SERVER ?= localhost
certs:
	@echo "Generating TLS certificates..."
	@$(GO) run scripts/gen-certs/main.go $(SERVER)
	@echo "Certificates generated!"
