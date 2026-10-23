SHELL := /usr/bin/env bash
.SHELLFLAGS := -eu -o pipefail -c

REGISTRY ?= ghcr.io
ORG ?= flippyboy
IMAGE_NAME ?= cluster-api-provider-hyperv
CONTROLLER_IMG ?= $(REGISTRY)/$(ORG)/$(IMAGE_NAME)
# TAG is the image tag and, for a vX.Y.Z release, the clusterctl release name.
TAG ?= latest
IMG ?= $(CONTROLLER_IMG):$(TAG)

RELEASE_DIR ?= out
CHART_DIR := charts/cluster-api-provider-hyperv

TOOLS_BIN_DIR := $(abspath hack/tools/bin)
CONTROLLER_GEN_VER := v0.21.0
CONTROLLER_GEN := $(TOOLS_BIN_DIR)/controller-gen-$(CONTROLLER_GEN_VER)
KUSTOMIZE_VER := v5.8.1
KUSTOMIZE := $(TOOLS_BIN_DIR)/kustomize-$(KUSTOMIZE_VER)
HELM ?= helm

.PHONY: all
all: test build

.PHONY: generate
generate: $(CONTROLLER_GEN)
	$(CONTROLLER_GEN) object:headerFile=hack/boilerplate.go.txt paths="./api/..."

.PHONY: manifests
manifests: $(CONTROLLER_GEN)
	$(CONTROLLER_GEN) crd:crdVersions=v1 paths="./api/..." output:crd:dir=config/crd/bases
	$(CONTROLLER_GEN) rbac:roleName=manager-role paths="./internal/controller/..." output:rbac:dir=config/rbac

.PHONY: test
test:
	go test ./...

.PHONY: build
build:
	go build -o bin/manager .

.PHONY: docker-build
docker-build:
	docker build -t $(IMG) .

.PHONY: docker-push
docker-push:
	docker push $(IMG)

.PHONY: release
release:
	$(MAKE) clean-release
	$(MAKE) release-manifests
	$(MAKE) release-metadata
	$(MAKE) release-templates
	$(MAKE) release-chart

.PHONY: clean-release
clean-release:
	rm -rf $(RELEASE_DIR)

.PHONY: release-manifests
release-manifests: $(KUSTOMIZE)
	rm -rf $(RELEASE_DIR)/config
	mkdir -p $(RELEASE_DIR)
	cp -a config $(RELEASE_DIR)/config
	cd $(RELEASE_DIR)/config/manager && $(KUSTOMIZE) edit set image $(CONTROLLER_IMG)=$(CONTROLLER_IMG):$(TAG)
	$(KUSTOMIZE) build $(RELEASE_DIR)/config/default > $(RELEASE_DIR)/infrastructure-components.yaml
	rm -rf $(RELEASE_DIR)/config
	if [[ "$(TAG)" == v* ]]; then cp $(RELEASE_DIR)/infrastructure-components.yaml $(RELEASE_DIR)/infrastructure-hyperv-$(TAG)-components.yaml; fi

.PHONY: release-metadata
release-metadata:
	mkdir -p $(RELEASE_DIR)
	cp metadata.yaml $(RELEASE_DIR)/metadata.yaml
	if [[ "$(TAG)" == v* ]]; then cp metadata.yaml $(RELEASE_DIR)/infrastructure-hyperv-$(TAG)-metadata.yaml; fi

.PHONY: release-templates
release-templates:
	mkdir -p $(RELEASE_DIR)
	cp templates/cluster-template*.yaml $(RELEASE_DIR)/

.PHONY: release-chart
release-chart: release-manifests
	python3 hack/build-chart.py --components $(RELEASE_DIR)/infrastructure-components.yaml --chart $(CHART_DIR) --image $(CONTROLLER_IMG)
	$(HELM) lint --namespace hyperv-system $(CHART_DIR)
	$(HELM) template cluster-api-provider-hyperv $(CHART_DIR) --namespace hyperv-system > $(RELEASE_DIR)/helm-template.yaml
	grep -q '^kind: CustomResourceDefinition$$' $(RELEASE_DIR)/helm-template.yaml
	grep -q '^kind: Deployment$$' $(RELEASE_DIR)/helm-template.yaml
	grep -q 'image: $(CONTROLLER_IMG):latest' $(RELEASE_DIR)/helm-template.yaml
	grep -q 'replicas: 1' $(RELEASE_DIR)/helm-template.yaml
	grep -F -q -- '--leader-elect' $(RELEASE_DIR)/helm-template.yaml
	$(HELM) template cluster-api-provider-hyperv $(CHART_DIR) --namespace hyperv-system --set leaderElection=false > $(RELEASE_DIR)/helm-no-leader.yaml
	if grep -F -q -- '--leader-elect' $(RELEASE_DIR)/helm-no-leader.yaml; then echo "leader election rendered while disabled"; exit 1; fi
	$(HELM) template cluster-api-provider-hyperv $(CHART_DIR) --namespace hyperv-system --set replicaCount=2 > $(RELEASE_DIR)/helm-replicas.yaml
	grep -F -q 'replicas: 2' $(RELEASE_DIR)/helm-replicas.yaml
	rm -rf $(RELEASE_DIR)/chart
	cp -a $(CHART_DIR) $(RELEASE_DIR)/chart
	if [[ "$(TAG)" == v* ]]; then \
		sed -i 's/^  tag: .*/  tag: $(TAG)/' $(RELEASE_DIR)/chart/values.yaml; \
		chart_version="$(TAG)"; \
		chart_version="$${chart_version#v}"; \
		$(HELM) package $(RELEASE_DIR)/chart --destination $(RELEASE_DIR) --version "$$chart_version" --app-version "$(TAG)"; \
	else \
		$(HELM) package $(RELEASE_DIR)/chart --destination $(RELEASE_DIR); \
	fi

.PHONY: helm
helm: release-chart

$(CONTROLLER_GEN):
	mkdir -p $(TOOLS_BIN_DIR)
	GOBIN=$(TOOLS_BIN_DIR) go install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_GEN_VER)
	mv $(TOOLS_BIN_DIR)/controller-gen $(CONTROLLER_GEN)

$(KUSTOMIZE):
	mkdir -p $(TOOLS_BIN_DIR)
	GOBIN=$(TOOLS_BIN_DIR) go install sigs.k8s.io/kustomize/kustomize/v5@$(KUSTOMIZE_VER)
	mv $(TOOLS_BIN_DIR)/kustomize $(KUSTOMIZE)
