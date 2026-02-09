.PHONY: all build proto clean run-server run-client help

# Variables
BINARY_DIR=bin
FILESERVER_BINARY=$(BINARY_DIR)/fileserver
CLIENT_BINARY=$(BINARY_DIR)/client
API_DIR=api
GO=go
PROTOC=protoc

all: build

# Build all binaries
build: $(BINARY_DIR)
	@echo "Building file server..."
	@$(GO) build -o $(FILESERVER_BINARY) cmd/fileserver/main.go
	@echo "Building client..."
	@$(GO) build -o $(CLIENT_BINARY) cmd/client/main.go
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

# Run client (usage: make run-client USER=alice)
USER ?= alice
run-client: build
	@echo "Starting client for user $(USER)..."
	@./$(CLIENT_BINARY) -username=$(USER)

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

# Help
help:
	@echo "Available targets:"
	@echo "  make build        - Build all binaries"
	@echo "  make proto        - Generate protobuf code"
	@echo "  make clean        - Remove build artifacts"
	@echo "  make run-server   - Build and run file server"
	@echo "  make run-client   - Build and run client (default user: alice)"
	@echo "  make run-client USER=bob - Run client with specific user"
	@echo "  make deps         - Install and tidy dependencies"
	@echo "  make fmt          - Format code"
	@echo "  make vet          - Run go vet"
	@echo "  make help         - Show this help message"
