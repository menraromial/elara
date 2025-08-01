#!/bin/bash
# This script simulates power fluctuations by applying labels to Kubernetes worker nodes.

set -e

# Get a list of worker nodes (excluding nodes with the master role label)
WORKER_NODES=$(kubectl get nodes -l '!node-role.kubernetes.io/master' -o jsonpath='{.items[*].metadata.name}')

if [ -z "$WORKER_NODES" ]; then
    echo "Error: No worker nodes found."
    exit 1
fi

echo "Target worker nodes: $WORKER_NODES"

# --- One-time setup: Set optimal power label on nodes if not present ---
for node in $WORKER_NODES; do
    if ! kubectl get node "$node" -o jsonpath='{.metadata.labels.rapl/optimal}' &> /dev/null; then
        OPTIMAL_POWER=100 # Assign a default optimal power for simulation
        echo "Setting 'rapl/optimal=${OPTIMAL_POWER}' on node $node..."
        kubectl label node "$node" "rapl/optimal"="$OPTIMAL_POWER" --overwrite
    fi
done

echo
echo "Starting power simulation loop. Press Ctrl+C to stop."

# --- Simulation loop ---
while true; do
    echo "---"
    echo "New power simulation cycle..."
    for node in $WORKER_NODES; do
        OPTIMAL_POWER=$(kubectl get node "$node" -o jsonpath='{.metadata.labels.rapl/optimal}')
        
        # Generate a random current power between 40% and 100% of optimal
        CURRENT_POWER=$(awk -v pmax="$OPTIMAL_POWER" 'BEGIN{srand(); print pmax * (0.4 + rand() * 0.6)}')
        
        echo "Updating node '$node': setting 'rapl/current' to ${CURRENT_POWER}"
        kubectl label node "$node" "rapl/current"="${CURRENT_POWER}" --overwrite
    done
    
    # Wait for the next cycle
    sleep 20
done