LINT_VERSION := v2.13.2
GOLANGCI_LINT := $(shell go env GOPATH)/bin/golangci-lint

.PHONY: build run test lint fmt lint-install

build:
	go build -o bin/ ./...

run:
	go run .

test:
	go test -race ./...

lint:
	$(GOLANGCI_LINT) run

fmt:
	$(GOLANGCI_LINT) fmt

lint-install:
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(LINT_VERSION)
