LINT_VERSION := v2.13.2
BUF_VERSION := v1.73.0
PROTOC_GEN_GO_VERSION := v1.36.12
PROTOC_GEN_GO_GRPC_VERSION := v1.6.2

GOBIN := $(shell go env GOPATH)/bin
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
DIST := dist/querypro-$(VERSION)-$(shell go env GOOS)-$(shell go env GOARCH)
DEPS := plugins/node_modules/.package-lock.json

.PHONY: build run demo services test it lint fmt proto tools dist

build: $(DEPS)
	go build -ldflags "$(LDFLAGS)" -o bin/ ./...

run: $(DEPS)
	go run .

services:
	docker compose up -d --wait

demo: $(DEPS) services
	mkdir -p .demo && cp -n demo/connections.json .demo/ 2>/dev/null || true
	QUERYPRO_HOME=.demo go run .

test: $(DEPS)
	go test -race ./...
	cd plugins && npm test

it: $(DEPS)
	QUERYPRO_IT=1 go test -race -count=1 -run 'TestBackends|TestPostgresCancel' -v ./internal/plugin/

lint: $(DEPS)
	$(GOBIN)/golangci-lint run
	cd plugins && npm run check
	PATH=$(GOBIN):$$PATH buf lint

fmt:
	$(GOBIN)/golangci-lint fmt

proto:
	PATH=$(GOBIN):$$PATH buf generate

tools:
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(LINT_VERSION)
	go install github.com/bufbuild/buf/cmd/buf@$(BUF_VERSION)
	go install google.golang.org/protobuf/cmd/protoc-gen-go@$(PROTOC_GEN_GO_VERSION)
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@$(PROTOC_GEN_GO_GRPC_VERSION)

dist:
	rm -rf $(DIST) && mkdir -p $(DIST)
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/querypro .
	cp -R proto $(DIST)/proto
	rsync -a --exclude node_modules --exclude '*.test.ts' plugins/ $(DIST)/plugins/
	cd $(DIST)/plugins && npm ci --omit=dev --ignore-scripts --no-audit --no-fund
	tar -C dist -czf $(DIST).tar.gz $(notdir $(DIST))

$(DEPS): plugins/package-lock.json
	cd plugins && npm ci --no-audit --no-fund
