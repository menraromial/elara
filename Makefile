# Image URL to use all building/pushing image targets
IMG ?= ghcr.io/menraromial/elara-controller:v0.1.0
# Produce CRDs that work back to Kubernetes 1.11 (no pruning)
CRD_OPTIONS ?= "crd:crdVersions=v1"

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
GOBIN ?= $(shell go env GOBIN)
# Setting GOBIN to a default if it's not set
ifeq ($(GOBIN),)
GOBIN = $(shell go env GOPATH)/bin
endif

# Tool Binaries
CONTROLLER_GEN ?= $(GOBIN)/controller-gen
KUSTOMIZE ?= $(GOBIN)/kustomize

# Target to download controller-gen if not installed
.PHONY: controller-gen
controller-gen:
	@echo "+++ Downloading controller-gen..."
	@go install sigs.k8s.io/controller-tools/cmd/controller-gen@latest

# --- NEW: Target to download kustomize if not installed ---
.PHONY: kustomize
kustomize:
ifeq (, $(shell which kustomize))
	@echo "+++ Kustomize not found. Downloading..."
	@{ \
	set -e; \
	go install sigs.k8s.io/kustomize/kustomize/v5@latest; \
	}
	@echo "Kustomize installed successfully."
else
	@echo "Kustomize is already installed."
endif

# ==========================================================================================
#  Main Targets
# ==========================================================================================

.PHONY: all
all: build

## build: Build the manager binary.
.PHONY: build
build: generate manifests
	@echo "+++ Building manager binary..."
	@go build -o bin/manager main.go

## run: Run the controller locally against the configured Kubernetes cluster.
.PHONY: run
run: generate manifests
	@echo "+++ Running controller locally..."
	@go run ./main.go

## generate: Generate code containing DeepCopy methods.
.PHONY: generate
generate: controller-gen
	@echo "+++ Generating Go object code..."
	@$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."

## manifests: Generate CRD and RBAC manifests.
.PHONY: manifests
manifests: controller-gen
	@echo "+++ Generating CRD and RBAC manifests..."
	@$(CONTROLLER_GEN) $(CRD_OPTIONS) rbac:roleName=manager-role webhook paths="./..." output:crd:artifacts:config=config/crd output:rbac:artifacts:config=config/rbac

# ==========================================================================================
#  Docker Targets
# ==========================================================================================

## docker-build: Build the container image.
.PHONY: docker-build
docker-build:
	@echo "+++ Building Docker image: $(IMG)"
	@docker build -t $(IMG) .

## docker-push: Push the container image.
.PHONY: docker-push
	@echo "+++ Pushing Docker image: $(IMG)"
	@docker push $(IMG)

# ==========================================================================================
#  Deployment Targets (Updated to use $(KUSTOMIZE))
# ==========================================================================================

## deploy: Deploy the controller to the cluster.
# This assumes your 'manager.yaml' is configured to use the $(IMG).
# You may need to run `make docker-build docker-push` first.
.PHONY: deploy
deploy: manifests kustomize
	@echo "+++ Deploying controller to the cluster..."
	# Use the kustomize binary managed by this Makefile
	@$(KUSTOMIZE) build config/crd | kubectl apply -f -
	@$(KUSTOMIZE) build config/default | kubectl apply -f -  # Assuming a 'default' kustomization exists

## undeploy: Undeploy the controller from the cluster.
.PHONY: undeploy
undeploy: kustomize
	@echo "+++ Undeploying controller from the cluster..."
	@$(KUSTOMIZE) build config/default | kubectl delete -f -
	@$(KUSTOMIZE) build config/default | kubectl delete -f -