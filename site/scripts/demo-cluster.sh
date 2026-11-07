#!/usr/bin/env bash
# Boot a throwaway minikube profile with the website demo fixture.
#
# Isolation: minikube writes its context into $DEMO_KUBECONFIG, never into
# ~/.kube/config, and uses its own profile so existing profiles are untouched.
#
#   site/scripts/demo-cluster.sh          # start + seed (idempotent)
#   site/scripts/demo-cluster.sh stop     # stop the profile, keep its disk
#   site/scripts/demo-cluster.sh delete   # remove the profile entirely

set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
PROFILE="${PROFILE:-safina-demo}"
K8S_VERSION="${K8S_VERSION:-v1.35.1}" # matches the locally cached preload
export DEMO_KUBECONFIG="${DEMO_KUBECONFIG:-$HERE/../.demo/kubeconfig}"
export KUBECONFIG="$DEMO_KUBECONFIG"
mkdir -p "$(dirname "$KUBECONFIG")"

case "${1:-up}" in
stop)
	minikube stop -p "$PROFILE"
	exit 0
	;;
delete)
	minikube delete -p "$PROFILE"
	rm -f "$KUBECONFIG"
	exit 0
	;;
esac

if ! minikube status -p "$PROFILE" >/dev/null 2>&1; then
	echo "→ starting minikube profile $PROFILE"
	minikube start -p "$PROFILE" --kubernetes-version="$K8S_VERSION" \
		--cpus=4 --memory=4096 --keep-context=false
fi

# minikube names everything after the profile. Re-register the same local
# cluster under region-style names with an on-call user, so the cluster
# picker reads like a real fleet. Every context reaches the one cluster.
SERVER="$(kubectl config view --raw -o jsonpath="{.clusters[?(@.name==\"$PROFILE\")].cluster.server}")"
CA="$(kubectl config view --raw -o jsonpath="{.clusters[?(@.name==\"$PROFILE\")].cluster.certificate-authority}")"
CERT="$(kubectl config view --raw -o jsonpath="{.users[?(@.name==\"$PROFILE\")].user.client-certificate}")"
KEY="$(kubectl config view --raw -o jsonpath="{.users[?(@.name==\"$PROFILE\")].user.client-key}")"
kubectl config set-credentials oncall --client-certificate="$CERT" --client-key="$KEY" >/dev/null
for region in eu-north-1 eu-west-1 eu-west-2 us-east-1 us-east-2 us-west-2 ap-south-1; do
	kubectl config set-cluster "fleet-$region" --server="$SERVER" --certificate-authority="$CA" >/dev/null
	kubectl config set-context "$region" --cluster="fleet-$region" --user=oncall >/dev/null
done
kubectl config delete-context "$PROFILE" >/dev/null 2>&1 || true
kubectl config use-context eu-north-1 >/dev/null

# Side-load images from the host instead of pulling inside the node: the
# minikube docker network's DNS is unreliable on some hosts, and this also
# keeps re-seeding offline-friendly.
METRICS_IMG=registry.k8s.io/metrics-server/metrics-server:v0.8.1
for img in busybox:1.37 "$METRICS_IMG"; do
	docker image inspect "$img" >/dev/null 2>&1 || docker pull -q "$img"
	minikube -p "$PROFILE" image load "$img"
done

# Every demo workload is the same busybox image under a realistic name, so
# screens read like a real shop rather than a fixture.
for name in storefront-web:3.8.1 cart:1.22.0 payments-api:5.4.2 settlement-worker:5.4.2 \
	ledger-db:17.6 ledger-api:2.14.0 search-api:0.9.3 search-indexer:0.9.3 \
	otel-collector:0.112.0 backup:1.4.0 \
	checkout:4.1.0 recommendations:2.3.7 image-resizer:1.9.2 fraud-scoring:3.0.4 refunds-worker:5.4.2 payouts-api:1.12.0 reconciler:2.14.0 query-router:0.9.3 auth-api:6.2.1 token-signer:6.2.1 session-store:7.4.0 email-sender:3.3.0 push-gateway:1.8.5 sms-relay:0.6.1 log-shipper:3.1.2; do
	minikube -p "$PROFILE" image tag busybox:1.37 "registry.example.com/shop/$name"
done

echo "→ enabling metrics-server (hot pods view)"
minikube addons enable metrics-server -p "$PROFILE" >/dev/null
kubectl -n kube-system patch deploy metrics-server --type=json -p="[
  {\"op\":\"replace\",\"path\":\"/spec/template/spec/containers/0/image\",\"value\":\"$METRICS_IMG\"},
  {\"op\":\"replace\",\"path\":\"/spec/template/spec/containers/0/imagePullPolicy\",\"value\":\"IfNotPresent\"},
  {\"op\":\"replace\",\"path\":\"/spec/template/spec/containers/0/args/4\",\"value\":\"--metric-resolution=15s\"}]" >/dev/null

echo "→ applying demo workloads"
kubectl apply -f "$HERE/demo-manifests.yaml"

echo "→ waiting for healthy workloads"
for ns_dep in storefront/web storefront/cart payments/payments-api payments/settlement-worker search/search-api ops/otel-collector; do
	kubectl -n "${ns_dep%/*}" rollout status "deploy/${ns_dep#*/}" --timeout=180s
done
kubectl -n ledger rollout status sts/ledger-db --timeout=180s

echo "✓ demo cluster ready — KUBECONFIG=$KUBECONFIG"
