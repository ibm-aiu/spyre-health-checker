# +-------------------------------------------------------------------+
# | (C) Copyright IBM Corp. 2025, 2026                                |
# | SPDX-License-Identifier: Apache-2.0                               |
# +-------------------------------------------------------------------+

# Enable automatic Go toolchain management
export GOTOOLCHAIN = auto

GOLANG_VERSION		?= $(shell cd $(REPO_ROOT) && go list -f {{.GoVersion}} -m)
BUILDER_IMAGE		?= registry.access.redhat.com/ubi9/go-toolset:9.6-1754467841
GOTOOLCHAIN			?= go$(GOLANG_VERSION)
MAKEFILE_PATH		:= $(abspath $(lastword $(MAKEFILE_LIST)))
REPO_ROOT			:= $(abspath $(patsubst %/,%,$(dir $(MAKEFILE_PATH))))
CURRENT_DIR			:= $(shell pwd)
VERSION				?= $(shell $(REPO_ROOT)/VERSION)
REGISTRY			?= docker.io/spyre-operator
DOCKER				?= $(shell command -v podman 2> /dev/null || echo docker)
DOCKERFILE			= $(REPO_ROOT)/Dockerfile
DOCKER_BUILD_OPTS	?= --progress=plain

IMAGE_NAME 			:= $(REGISTRY)/spyre-health-checker
IMAGE_TAG 			?= $(VERSION)
IMAGE 				?= $(IMAGE_NAME):$(IMAGE_TAG)
TEST_IMG			?= $(IMAGE_NAME):dev
CODECOV_PERCENT		?= 45
GOCOVERDIR			?= $(REPO_ROOT)

KUBECTL				?= $(shell command -v oc 2> /dev/null || echo kubectl)
OC					?= $(shell command -v oc)

# Operating system
OS					?= $(shell go env GOOS)
ARCH				?= $(shell go env GOARCH)
LDFLAGS				=

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# Setting SHELL to bash allows bash commands to be executed by recipes.
# This is a requirement for 'setup-envtest.sh' in the test target.
# Options are set to exit when a recipe line exits non-zero or a piped command fails.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

## Tool Binaries
PYTHON			?= python3
PIP				?= pip3
ENVTEST			?= $(LOCALBIN)/setup-envtest
GOLANGCI_LINT	?= $(LOCALBIN)/golangci-lint
GOVULCHECK		?= $(LOCALBIN)/govulncheck
GINKGO			?= $(LOCALBIN)/ginkgo
YQ				?= $(LOCALBIN)/yq


## Tool Versions
ENVTEST_K8S_VERSION		?= 1.33
GOLANGCI_LINT_VERSION	?= 2.11.4
GINKGO_VERSION			?= v2.28.1
YQ_VERSION				?= v4.29.2

# Shamelessly copied from: https://github.com/opendatahub-io/opendatahub-operator/blob/a08c94a226585e43387ad263e2653c0fd43130f1/Makefile#L132C1-L139C1
define go-mod-version
$(shell go mod graph | grep $(1) 2>/dev/null | head -n 1 | cut -d'@' -f 2)
endef

# detect-secrets
DETECT_SECRETS_GIT ?= "https://github.com/ibm/detect-secrets.git@master\#egg=detect-secrets"

DOCKER_GO_BUILD_FLAGS ?= -race

##@ General

# The help target prints out all targets with their descriptions organized
# beneath their categories. The categories are represented by '##@' and the
# target descriptions by '##'. The awk commands is responsible for reading the
# entire set of makefiles included in this invocation, looking for lines of the
# file as xyz: ## something, and then pretty-format the target and help. Then,
# if there's a line with ##@ something, that gets pretty-printed as a category.
# More info on the usage of ANSI control characters for terminal formatting:
# https://en.wikipedia.org/wiki/ANSI_escape_code#SGR_parameters
# More info on the awk command:
# http://linuxcommand.org/lc3_adv_awk.php

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-25s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)


##@ Development tools
.PHONY: ginkgo
ginkgo: $(GINKGO) ## Download and install ginkgo
$(GINKGO):$(LOCALBIN)
	GOBIN=$(LOCALBIN) go install github.com/onsi/ginkgo/v2/ginkgo@$(GINKGO_VERSION)

.PHONY: envtest
envtest: $(ENVTEST) ## Download and install setup-envtest
$(ENVTEST):$(LOCALBIN)
	GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-runtime/tools/setup-envtest@v0.0.0-20240624150636-162a113134de

GOLANGCI_LINT_INSTALL_SCRIPT ?= 'https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh'
.PHONY: golangci-lint
golangci-lint: $(GOLANGCI_LINT) ### Download golangci-lint locally if necessary.
$(GOLANGCI_LINT):$(LOCALBIN)
	test -s $(GOLANGCI_LINT) || { curl -sSfL $(GOLANGCI_LINT_INSTALL_SCRIPT) | sh -s -- -b $(LOCALBIN)  v$(GOLANGCI_LINT_VERSION); }

.PHONY: yq
yq: $(YQ) ## Download yq locally if necessary.
$(YQ): $(LOCALBIN)
	test -s $(YQ) || GOBIN=$(LOCALBIN) go install github.com/mikefarah/yq/v4@$(YQ_VERSION)

.PHONY: govulncheck
govulncheck: $(GOVULCHECK) ## Download govulncheck tool if necessary
$(GOVULCHECK): $(LOCALBIN)
	GOBIN=$(LOCALBIN) go install golang.org/x/vuln/cmd/govulncheck@latest

##@ protoc targets

.PHONY: protoc-gen
protoc-gen: ## Generated files from proto
	go tool buf generate

.PHONY: venv
venv: ## Setup and activate venv
	$(PYTHON) -m venv venv

PHONY: clean
clean: ## Clean-up intermediate artifacts
	-rm -rf $(LOCALBIN)
	-rm -rf local.mk

##@ Test targets

COVERAGE_FILE := coverage.out

.PHONY: test
test: envtest ginkgo vendor checks ## Run unit tests
	$(CGO_FLAGS) $(GINKGO) -r --cover --coverprofile=$(COVERAGE_FILE) --race --json-report unittest-report.json -v ./...
	go tool cover -func $(COVERAGE_FILE)
	go tool cover -html $(COVERAGE_FILE) -o coverage-report.html
	@percentage=$$(go tool cover -func=$(COVERAGE_FILE) | grep ^total | awk '{print $$3}' | tr -d '%'); \
		if (( $$(echo "$$percentage < $(CODECOV_PERCENT)" | bc -l) )); then \
			echo "----------"; \
			echo "Total test coverage ($${percentage}%) is less than the coverage threshold ($(CODECOV_PERCENT)%)."; \
			exit 1; \
		else \
			echo "Total test coverage ($${percentage}%) is more than the coverage threshold ($(CODECOV_PERCENT)%)."; \
		fi

##@ Development Targets

.PHONY: fmt
fmt: ## Run the formatter
	go fmt ./...

.PHONY: vet
vet: vendor ## Run the vet command
	$(CGO_FLAGS) go vet -mod vendor ./...

.PHONY: vendor
vendor: ## Run vendor
	go mod vendor

.PHONY: build
build: vendor ## Build local binary
	$(CGO_FLAGS) go build -mod vendor $(LDFLAGS) -race -o $(LOCALBIN)/spyre-health-checker ./cmd/health-checker

.PHONY: lint
lint: golangci-lint vendor  ## Run golangci-lint against code.
	$(GOLANGCI_LINT) run --config $(REPO_ROOT)/.golangci.yaml

.PHONY: checks
checks: fmt vet lint # Run fmt vet lint

.PHONY: lint-fix
lint-fix: golangci-lint vendor ## Run golangci-lint against code.
	$(GOLANGCI_LINT) run --fix --config $(REPO_ROOT)/.golangci.yaml

.PHONY: vulcheck
vulcheck: govulncheck ## Scan for golang vulnerabilities
	$(CGO_FLAGS) $(GOVULCHECK) -show verbose	 ./...

##@ Image operations

.PHONY: docker-build
docker-build: vendor ## Build spyre health checker image for build host architecture
	$(DOCKER) build $(DOCKER_BUILD_OPTS) --pull \
	--tag $(IMAGE) \
	--build-arg VERSION="$(VERSION)" \
	--build-arg BUILDER_IMAGE="$(BUILDER_IMAGE)" \
	--build-arg BUILD_FLAGS="$(DOCKER_GO_BUILD_FLAGS)" \
	--file $(DOCKERFILE) $(CURDIR)

.PHONY: docker-push
docker-push: ## Push spyre health checker image for the build host architecture.
	$(DOCKER) push $(IMAGE)

.PHONY: docker-build-push
docker-build-push: docker-build docker-push ## Build and push the spyre health checker image for the build host

##@ Release targets

.PHONY: version
version: ## Display image version
	@echo "Image version: $(VERSION)"

.PHONY: echo-version
echo-version: ## Print (echo) the current version
	$(info $(VERSION))
	@echo > /dev/null

##@ Secret detection targets

.PHONY: detect-secrets-install
detect-secrets-install: venv ## Install detect-secret tool
	. venv/bin/activate; $(PIP) install "git+$(DETECT_SECRETS_GIT)"

.PHONY: secrets-scan
secrets-scan: detect-secrets-install venv ## Scan secrets and create secret-baseline for repo
	. venv/bin/activate; detect-secrets scan --exclude-files go.sum --update .secrets.baseline --no-ghe-scan

.PHONY: secrets-audit
secrets-audit: detect-secrets-install venv ## Audit secrets
	. venv/bin/activate; detect-secrets audit .secrets.baseline

# helper target for viewing the value of makefile variables.
print-%  : ;@echo $* = $($*)
