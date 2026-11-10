#!/usr/bin/env bash
# Tear down the safina-bench minikube profile. Use --keep-cluster to delete
# only the fixture namespaces but leave the cluster running.
set -euo pipefail

PROFILE="${PROFILE:-safina-bench}"
NS_COUNT="${NS_COUNT:-10}"

if [[ "${1:-}" == "--keep-cluster" ]]; then
    echo "→ deleting bench-* namespaces in profile '$PROFILE'"
    for i in $(seq 0 $((NS_COUNT - 1))); do
        kubectl --context "$PROFILE" delete namespace "bench-$i" --ignore-not-found --wait=false
    done
    kubectl --context "$PROFILE" delete namespace bench-logs --ignore-not-found --wait=false
    exit 0
fi

if minikube status -p "$PROFILE" >/dev/null 2>&1; then
    echo "→ deleting minikube profile '$PROFILE'"
    minikube delete -p "$PROFILE"
else
    echo "profile '$PROFILE' not running — nothing to do"
fi
