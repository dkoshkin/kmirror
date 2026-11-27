# Copyright 2026 Dimitri Koshkin. All rights reserved.
# SPDX-License-Identifier: Apache-2.0

E2E_DIR := $(REPO_ROOT)/.local/e2e

# Kubernetes version of the workload cluster created by the e2e tests.
# It must have both a kindest/node image (CAPD control plane) and a CAPK kubemark image (worker nodes).
export E2E_KUBERNETES_VERSION ?= v1.35.5

# The e2e tests run a local build of the CAPK manager in place of the in-cluster one.
# It is built with cgo so it is dynamically linked on Linux and mirrord can hook it,
# and with the Go version CAPK declares, which mirrord's Go support is tested against.
CAPK_REPOSITORY ?= https://github.com/kubernetes-sigs/cluster-api-provider-kubemark
CAPK_GO_TOOLCHAIN ?= go1.26.11
CAPK_SRC_DIR := $(E2E_DIR)/src/cluster-api-provider-kubemark-$(CAPK_VERSION)
export CAPK_MANAGER_BINARY ?= $(E2E_DIR)/bin/capk-manager-$(CAPK_VERSION)

$(CAPK_MANAGER_BINARY):
	$(info $(M) building CAPK $(CAPK_VERSION) manager)
	rm -rf $(CAPK_SRC_DIR)
	git clone --quiet --depth 1 --branch $(CAPK_VERSION) $(CAPK_REPOSITORY) $(CAPK_SRC_DIR)
	cd $(CAPK_SRC_DIR) && env CGO_ENABLED=1 GOTOOLCHAIN=$(CAPK_GO_TOOLCHAIN) GOWORK=off go build -o $@ .

.PHONY: e2e.capk-manager
e2e.capk-manager: ## Builds the CAPK manager binary used by the e2e tests
e2e.capk-manager: $(CAPK_MANAGER_BINARY)
