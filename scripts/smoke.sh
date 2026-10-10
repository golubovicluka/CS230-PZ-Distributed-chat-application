#!/usr/bin/env bash
# Smoke-tests a running stack through its public origin (default: Compose gateway).
set -euo pipefail

base="${1:-http://localhost:8080}"
failures=0

check() {
  local name="$1" url="$2" expected="$3"
  local body
  if body="$(curl -fsS --max-time 5 "$url")" && grep -qF -- "$expected" <<<"$body"; then
    echo "ok   $name"
  else
    echo "FAIL $name ($url)" >&2
    failures=$((failures + 1))
  fi
}

check "gateway health" "$base/healthz" "ok"
check "runtime config" "$base/config.js" "__CHAT_CONFIG__"
check "SPA index" "$base/" "<div id=\"root\">"
check "load balancer selection" "$base/lb/get" "Address"
check "shared history" "$base/api/history" "["

if ((failures > 0)); then
  echo "$failures check(s) failed" >&2
  exit 1
fi
echo "all checks passed"
