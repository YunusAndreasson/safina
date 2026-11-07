#!/usr/bin/env bash
# Provision a minikube profile and seed the deterministic fixture for the
# safina-bench harness. Idempotent — run repeatedly to refresh.
#
# Configurable via env:
#   PROFILE       minikube profile name (default: safina-bench)
#   NS_COUNT      number of bench-N namespaces (default: 10)
#   DEPLOY_COUNT  deployments per namespace (default: 50)
#   REPLICAS      replicas per deployment (default: 1)  # keep low: minikube
#   K8S_VERSION   minikube --kubernetes-version (default: v1.37.2)

set -euo pipefail

PROFILE="${PROFILE:-safina-bench}"
NS_COUNT="${NS_COUNT:-10}"
DEPLOY_COUNT="${DEPLOY_COUNT:-50}"
REPLICAS="${REPLICAS:-1}"
K8S_VERSION="${K8S_VERSION:-v1.37.2}"
FIXTURE_DIR="$(cd "$(dirname "$0")/../../bench" && pwd)/fixtures"
FIXTURE="$FIXTURE_DIR/manifests.yaml"

if ! command -v minikube >/dev/null; then
    echo "minikube not installed — see https://minikube.sigs.k8s.io/" >&2
    exit 1
fi
if ! command -v kubectl >/dev/null; then
    echo "kubectl not installed" >&2
    exit 1
fi

echo "→ ensuring minikube profile '$PROFILE' is running"
if ! minikube status -p "$PROFILE" >/dev/null 2>&1; then
    minikube start -p "$PROFILE" --kubernetes-version="$K8S_VERSION" --cpus=4 --memory=4096
fi

CTX="$PROFILE"
echo "→ generating fixture ($NS_COUNT ns × $DEPLOY_COUNT deploys × $REPLICAS replicas)"
generate_fixture() {
    for i in $(seq 0 $((NS_COUNT - 1))); do
        cat <<EOF
---
apiVersion: v1
kind: Namespace
metadata:
  name: bench-$i
EOF
        for d in $(seq 0 $((DEPLOY_COUNT - 1))); do
            cat <<EOF
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: app-$d
  namespace: bench-$i
  labels:
    app: app-$d
    bench: "true"
spec:
  replicas: $REPLICAS
  selector:
    matchLabels:
      app: app-$d
  template:
    metadata:
      labels:
        app: app-$d
        bench: "true"
    spec:
      containers:
      - name: pause
        image: registry.k8s.io/pause:3.10.2
        resources:
          requests:
            cpu: 1m
            memory: 8Mi
          limits:
            cpu: 10m
            memory: 16Mi
---
apiVersion: v1
kind: Service
metadata:
  name: svc-$d
  namespace: bench-$i
spec:
  selector:
    app: app-$d
  ports:
  - port: 80
    targetPort: 80
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: cm-$d
  namespace: bench-$i
data:
  marker: "bench"
EOF
        done
    done
}
mkdir -p "$FIXTURE_DIR"
generate_fixture > "$FIXTURE"
echo "→ fixture written to $FIXTURE ($(wc -l < "$FIXTURE") lines)"

echo "→ applying fixture against context $CTX"
kubectl --context "$CTX" apply -f "$FIXTURE" >/dev/null

echo "→ waiting for at least one pod per namespace to be scheduled"
for i in $(seq 0 $((NS_COUNT - 1))); do
    kubectl --context "$CTX" -n "bench-$i" wait --for=condition=Available --timeout=120s deployment/app-0 >/dev/null
done

echo "done. context: $CTX"
echo
echo "next:"
echo "  make bench-quick         # smoke run + regression gate"
echo "  make bench-full          # full grid → bench/out/"
