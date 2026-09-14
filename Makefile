BINARY     := vivarium
PKG        := ./cmd/vivarium
GUESTBRIDGE := internal/guestbridge/bin/vivarium-guestbridge
GO         ?= go

.PHONY: all build guestbridge run test test-integration race vet fmt tidy clean

all: build

## guestbridge: build the embedded in-guest TCP relay (committed)
guestbridge:
	GOOS=linux GOARCH=$(shell $(GO) env GOARCH) CGO_ENABLED=0 $(GO) build -trimpath -ldflags="-s -w" -o $(GUESTBRIDGE) ./cmd/vivarium-guestbridge

## build: compile the guest relay, then the single vivarium binary
build: guestbridge
	$(GO) build -o $(BINARY) $(PKG)

## run: build and launch the TUI
run: build
	./$(BINARY)

## test: run unit tests
test:
	$(GO) test ./...

## test-integration: run real-Docker integration tests (network, GPU, proxy, agent e2e)
test-integration:
	VIVARIUM_INTEGRATION=1 $(GO) test -tags=integration ./internal/docker/ ./internal/proxy/ ./internal/keyring/ ./internal/e2e/

## race: run unit tests with the race detector
race:
	$(GO) test -race ./internal/...

## vet: run go vet including integration-tagged files
vet:
	$(GO) vet ./...
	$(GO) vet -tags=integration ./...

## fmt: format all Go sources
fmt:
	gofmt -w .

## tidy: synchronise go.mod/go.sum
tidy:
	$(GO) mod tidy

## clean: remove the built binary
clean:
	rm -f $(BINARY)
