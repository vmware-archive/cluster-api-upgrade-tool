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
BIN_DIR := bin
CAPDCTL_BIN := bin/capdctl
CAPDCTL := $(TOOLS_DIR)/$(CAPDCTL_BIN)


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


## --------------------------------------
## Testing
## --------------------------------------

TESTCASE ?=
TEST_ARGS =

ifdef TESTCASE
override TEST_ARGS = -run $(TESTCASE)
endif

# TODO(ncdc): move main_test.go to test/integration/... and make sure it has an -integration tag so we can just do
# go test ./... for the test target.
.PHONY: test
test:  ## Run unit tests
	go test ./pkg/...

.PHONY: integration-test
integration-test: $(CAPDCTL) ## Run integration tests
	go test -count=1 -v -timeout=20m $(TEST_ARGS) .

# Build capdctl
$(CAPDCTL): $(TOOLS_DIR)/go.mod ## Build capdctl
	cd $(TOOLS_DIR) && go build -o $(CAPDCTL_BIN) sigs.k8s.io/cluster-api-provider-docker/cmd/capdctl


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
