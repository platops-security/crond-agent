VERSION ?= dev
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS = -s -w -X main.Version=$(VERSION) -X main.Commit=$(COMMIT) -X main.BuildDate=$(DATE)

.PHONY: build test release release-local clean

build:
	CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o bin/crond-agent ./cmd/crond-agent

test:
	go test -v -race ./...

release-local:
	goreleaser release --snapshot --clean

release:
	GOOS=linux   GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o bin/crond-agent-linux-amd64  ./cmd/crond-agent
	GOOS=linux   GOARCH=arm64 CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o bin/crond-agent-linux-arm64  ./cmd/crond-agent
	GOOS=darwin  GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o bin/crond-agent-darwin-amd64 ./cmd/crond-agent
	GOOS=darwin  GOARCH=arm64 CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o bin/crond-agent-darwin-arm64 ./cmd/crond-agent

clean:
	rm -rf bin/ dist/
