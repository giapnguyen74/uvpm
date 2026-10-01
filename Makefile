VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/giapnguyen74/uvpm/internal/version.Version=$(VERSION)

.PHONY: build test vet linux darwin dist
build:
	go build -ldflags "$(LDFLAGS)" -o bin/uvpm ./cmd/uvpm
test:
	go test ./...
vet:
	go vet ./...
linux:
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o bin/uvpm-linux-amd64 ./cmd/uvpm
	GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o bin/uvpm-linux-arm64 ./cmd/uvpm
darwin:
	GOOS=darwin GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o bin/uvpm-darwin-amd64 ./cmd/uvpm
	GOOS=darwin GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o bin/uvpm-darwin-arm64 ./cmd/uvpm
dist: linux darwin
