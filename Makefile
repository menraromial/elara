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
ENVTEST ?= $(GOBIN)/setup-envtest


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


# ==========================================================================================
#  Testing Targets
# ==========================================================================================

## test-env: Download envtest-setup binaries if not already present.
.PHONY: test-env
test-env:
	@echo "+++ Setting up test environment..."
	@go install sigs.k8s.io/controller-runtime/tools/setup-envtest@latest
	@$(ENVTEST) use -p path --bin-dir $(shell pwd)/testbin 1.28.3

## test: Run the fast, functional E2E tests.
.PHONY: test
test: manifests generate test-env
	@echo "+++ Running E2E tests..."
	# --- THIS IS THE CORRECTED LINE ---
	# Use `setup-envtest` to find the correct asset path and export it.
	@KUBEBUILDER_ASSETS=`$(ENVTEST) use -p path 1.28.3` go test ./controllers/... -v -ginkgo.v

## test-perf: Run the long-running performance/scale E2E tests.
.PHONY: test-perf
test-perf: manifests generate test-env
	@echo "+++ Running performance E2E tests (this may take a minute)..."
	@KUBEBUILDER_ASSETS=`$(ENVTEST) use -p path 1.28.3` go test ./controllers/... -v -ginkgo.v -ginkgo.focus="ElaraPolicy Controller Performance E2E"

## test-exp-step: Run the Step Function experiment to measure convergence time.
.PHONY: test-exp-step
test-exp-step: manifests generate test-env
	@echo "+++ Running Experiment: Step Function Test..."
	@KUBEBUILDER_ASSETS=`$(ENVTEST) use -p path 1.28.3` go test ./controllers/... -v -ginkgo.v -ginkgo.focus="ElaraPolicy Controller: Step Function Test"

## test-exp-ramp: Run the Ramp Test experiment to measure replication error.
.PHONY: test-exp-ramp
test-exp-ramp: manifests generate test-env
	@echo "+++ Running Experiment: Ramp Test..."
	@KUBEBUILDER_ASSETS=`$(ENVTEST) use -p path 1.28.3` go test ./controllers/... -v -ginkgo.v -ginkgo.focus="ElaraPolicy Controller: Ramp Test"

## test-exp-ramp-conv: Run the Ramp Convergence experiment to measure reactivity.
.PHONY: test-exp-ramp-conv
test-exp-ramp-conv: manifests generate test-env
	@echo "+++ Running Experiment: Ramp Convergence Test (Reactivity)..."
	@KUBEBUILDER_ASSETS=`$(ENVTEST) use -p path 1.28.3` go test ./controllers/... -v -ginkgo.v -ginkgo.focus="ElaraPolicy Controller: Ramp Convergence Test"

## test-exp-stress: Run the Constraint Stress Test experiment.
.PHONY: test-exp-stress
test-exp-stress: manifests generate test-env
	@echo "+++ Running Experiment: Constraint Stress Test..."
	@KUBEBUILDER_ASSETS=`$(ENVTEST) use -p path 1.28.3` go test ./controllers/... -v -ginkgo.v -ginkgo.focus="ElaraPolicy Controller: Constraint Stress Test"

## plot: Run all experiments and generate publication-ready plots.
.PHONY: plot
plot: test-exp-ramp test-exp-ramp-conv
	@echo "+++ All experiments finished. Generating plots..."
	@python3 analysis/plot_results.py


## plot-plateau: Run the plateau experiment and generate the final plot.
.PHONY: plot-plateau
plot-plateau: test-exp-plateau
	@echo "+++ Plateau experiment finished. Generating plot..."
	@python3 analysis/plot_results.py

## test-exp-plateau: Run the Plateau Test experiment.
.PHONY: test-exp-plateau
test-exp-plateau: manifests generate test-env
	@echo "+++ Running Experiment: Power Plateau Test..."
	@KUBEBUILDER_ASSETS=`$(ENVTEST) use -p path 1.28.3` go test ./controllers/... -v -ginkgo.v -ginkgo.focus="ElaraPolicy Controller: Complex Plateau Test"

## plot-full-ramp: Run the complete ramp-down/ramp-up experiment and generate the plot.
.PHONY: plot-full-ramp
plot-full-ramp: test-exp-full-ramp
	@echo "+++ Full ramp experiment finished. Generating plot..."
	@python3 analysis/plot_results.py

## test-exp-full-ramp: Run the complex, full-cycle Ramp Test experiment.
.PHONY: test-exp-full-ramp
test-exp-full-ramp: manifests generate test-env
	@echo "+++ Running Experiment: Full-Cycle Ramp Test..."
	@KUBEBUILDER_ASSETS=`$(ENVTEST) use -p path 1.28.3` go test ./controllers/... -v -ginkgo.v -ginkgo.focus="ElaraPolicy Controller: Full-Cycle Ramp Test"





