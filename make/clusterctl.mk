# Copyright 2026 Dimitri Koshkin. All rights reserved.
# SPDX-License-Identifier: Apache-2.0

# Versions of the Cluster API providers installed for e2e tests.
# CAPK (the Kubemark provider) v0.13 supports Cluster API v1.13.
export CAPI_VERSION ?= v1.13.8
export CAPD_VERSION ?= $(CAPI_VERSION)
export CAPK_VERSION ?= v0.13.0

export CLUSTERCTL_CONFIG ?= $(REPO_ROOT)/hack/e2e/clusterctl.yaml

.PHONY: clusterctl.init
clusterctl.init: ## Installs Cluster API, CAPD and CAPK into the KinD cluster
clusterctl.init: ; $(info $(M) installing Cluster API $(CAPI_VERSION), CAPD and CAPK $(CAPK_VERSION))
	if kubectl --kubeconfig=$(KIND_KUBECONFIG) get namespace capk-system >/dev/null 2>&1; then \
	  echo "Cluster API is already installed"; \
	else \
	clusterctl init \
	  --config=$(CLUSTERCTL_CONFIG) \
	  --kubeconfig=$(KIND_KUBECONFIG) \
	  --core cluster-api:$(CAPI_VERSION) \
	  --bootstrap kubeadm:$(CAPI_VERSION) \
	  --control-plane kubeadm:$(CAPI_VERSION) \
	  --infrastructure docker:$(CAPD_VERSION),kubemark:$(CAPK_VERSION) \
	  --wait-providers; \
	fi

.PHONY: clusterctl.delete
clusterctl.delete:
	clusterctl delete --config=$(CLUSTERCTL_CONFIG) --kubeconfig=$(KIND_KUBECONFIG) --all
