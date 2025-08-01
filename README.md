
# Elara: Energy-Aware Kubernetes Controller

[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)
[![Go Report Card](https://goreportcard.com/badge/github.com/menraromial/elara)](https://goreportcard.com/report/github.com/menraromial/elara)
[![CI/CD Status](https://img.shields.io/badge/CI%2FCD-TBD-lightgrey)](https://github.com/menraromial/elara/actions)

**Elara is a Kubernetes native controller designed to align application scaling with energy availability. In dynamic environments where power supply can fluctuate (e.g., due to renewable sources or cost signals), Elara ensures that your workloads adapt by proportionally scaling down deployment replicas during power dips and restoring them to optimal levels when power is stable.**

This controller introduces a declarative, policy-driven approach to GreenOps, allowing you to manage your cluster's energy consumption without sacrificing the core principles of Kubernetes automation.

---

## Table of Contents

- [Overview](#overview)
- [How It Works](#how-it-works)
- [Key Features](#key-features)
- [Getting Started](#getting-started)
  - [Prerequisites](#prerequisites)
  - [Installation](#installation)
- [Usage](#usage)
  - [1. Define an ElaraPolicy](#1-define-an-elarapolicy)
  - [2. Label Your Nodes](#2-label-your-nodes)
  - [3. Observe the Controller](#3-observe-the-controller)
- [The Scaling Algorithm](#the-scaling-algorithm)
- [Development](#development)
  - [Running Locally](#running-locally)
  - [Building and Pushing the Image](#building-and-pushing-the-image)
  - [Running the Simulation](#running-the-simulation)
- [Contributing](#contributing)
- [License](#license)

## Overview

Traditional Kubernetes autoscaling (like the HPA) is reactive to application-level metrics such as CPU and memory utilization. Elara complements this by introducing an infrastructure-level scaling dimension: **power availability**.

The core idea is to treat the total number of replicas in your managed deployments as a "power budget." When the available power drops, Elara declaratively calculates the new target state for all managed deployments and scales them down in a fair, weighted, and constraint-aware manner.

## How It Works

1.  **Policy as Code**: You define a single, cluster-scoped `ElaraPolicy` Custom Resource. This policy specifies which `Deployments` to manage and defines their scaling rules (min/max replicas, groups, and weights).
2.  **Infrastructure Awareness**: The controller continuously observes the worker nodes in the cluster. It aggregates the values from two specific labels on each node:
    - `rapl/optimal`: The maximum, optimal power capacity of the node.
    - `rapl/current`: The currently available power for that node.
3.  **Declarative Reconciliation**: At each reconciliation loop (and periodically), the controller:
    - Calculates the total optimal and current power for the entire cluster.
    - Determines the power reduction percentage.
    - Computes the ideal number of replicas for **every single managed deployment** based on this percentage, starting from their defined `maxReplicas`.
4.  **Intelligent Scaling**: The scaling algorithm distributes the required reduction proportionally across deployment groups and independent deployments. It respects `minReplicas` constraints and handles reduction deficits by re-assigning them to deployments with more capacity.
5.  **Cluster Enforcement**: The controller updates the `.spec.replicas` field of the managed `Deployments` to match the calculated target state. If power is restored, it scales all deployments back up to their `maxReplicas`.

## Key Features

- **Declarative Configuration**: Manage your entire energy scaling policy with a single Kubernetes manifest.
- **Group-Based Scaling**: Group related deployments (e.g., a web frontend and its backend) to scale them as a single unit.
- **Weighted Prioritization**: Assign weights to deployments within a group to ensure critical components are scaled down last.
- **Constraint-Aware**: Strictly respects the `minReplicas` and `maxReplicas` defined for each deployment.
- **Infrastructure-Driven**: Reacts to the real state of your nodes, not just application metrics.
- **Built with Kubebuilder**: Follows modern best practices for building robust Kubernetes operators.

## Getting Started

### Prerequisites

- A running Kubernetes cluster (v1.21+).
- `kubectl` configured to connect to your cluster.
- `kustomize` (v5+) installed.
- Docker or another container runtime for building the controller image.

### Installation

1.  **Clone the Repository**
    ```sh
    git clone https://github.com/menraromial/elara.git
    cd elara
    ```

2.  **Build and Push the Controller Image**
    Update the `IMG` variable in the `Makefile` or provide it on the command line, then build and push the image to your container registry.
    ```sh
    # Replace with your own registry
    IMG="your-registry/elara-controller:v0.1.0"
    
    make docker-build IMG=$IMG
    make docker-push IMG=$IMG
    ```

3.  **Deploy the Controller**
    Before deploying, make sure the image name in `config/manager/manager.yaml` is updated to match the one you just pushed. Then, use the `Makefile` target to deploy the CRD, RBAC rules, and the controller Deployment.
    ```sh
    make deploy
    ```
    This will create the `elara-system` namespace and all necessary resources.

## Usage

### 1. Define an ElaraPolicy

Create a YAML file for your policy. This tells Elara which deployments to manage.

**`my-policy.yaml`**
```yaml
apiVersion: greenops.elara.dev/v1
kind: ElaraPolicy
metadata:
  name: cluster-policy
spec:
  deployments:
    - name: frontend-app
      namespace: production
      minReplicas: 2
      maxReplicas: 20
      weight: "0.6" # Must be a string
      group: web-tier
    - name: backend-api
      namespace: production
      minReplicas: 3
      maxReplicas: 15
      weight: "0.4"
      group: web-tier
    - name: monitoring-agent
      namespace: monitoring
      minReplicas: 1
      maxReplicas: 2
```

Apply it to your cluster:
```sh
kubectl apply -f my-policy.yaml
```

### 2. Label Your Nodes

The controller will now look for power labels on your worker nodes. You must apply these labels for the controller to function.

```sh
# Example for a worker node named 'node-1'
# Set the optimal (max) power capacity
kubectl label node node-1 rapl/optimal=100

# Set the initial current power
kubectl label node node-1 rapl/current=100
```
Repeat for all worker nodes you want to include in the power calculation.

### 3. Observe the Controller

- **Check the logs** to see reconciliation in action:
  ```sh
  kubectl logs -f -n elara-system deploy/elara-controller-manager -c manager
  ```
- **Simulate a power drop** by changing a node's label:
  ```sh
  kubectl label node node-1 rapl/current=60 --overwrite
  ```
- **Watch your deployments scale down**:
  ```sh
  kubectl get deployments -n production -w
  ```

## The Scaling Algorithm

The core logic is declarative and follows these steps during a power reduction:

1.  **Global Reduction Target**: Calculate the total number of replicas to remove from the cluster-wide maximum based on the power drop percentage.
2.  **Entity-Level Distribution**: Distribute this total reduction among deployment groups and independent deployments, proportional to their maximum replica potential (`maxReplicas`).
3.  **Group-Level Distribution**: Within each group, distribute its share of the reduction among its members based on their assigned `weight`.
4.  **Constraint Enforcement & Deficit Handling**: Ensure no deployment is scaled below its `minReplicas`. If a reduction is blocked, the "deficit" is intelligently re-assigned to other deployments that still have scaling margin.
5.  **State Calculation**: The final target replica count is calculated by subtracting the final reduction from each deployment's `maxReplicas`.

## Development

You can easily run and test the controller locally.

### Running Locally

To run the controller outside the cluster (e.g., on your local machine), it will use your local kubeconfig file to connect to the cluster.

```sh
# This will also install controller-gen and kustomize if they are missing
make run
```

### Building and Pushing the Image

```sh
# Make sure to set the IMG variable
make docker-build docker-push IMG=your-registry/elara-controller:v0.1.0
```

### Running the Simulation

A helper script is provided in the `hack/` directory to simulate power changes by automatically updating node labels.

```sh
# Make the script executable
chmod +x hack/simulate-power-changes.sh

# Run the simulation
./hack/simulate-power-changes.sh
```

## Contributing

Contributions are welcome! Please feel free to open an issue or submit a pull request.

1.  Fork the repository.
2.  Create your feature branch (`git checkout -b feature/AmazingFeature`).
3.  Commit your changes (`git commit -m 'Add some AmazingFeature'`).
4.  Push to the branch (`git push origin feature/AmazingFeature`).
5.  Open a Pull Request.

## License

This project is licensed under the Apache 2.0 License. See the [LICENSE](LICENSE) file for details.
