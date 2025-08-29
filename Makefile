# Copyright (c) 2022 IBM Corp. All rights reserved.
# SPDX-License-Identifier: Apache-2.0

GOLANG_VERSION		?= $(shell cd $(REPO_ROOT) && go list -f {{.GoVersion}} -m)
BUILDER_IMAGE		?= registry.access.redhat.com/ubi9/go-toolset:9.6-1754467841
MAKEFILE_PATH		:= $(abspath $(lastword $(MAKEFILE_LIST)))
REPO_ROOT 			:= $(abspath $(patsubst %/,%,$(dir $(MAKEFILE_PATH))))
CURRENT_DIR			:= $(shell pwd)
VERSION				= $(shell $(REPO_ROOT)/hack/get-version.bash $(shell cat $(REPO_ROOT)/VERSION))
REGISTRY			?= icr.io
NAMESPACE			?= ibmaiu_internal
DOCKER				?= $(shell command -v podman 2> /dev/null || echo docker)
DOCKERFILE			= $(REPO_ROOT)/Dockerfile
DOCKER_BUILD_OPTS	?= --progress=plain

IMAGE_NAME 			:= $(REGISTRY)/$(NAMESPACE)/spyre-health-checker
IMAGE_TAG 			?= $(VERSION)
IMAGE 				?= $(IMAGE_NAME):$(IMAGE_TAG)
TEST_IMG			?= $(IMAGE_NAME):dev

KUBECTL              ?= $(shell command -v oc 2> /dev/null || echo kubectl)
OC                   ?= $(shell command -v oc)

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
ENVTEST			?= $(LOCALBIN)/setup-envtest
GOLANGCI_LINT	?= $(LOCALBIN)/golangci-lint
GOVULCHECK		?= $(LOCALBIN)/govulncheck
GINKGO			?= $(LOCALBIN)/ginkgo
YQ				?= $(LOCALBIN)/yq
PROTOC_GEN		?= $(LOCALBIN)/protoc

## Tool Versions
ENVTEST_K8S_VERSION			?= 1.31
GOLANGCI_LINT_VERSION 		?= 1.64.8
GINKGO_VERSION 				?= v2.25.1
YQ_VERSION 					?= v4.29.2
PROTOC_GEN_VERSION			?= v1.5.1

# Shamesly copied from: https://github.com/opendatahub-io/opendatahub-operator/blob/a08c94a226585e43387ad263e2653c0fd43130f1/Makefile#L132C1-L139C1
define go-mod-version
$(shell go mod graph | grep $(1) | head -n 1 | cut -d'@' -f 2)
endef

DOCKER_GO_BUILD_FLAGS ?=
BUILD_TYPE = $(shell $(REPO_ROOT)/hack/get-build-type.bash)
ifeq ($(strip $(BUILD_TYPE)), pr)
DOCKER_GO_BUILD_FLAGS += -race
endif

ifeq (release , $(BUILD_TYPE))
ADDITIONAL_IMAGE_TAG := stable
else ifeq (development, $(BUILD_TYPE))
ADDITIONAL_IMAGE_TAG := fast
else ifneq (, $(strip $(CHANGE_ID)))
ADDITIONAL_IMAGE_TAG := PR-$(CHANGE_ID)
else
ADDITIONAL_IMAGE_TAG := latest-pr
endif

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

.PHONY: all
all: build ## Build all defined targets


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

.PHONY: protoc-gen-install
protoc-gen-install: $(PROTOC_GEN) ## Download and install protoc
$(PROTOC_GEN): $(LOCALBIN)
	GOBIN=$(LOCALBIN) go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@$(PROTOC_GEN_VERSION)

.PHONY: protoc-gen
protoc-gen: protoc-gen-install
	protoc --go_out=. --go-grpc_out=. pkg/proto/spyre_health/spyre_health.proto

##@ Test targets

.PHONY: test
test: envtest ginkgo vendor checks ## Run unit tests
	$(CGO_FLAGS) $(GINKGO) -r --cover --coverprofile=coverage-report.out --race --json-report unittest-report.json -v ./...
	./hack/convert-to-markdown.sh unittest-report "Unit Tests"

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
	$(CGO_FLAGS) $(GOLANGCI_LINT) run --sort-results --config $(REPO_ROOT)/.golangci.yaml --go $(GOLANG_VERSION)

.PHONY: checks
checks: fmt vet lint # Run fmt vet lint

.PHONY: lint-fix
lint-fix: golangci-lint vendor ## Run golangci-lint against code.
	$(CGO_FLAGS) $(GOLANGCI_LINT) run --fix --config $(REPO_ROOT)/.golangci.yaml --go $(GOLANG_VERSION)

.PHONY: vulcheck
vulcheck: govulncheck ## Scan for golang vulnerabilities
	$(CGO_FLAGS) $(GOVULCHECK) -show verbose	 ./...

.PHONY: clean
clean: ## Clean-up intermediate artifacts
	-rm -rf vendor
	-rm -rf $(LOCALBIN)

.PHONY: pr
pr: vendor fmt vet lint test docker-build docker-push ## Execute a pull request build

.PHONY: release
release: vendor fmt vet lint test docker-buildx docker-pushx release-tag-push ## Execute release build

.PHONY: development
development: vendor fmt vet lint test docker-buildx docker-pushx ## Execute a main branch build (same as release build)


##@ Image operations

.PHONY: docker-build
docker-build: vendor ## Build spyre health checker image for build host architecture
	$(DOCKER) build $(DOCKER_BUILD_OPTS) --pull \
	--tag $(IMAGE) \
	--tag $(IMAGE_NAME):$(ADDITIONAL_IMAGE_TAG) \
	--build-arg VERSION="$(VERSION)" \
	--build-arg BUILDER_IMAGE="$(BUILDER_IMAGE)" \
	--build-arg BUILD_FLAGS="$(DOCKER_GO_BUILD_FLAGS)" \
	--file $(DOCKERFILE) $(CURDIR)

.PHONY: docker-push
docker-push: ## Push spyre health checker image for the build host architecture.
	$(DOCKER) push $(IMAGE)

.PHONY: docker-build-push
docker-build-push: docker-build docker-push ## Build and push the spyre health checker image for the build host

.PHONY: docker-build-amd64
docker-build-amd64: vendor ## Build amd64 spyre health checker image
ifeq ($(DOCKER),docker)
	docker build --platform linux/amd64 \
		--push --pull  --no-cache \
		$(DOCKER_BUILD_OPTS) \
		--tag $(IMAGE)-amd64 \
		--tag $(IMAGE_NAME):$(ADDITIONAL_IMAGE_TAG)-amd64 \
		--build-arg VERSION="$(VERSION)" \
		--build-arg BUILDER_IMAGE="$(BUILDER_IMAGE)" \
		--build-arg BUILD_FLAGS="$(DOCKER_GO_BUILD_FLAGS)" \
		--file $(DOCKERFILE) $(CURDIR)
else
	podman build --platform linux/amd64 \
		$(DOCKER_BUILD_OPTS) \
		--build-arg VERSION="$(VERSION)" \
		--build-arg BUILD_FLAGS="$(DOCKER_GO_BUILD_FLAGS)" \
		--build-arg BUILDER_IMAGE="$(BUILDER_IMAGE)" \
		--tag $(IMAGE)-amd64 \
		--tag $(IMAGE_NAME):$(ADDITIONAL_IMAGE_TAG)-amd64 \
		--file $(DOCKERFILE) $(CURDIR)
endif

.PHONY: docker-build-s390x
docker-build-s390x: vendor ## Build s390x spyre health checker image
ifeq ($(DOCKER),docker)
	docker buildx build --platform linux/s390x \
		--push --pull  --no-cache \
		$(DOCKER_BUILD_OPTS) \
		--tag $(IMAGE)-s390x \
		--tag $(IMAGE_NAME):$(ADDITIONAL_IMAGE_TAG)-s390x \
		--build-arg VERSION="$(VERSION)" \
		--build-arg BUILDER_IMAGE="$(BUILDER_IMAGE)" \
		--build-arg BUILD_FLAGS="$(DOCKER_GO_BUILD_FLAGS)" \
		--file $(DOCKERFILE) $(CURDIR)
else
	podman build --platform linux/s390x \
		$(DOCKER_BUILD_OPTS) \
		--build-arg VERSION="$(VERSION)" \
		--build-arg BUILD_FLAGS="$(DOCKER_GO_BUILD_FLAGS)" \
		--build-arg BUILDER_IMAGE="$(BUILDER_IMAGE)" \
		--tag $(IMAGE)-s390x \
		--tag $(IMAGE_NAME):$(ADDITIONAL_IMAGE_TAG)-s390x \
		--file $(DOCKERFILE) $(CURDIR)
endif

.PHONY: docker-build-power
docker-build-power: vendor ## Build ppc64le spyre health checker image
ifeq ($(DOCKER),docker)
	docker buildx build --platform linux/ppc64le \
		--push --pull  --no-cache \
		$(DOCKER_BUILD_OPTS) \
		--tag $(IMAGE)-ppc64le \
		--tag $(IMAGE_NAME):$(ADDITIONAL_IMAGE_TAG)-ppc64le \
		--build-arg VERSION="$(VERSION)" \
		--build-arg BUILDER_IMAGE="$(BUILDER_IMAGE)" \
		--file $(DOCKERFILE) $(CURDIR)
else
	podman build --platform linux/ppc64le \
		$(DOCKER_BUILD_OPTS) \
		--build-arg VERSION="$(VERSION)" \
		--build-arg BUILDER_IMAGE="$(BUILDER_IMAGE)" \
		--tag $(IMAGE)-ppc64le \
		--tag $(IMAGE_NAME):$(ADDITIONAL_IMAGE_TAG)-ppc64le \
		--file $(DOCKERFILE) $(CURDIR)
endif

.PHONY: docker-push-power
docker-push-power: ## Push ppc64le spyre health checker image
ifeq ($(DOCKER), docker)
	$(info Image '$(IMAGE)-ppc64le' was already pushed')
else
	$(DOCKER) push $(IMAGE)-ppc64le
endif

.PHONY: docker-push-amd64
docker-push-amd64: ## Push amd64 spyre health checker image
ifeq ($(DOCKER),docker)
	$(info Image '$(IMAGE)-amd64' was already pushed')
else
	$(DOCKER) push $(IMAGE)-amd64
endif

.PHONY: docker-push-s390x
docker-push-s390x: ## Push s390x spyre health checker image
ifeq ($(DOCKER),docker)
	$(info Image '$(IMAGE)-s390x' was already pushed')
else
	$(DOCKER) push $(IMAGE)-s390x
endif

.PHONY: docker-build-manifest
docker-build-manifest: ## Build spyre health checker image manifest for all architectures
ifeq ($(DOCKER), docker)
	docker manifest rm $(IMAGE) || true
	docker manifest create   $(IMAGE) $(IMAGE)-ppc64le $(IMAGE)-amd64 $(IMAGE)-s390x
	docker manifest annotate $(IMAGE) $(IMAGE)-ppc64le --os linux --arch ppc64le
	docker manifest annotate $(IMAGE) $(IMAGE)-amd64   --os linux --arch amd64
	docker manifest annotate $(IMAGE) $(IMAGE)-s390x   --os linux --arch s390x
else
	podman manifest rm $(IMAGE) || true
	podman manifest create $(IMAGE)
	podman manifest add $(IMAGE) $(IMAGE)-ppc64le
	podman manifest add $(IMAGE) $(IMAGE)-amd64
	podman manifest add $(IMAGE) $(IMAGE)-s390x
endif

.PHONY: docker-push-manifest
docker-push-manifest: ## Push spyre health checker manifest for all architectures
	$(DOCKER) manifest push $(IMAGE)

.PHONY: docker-buildx
docker-buildx: docker-build-s390x docker-build-power docker-build-amd64 ## Build spyre health checker image for all architectures

.PHONY: docker-pushx
docker-pushx: docker-push-s390x docker-push-power docker-push-amd64 docker-build-manifest docker-push-manifest ## Push spyre health checker image for all architectures

.PHONY: docker-build-pushx
docker-build-pushx: docker-buildx docker-pushx ## Build and push the multi architecture spyre health checkerimage

.PHONY: synch-to-registry
synch-to-registry: ## Synchronize the image from the source to the target registry
	skopeo copy --multi-arch all --preserve-digests docker://$(IMAGE) docker://$(ALTERNATE_REGISTRY)/$(ALTERNATE_NAMESPACE)/spyre-health-checker:$(IMAGE_TAG)

.PHONY: docker-remove-images
docker-remove-images: ## Remove images from build host
	$(DOCKER) manifest rm $(IMAGE) || true
	$(DOCKER) rmi -f $(IMAGE)-ppc64le $(IMAGE)-amd64 $(IMAGE)-s390x || true

##@ Release targets

.PHONY: echo-version
echo-version: ## Print (echo) the current version
	$(info $(VERSION))
	@echo > /dev/null

.PHONY: increment-patch-version
increment-patch-version: ## Increment patch version and create branch
	$(REPO_ROOT)/hack/increment-version.bash --patch
	$(REPO_ROOT)/hack/create-branch.bash --type version-upgrade

.PHONY: increment-minor-version
increment-minor-version: ## Increment minor version and create branch
	$(REPO_ROOT)/hack/increment-version.bash --minor
	$(REPO_ROOT)/hack/create-branch.bash --type version-upgrade

.PHONY: increment-major-version
increment-major-version: ## Increment major version and create branch
	$(REPO_ROOT)/hack/increment-version.bash --major
	$(REPO_ROOT)/hack/create-branch.bash --type version-upgrade

.PHONY: minor-release-branch
minor-release-branch: ## Create a minor release branch (i.e release_v2.3.0)
	$(REPO_ROOT)/hack/create-branch.bash --type minor-release

.PHONY: major-release-branch
major-release-branch: ## Create a minor release branch (i.e release_v3.0.0)
	$(REPO_ROOT)/hack/create-branch.bash --type major-release

.PHONY: patch-release-branch
patch-release-branch: ## Create a release branch (i.e release_v2.2.1)
	$(REPO_ROOT)/hack/create-branch.bash --type patch-release

#find the last rc number based upon the tags published
LAST_RC_NUMBER ?= $(shell $(REPO_ROOT)/hack/get-last-rc-number.bash)
.PHONY: release-candidate-branch
release-candidate-branch: ## Create a release branch (i.e release_v2.2.0)
	$(REPO_ROOT)/hack/create-branch.bash --type rc $(LAST_RC_NUMBER)

.PHONY: release-tag-push
release-tag-push: ## Create a release tag for branch
	$(info BUILD_TYPE = $(BUILD_TYPE))
ifeq (release , $(BUILD_TYPE))
	git fetch origin --tags --quiet
ifeq (, $(shell git rev-parse -q --verify "refs/tags/v$(VERSION)"))
	$(info Creating tag 'v$(VERSION)')
	git tag v$(VERSION) --annotate -m "Created  release v$(VERSION)"
	git push origin tag v$(VERSION)
else
	$(error Tag v$(VERSION) already exists)
endif
else
	$(error release-tag-push can be executed only for a release build)
endif

.PHONY: create-gh-release
create-gh-release: ## Create a GitHub release for the tag and branch
	$(info BUILD_TYPE = $(BUILD_TYPE))
ifeq (release , $(BUILD_TYPE))
	git fetch origin --tags --quiet
	gh release create --verify-tag --latest --generate-notes v$(VERSION)
else
	$(error create-gh-release can be executed only for a release build)
endif

# helper target for viewing the value of makefile variables.
print-%  : ;@echo $* = $($*)
