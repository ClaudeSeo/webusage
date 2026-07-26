.PHONY: build test test-race run dev clean deps fmt lint coverage build-prod

# Build binary
build:
	mise exec -- go build -o webusage ./cmd/server

# Run all tests
test:
	mise exec -- go test ./... -v

# Run tests with race detector
test-race:
	mise exec -- go test ./... -race -v

# Run server
run:
	./webusage

# Development mode
dev:
	mise exec -- go run ./cmd/server

# Clean build artifacts and data
clean:
	rm -f webusage webusage-linux webusage-macos
	rm -rf data/*.db data/*.db-wal data/*.db-shm
	mise exec -- go clean -cache

# Download and tidy dependencies
deps:
	mise exec -- go mod download
	mise exec -- go mod tidy

# Format code
fmt:
	mise exec -- go fmt ./...

# Run lint
lint:
	golangci-lint run ./...

# Generate coverage report
coverage:
	mise exec -- go test ./... -coverprofile=coverage.out
	mise exec -- go tool cover -html=coverage.out -o coverage.html

# Production build (Linux/macOS)
build-prod:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 mise exec -- go build -ldflags="-s -w" -o webusage-linux ./cmd/server
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 mise exec -- go build -ldflags="-s -w" -o webusage-macos ./cmd/server
