#!/usr/bin/env bash
# Drives WebSocket load through Envoy with k6 and fails unless KEDA scales
# chatserver above its minimum. The first argument is the local CA file.
set -euo pipefail

ca_file="${1:?usage: load.sh <ca-file>}"
namespace="${CHAT_NAMESPACE:-chat}"
kube=(kubectl --context kind-chat)

"${kube[@]}" -n observability port-forward svc/prometheus-operated 19090:9090 >/dev/null 2>&1 &
forward=$!
trap 'kill "$forward" 2>/dev/null || true' EXIT
for _ in $(seq 30); do
  curl -fs -o /dev/null http://127.0.0.1:19090/-/ready && break
  sleep 1
done

minimum="$("${kube[@]}" -n "$namespace" get scaledobject chatserver -o jsonpath='{.spec.minReplicaCount}')"

SSL_CERT_FILE="$ca_file" \
  K6_PROMETHEUS_RW_SERVER_URL=http://127.0.0.1:19090/api/v1/write \
  K6_PROMETHEUS_RW_TREND_STATS='p(95),p(99),avg' \
  k6 run --quiet -o experimental-prometheus-rw tests/load/ws-fanout.js &
k6_pid=$!

peak="$minimum"
while kill -0 "$k6_pid" 2>/dev/null; do
  desired="$("${kube[@]}" -n "$namespace" get hpa keda-hpa-chatserver -o jsonpath='{.status.desiredReplicas}' 2>/dev/null || true)"
  available="$("${kube[@]}" -n "$namespace" get deployment chatserver -o jsonpath='{.status.availableReplicas}' 2>/dev/null || true)"
  echo "chatserver replicas: ${available:-?} available, ${desired:-?} wanted by KEDA"
  if [[ "$desired" =~ ^[0-9]+$ ]] && ((desired > peak)); then
    peak="$desired"
  fi
  sleep 15
done

k6_status=0
wait "$k6_pid" || k6_status=$?

if ((peak <= minimum)); then
  echo "KEDA never asked for more than $minimum chat servers" >&2
  exit 1
fi
echo "KEDA scaled chatserver from $minimum to $peak replicas"
exit "$k6_status"
