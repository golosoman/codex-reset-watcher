GO ?= go
GOLANGCI_LINT ?= golangci-lint
GOVULNCHECK ?= govulncheck

.PHONY: check fmt fmt-check test test-race vet lint vuln build run docker-build up down
check: fmt-check vet test-race lint vuln build
fmt:
	gofmt -w cmd internal
fmt-check:
	@test -z "$$(gofmt -l cmd internal)" || (gofmt -l cmd internal; exit 1)
test:
	$(GO) test ./...
test-race:
	$(GO) test -race -count=1 ./...
vet:
	$(GO) vet ./...
lint:
	$(GOLANGCI_LINT) run
vuln:
	$(GOVULNCHECK) ./...
build:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags='-s -w' -o bin/watcher ./cmd/watcher
run:
	$(GO) run ./cmd/watcher
docker-build:
	docker compose build
up:
	docker compose up -d
down:
	docker compose down
