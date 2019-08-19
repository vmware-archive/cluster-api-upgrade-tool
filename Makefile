TOOLS_DIR := hack/tools
CAPDCTL_BIN := bin/capdctl
CAPDCTL := $(TOOLS_DIR)/$(CAPDCTL_BIN)

TESTCASE ?=
TEST_ARGS =

ifdef TESTCASE
override TEST_ARGS = -run $(TESTCASE)
endif

.PHONY: test
test: $(CAPDCTL)
	go test -count=1 -v -timeout=20m $(TEST_ARGS) .

# Build capdctl
$(CAPDCTL): $(TOOLS_DIR)/go.mod
	cd $(TOOLS_DIR) && go build -o $(CAPDCTL_BIN) sigs.k8s.io/cluster-api-provider-docker/cmd/capdctl
