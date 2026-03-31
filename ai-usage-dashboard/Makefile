.PHONY: build test run clean dev

# Build the binary
build:
	go build -o ai-usage-dashboard ./cmd/server

# Run all tests
test:
	go test ./... -v

# Run with race detector
test-race:
	go test ./... -race -v

# Run the server
run:
	./ai-usage-dashboard

# Development mode (rebuild on changes)
dev:
	@echo "Install air for hot reload: go install github.com/air-verse/air@latest"
	air -c .air.toml 2>/dev/null || go run ./cmd/server

# Clean build artifacts
clean:
	rm -f ai-usage-dashboard
	rm -rf data/*.db data/*.db-wal data/*.db-shm
	go clean -cache

# Install dependencies
deps:
	go mod download
	go mod tidy

# Format code
fmt:
	go fmt ./...

# Lint code
lint:
	golangci-lint run ./...

# Generate coverage report
coverage:
	go test ./... -coverprofile=coverage.out
	go tool cover -html=coverage.out -o coverage.html

# Build for production
build-prod:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o ai-usage-dashboard-linux ./cmd/server
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w" -o ai-usage-dashboard-macos ./cmd/server
