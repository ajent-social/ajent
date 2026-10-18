VERSION ?= dev
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
PKG     := github.com/ajent-social/ajent/internal/version
LDFLAGS := -s -w -X $(PKG).Version=$(VERSION) -X $(PKG).Commit=$(COMMIT) -X $(PKG).Date=$(DATE)

.PHONY: fmt lint test build docs
fmt:
	gofmt -w .
lint:
	test -z "$$(gofmt -l .)"
	go vet ./...
	golangci-lint run
test:
	go test -race -count=1 ./...
build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o ajent ./cmd/ajent
docs:
	go run ./cmd/ajent gendocs docs
