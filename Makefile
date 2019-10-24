# Ensure Make is run with bash shell as some syntax below is bash-specific
SHELL:=/usr/bin/env bash

.DEFAULT_GOAL:=help

# Use GOPROXY environment variable if set
GOPROXY := $(shell go env GOPROXY)
ifeq ($(GOPROXY),)
GOPROXY := https://proxy.golang.org
endif
export GOPROXY

# Active module mode, as we use go modules to manage dependencies
export GO111MODULE=on

TOOLS_DIR := hack/tools
TOOLS_BIN_DIR := $(TOOLS_DIR)/bin
BIN_DIR := bin

GOLANGCI_LINT := $(TOOLS_BIN_DIR)/golangci-lint


## --------------------------------------
## Help
## --------------------------------------

help:  ## Display this help
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)


## --------------------------------------
## Binaries
## --------------------------------------

.PHONY: bin
bin: ## Build binary.
	go build -o $(BIN_DIR)/cluster-api-upgrade-tool .

$(GOLANGCI_LINT): $(TOOLS_DIR)/go.mod # Build golangci-lint from tools folder.
	cd $(TOOLS_DIR); go build -tags=tools -o $(BIN_DIR)/golangci-lint github.com/golangci/golangci-lint/cmd/golangci-lint


## --------------------------------------
## Linting
## --------------------------------------

.PHONY: lint
lint: $(GOLANGCI_LINT) ## Lint codebase
	$(GOLANGCI_LINT) run -v

lint-full: $(GOLANGCI_LINT) ## Run slower linters to detect possible issues
	$(GOLANGCI_LINT) run -v --fast=false


## --------------------------------------
## Testing
## --------------------------------------

.PHONY: test
test:  ## Run unit tests
	go test ./...

.PHONY: integration-test
integration-test: ## Run integration tests
	cd test/integration; go test -count=1 -ginkgo.v -v -timeout=20m $(TEST_ARGS)


## --------------------------------------
## Docker
## --------------------------------------

IMAGE ?= cluster-api-upgrade-tool

.PHONY: docker-build
docker-build: ## Build the docker image
	docker build --pull . -t $(IMAGE)


## --------------------------------------
## Release
## --------------------------------------

RELEASE_TAG := $(shell git describe --abbrev=0 2>/dev/null)
RELEASE_DIR := out
RELEASE_BINARY := cluster-api-upgrade-tool

$(RELEASE_DIR):
	mkdir -p $(RELEASE_DIR)/

.PHONY: release
release: clean-release  ## Builds and push container images using the latest git tag for the commit.
	@if [ -z "${RELEASE_TAG}" ]; then echo "RELEASE_TAG is not set"; exit 1; fi
	$(MAKE) release-binaries

.PHONY: release-binaries
release-binaries: ## Builds the binaries to publish with a release
	GOOS=linux GOARCH=amd64 $(MAKE) release-binary
	GOOS=darwin GOARCH=amd64 $(MAKE) release-binary

.PHONY: release-binary
release-binary: $(RELEASE_DIR)
	docker run \
		--rm \
		-e CGO_ENABLED=0 \
		-e GOOS=$(GOOS) \
		-e GOARCH=$(GOARCH) \
		-v "$$(pwd):/workspace" \
		-w /workspace \
		golang:1.12.9 \
		go build -a -ldflags '-extldflags "-static"' \
		-o $(RELEASE_DIR)/$(notdir $(RELEASE_BINARY))-$(GOOS)-$(GOARCH) $(RELEASE_BINARY)


## --------------------------------------
## Generate
## --------------------------------------

.PHONY: modules
modules: ## Runs go mod to ensure proper vendoring.
	go mod tidy
	cd $(TOOLS_DIR); go mod tidy
	cd test/integration; go mod tidy


## --------------------------------------
## Cleanup / Verification
## --------------------------------------

.PHONY: clean
clean: ## Remove all generated files
	$(MAKE) clean-bin

.PHONY: clean-bin
clean-bin: ## Remove all generated binaries
	rm -rf bin
	rm -rf hack/tools/bin

.PHONY: verify
verify: verify-modules

.PHONY: verify-modules
verify-modules: modules
	@if !(git diff --quiet HEAD -- go.sum go.mod hack/tools/go.mod hack/tools/go.sum); then \
		echo "go module files are out of date"; exit 1; \
	fi

.PHONY: clean-release
clean-release: ## Remove the release folder
	rm -rf $(RELEASE_DIR)
