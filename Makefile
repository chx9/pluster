.PHONY: build run restart test test-unit test-integration test-integration-full lint clean _check-goroot

GO          := GOROOT=$(GOROOT) $(GOROOT)/bin/go

_check-goroot:
	@if [ -z "$$GOROOT" ] && [ ! -d "$(GOROOT)" ]; then \
		echo "WARNING: GOROOT is not set and default path $(GOROOT) does not exist."; \
		echo "  Set GOROOT to your Go installation, e.g.: make <target> GOROOT=/usr/local/go"; \
	fi

BIN         := bin/pluster
CLUSTER_ADDR ?= 127.0.0.1:7000
PORT        ?= 7777

build:
	mkdir -p bin
	$(GO) build -o $(BIN) ./cmd/pluster/

restart: build
	@pkill -x pluster 2>/dev/null || true
	@sleep 0.2
	$(BIN) --port $(PORT) $(CLUSTER_ADDR)

run: build
	@if lsof -ti :$(PORT) >/dev/null 2>&1; then \
		echo "ERROR: port $(PORT) already in use. Run 'make restart' to kill the old instance first."; \
		exit 1; \
	fi
	$(BIN) --port $(PORT) $(CLUSTER_ADDR)

test-unit:
	$(GO) test ./pkg/... -timeout 30s -count=10

test-integration:
	$(GO) test ./tests/... -timeout 1800s -count=10

test: test-unit test-integration

lint:
	$(GO) vet ./...

clean: _check-goroot
	$(GO) clean ./...
