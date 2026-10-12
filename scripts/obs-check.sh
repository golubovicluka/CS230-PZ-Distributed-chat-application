#!/usr/bin/env bash
# Checks that the chat's metrics, traces, logs, profiles, dashboard, SLO rules
# and alert notifications all reached their backends in the kind cluster.
set -euo pipefail

namespace="${CHAT_NAMESPACE:-chat}"
kube=(kubectl --context kind-chat)
forwards=()
failures=0

cleanup() {
  ((${#forwards[@]})) && kill "${forwards[@]}" 2>/dev/null || true
}
trap cleanup EXIT

forward() {
  "${kube[@]}" -n observability port-forward "svc/$1" "$2:$3" >/dev/null 2>&1 &
  forwards+=($!)
}

forward prometheus-operated 19090 9090
forward tempo 13200 3200
forward loki 13100 3100
forward pyroscope 14040 4040
forward kube-prometheus-stack-grafana 13000 80
forward ntfy 18080 80
forward mailpit 18025 8025
sleep 3

check() {
  local name="$1"
  shift
  for _ in $(seq 24); do
    if "$@" >/dev/null 2>&1; then
      echo "ok   $name"
      return
    fi
    sleep 5
  done
  echo "FAIL $name" >&2
  failures=$((failures + 1))
}

prometheus() {
  curl -fsS --get --data-urlencode "query=$1" http://127.0.0.1:19090/api/v1/query |
    jq -e "(.data.result[0].value[1] | tonumber) $2"
}

tempo_has() {
  curl -fsS --get --data-urlencode "q={resource.service.name=\"$1\"}" \
    --data-urlencode "start=$(($(date +%s) - 3600))" --data-urlencode "end=$(date +%s)" \
    --data-urlencode limit=5 http://127.0.0.1:13200/api/search | jq -e '.traces | length > 0'
}

loki_has() {
  curl -fsS --get --data-urlencode 'query={service_name="chatserver"}' --data-urlencode since=1h \
    --data-urlencode limit=5 http://127.0.0.1:13100/loki/api/v1/query_range | jq -e '.data.result | length > 0'
}

pyroscope_has() {
  local now
  now="$(date +%s)"
  curl -fsS -H 'Content-Type: application/json' \
    -d "{\"name\":\"service_name\",\"start\":$(((now - 3600) * 1000)),\"end\":$((now * 1000))}" \
    http://127.0.0.1:14040/querier.v1.QuerierService/LabelValues | jq -e '.names | index("chatserver") != null'
}

grafana_dashboard() {
  local password
  password="$("${kube[@]}" -n observability get secret grafana-admin -o jsonpath='{.data.admin-password}' | base64 -d)"
  curl -fsS -u "admin:$password" http://127.0.0.1:13000/api/dashboards/uid/chat | jq -e '.dashboard.title == "Chat"'
}

slo_rules() {
  "${kube[@]}" -n "$namespace" get prometheusrule chat-http-availability chat-fanout-latency
}

ntfy_heartbeat() {
  curl -fsS 'http://127.0.0.1:18080/chat-heartbeat/json?poll=1&since=all' | grep -q '"event":"message"'
}

mailpit_has_mail() {
  curl -fsS http://127.0.0.1:18025/api/v1/messages | jq -e '.total > 0'
}

check "metrics: a chat_connections_active series per chat server" \
  prometheus "count(count by (instance) (chat_connections_active{job=\"chatserver\", k8s_namespace_name=\"$namespace\"}))" '>= 2'
check "metrics: blackbox probes succeed" prometheus 'min(probe_success{job="chat-health"})' '== 1'
check "metrics: Envoy proxies are scraped" prometheus 'count(envoy_server_live)' '>= 1'
check "traces: chatserver spans in Tempo" tempo_has chatserver
check "traces: gateway spans from Envoy in Tempo" tempo_has envoy-gateway
check "logs: chatserver logs in Loki" loki_has
check "profiles: chatserver in Pyroscope" pyroscope_has
check "dashboards: Chat dashboard in Grafana" grafana_dashboard
check "slos: Pyrra generated the SLO rules" slo_rules
check "alerting: Watchdog heartbeat reached ntfy" ntfy_heartbeat
check "alerting: Alertmanager email reached Mailpit" mailpit_has_mail

if ((failures > 0)); then
  echo "$failures check(s) failed" >&2
  exit 1
fi
echo "all observability checks passed"
