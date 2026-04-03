.PHONY: build test test-race run dev clean deps fmt lint coverage build-prod

# Binary 빌드
build:
	mise exec -- go build -o webusage ./cmd/server

# 전체 테스트 실행
test:
	mise exec -- go test ./... -v

# Race detector 포함 테스트
test-race:
	mise exec -- go test ./... -race -v

# 서버 실행
run:
	./webusage

# 개발 모드
dev:
	mise exec -- go run ./cmd/server

# 빌드 산출물 및 데이터 정리
clean:
	rm -f webusage webusage-linux webusage-macos
	rm -rf data/*.db data/*.db-wal data/*.db-shm
	mise exec -- go clean -cache

# 의존성 다운로드 및 정리
deps:
	mise exec -- go mod download
	mise exec -- go mod tidy

# 코드 포맷
fmt:
	mise exec -- go fmt ./...

# Lint 실행
lint:
	golangci-lint run ./...

# 커버리지 리포트 생성
coverage:
	mise exec -- go test ./... -coverprofile=coverage.out
	mise exec -- go tool cover -html=coverage.out -o coverage.html

# 프로덕션 빌드 (Linux/macOS)
build-prod:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 mise exec -- go build -ldflags="-s -w" -o webusage-linux ./cmd/server
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 mise exec -- go build -ldflags="-s -w" -o webusage-macos ./cmd/server
