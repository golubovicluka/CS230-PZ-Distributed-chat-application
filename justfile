set shell := ["bash", "-euo", "pipefail", "-c"]

pnpm := "pnpm --dir chat-app"
local_db := "postgres://chat:chat@127.0.0.1:5432/chat?sslmode=disable"
otel_env := "OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-lgtm:4318 PYROSCOPE_SERVER_ADDRESS=http://otel-lgtm:4040"
release_identity := "^https://github.com/golubovicluka/CS230-PZ-Distributed-chat-application/\\.github/workflows/release\\.yaml@"
repo_url := "https://github.com/golubovicluka/CS230-PZ-Distributed-chat-application.git"
kube := "kubectl --context kind-chat"
docker_host := "export DOCKER_HOST=\"$(docker context inspect --format '{{.Endpoints.docker.Host}}')\""

# List recipes
default:
    @just --list --unsorted

# Install pinned tools, dependencies and the git hooks
setup:
    mise install
    go mod download
    {{ pnpm }} install --frozen-lockfile
    just hooks

# Start the Colima VM that provides Docker Engine on macOS
colima:
    scripts/colima-start.sh

# Show versions of every tool this repo depends on
doctor:
    @echo "docker context: $(docker context show)"
    @docker version --format 'docker engine {{{{.Server.Version}}' || echo "docker engine: not reachable"
    @docker buildx version
    @docker compose version
    @go version
    @node --version
    @pnpm --version
    @golangci-lint version
    @gitleaks version
    @shellcheck --version | sed -n 2p
    @yamllint --version
    @markdownlint-cli2 --help | head -1
    @dagger version
    @act --version
    @cosign version 2>&1 | grep GitVersion
    @tofu version | head -1
    @kind version
    @kubectl version --client | head -1
    @helm version --short
    @tilt version
    @k9s version --short | head -1
    @kubeconform -v
    @k6 version
    @jq --version

# Build and start the full stack: http://localhost:8080
up:
    docker compose up -d --build --wait
    @echo "Chat: http://localhost:8080"

# Same as up, plus Grafana/Prometheus/Loki/Tempo/Pyroscope in one container
up-obs:
    {{ otel_env }} docker compose --profile obs up -d --build --wait
    @echo "Chat: http://localhost:8080   Grafana: http://localhost:3000"

# Stop the stack, keeping the database volume
down:
    docker compose --profile obs down

# Stop the stack and delete the database volume
reset:
    docker compose --profile obs down --volumes

# Follow logs, optionally for specific services
logs *services:
    docker compose logs --follow {{ services }}

# Check the running stack through its public origin
smoke base="http://localhost:8080":
    scripts/smoke.sh {{ base }}

# Simulate a crash of one chat server (SIGKILL, no deregistration)
crash server="chatserver-1":
    docker compose kill --signal SIGKILL {{ server }}

# Start only Postgres and Redis for running the Go binaries on the host
deps:
    docker compose up -d --wait postgres redis

# Run the load balancer on the host
dev-lb:
    go run ./cmd/loadbalancer

# Run a chat server on the host, e.g. `just dev-server 8081`
dev-server port="8080":
    go run ./cmd/chatserver -name chat-{{ port }} -listen 127.0.0.1:{{ port }} -advertise ws://127.0.0.1:{{ port }} -db "{{ local_db }}"

# Run the Vite dev server with proxies to the host backend
dev-web:
    {{ pnpm }} dev

# Unit tests (integration tests skip without databases)
test:
    go test -race ./...

# All tests against the Compose Postgres and Redis
test-integration: deps
    TEST_DATABASE_URL="{{ local_db }}" TEST_REDIS_ADDR=127.0.0.1:6379 go test -race -count=1 ./...

# Every linter and format check
lint: lint-go lint-web lint-yaml lint-md lint-shell secrets

lint-go:
    golangci-lint run ./...
    golangci-lint fmt --diff ./...
    go mod tidy -diff

lint-web:
    {{ pnpm }} lint
    {{ pnpm }} exec tsc -b

lint-yaml:
    yamllint --strict .

lint-md:
    markdownlint-cli2

lint-shell:
    shellcheck scripts/*.sh .githooks/pre-commit

# Scan git history and the working tree for secrets
secrets:
    gitleaks git --redact --no-banner .
    gitleaks dir --redact --no-banner .

# Run every CI check through Dagger, exactly as GitHub Actions does
ci:
    dagger check

# List the CI checks
ci-list:
    dagger check -l

# Build all images for amd64 and arm64 and fail on fixable critical CVEs
images-scan:
    dagger call images

# Write an SPDX SBOM for one component (chatserver, loadbalancer or web)
sbom component="chatserver":
    dagger call sbom --component {{ component }} export --path sbom-{{ component }}.spdx.json

# Run the CI workflow locally with act against the current Docker context
act:
    DOCKER_HOST="$(docker context inspect --format '{{{{.Endpoints.docker.Host}}')" act pull_request --workflows .github/workflows/ci.yaml

# Verify the keyless signature and SBOM attestation of a published image
verify-image image:
    cosign verify {{ image }} --certificate-identity-regexp "{{ release_identity }}" --certificate-oidc-issuer https://token.actions.githubusercontent.com
    cosign verify-attestation {{ image }} --type spdxjson --certificate-identity-regexp "{{ release_identity }}" --certificate-oidc-issuer https://token.actions.githubusercontent.com

# Apply Go formatting
fmt:
    golangci-lint fmt ./...

# Build images for the local platform through Compose
images:
    docker compose build

# Build every image for linux/amd64 and linux/arm64 (no load)
images-all:
    docker buildx bake

# Point git at the versioned hooks in .githooks
hooks:
    git config core.hooksPath .githooks

# Create the kind cluster, zot registry and Argo CD platform, then configure OpenBao
cluster rev=`git rev-parse --abbrev-ref HEAD` repo=repo_url:
    #!/usr/bin/env bash
    set -euo pipefail
    if [[ -z "${CI:-}" ]] && ! git ls-remote --exit-code origin "{{ rev }}" >/dev/null 2>&1 && [[ -z "$(git branch -r --contains "{{ rev }}" 2>/dev/null)" ]]; then
      echo "Argo CD syncs the platform from GitHub: push {{ rev }} first" >&2
      exit 1
    fi
    ca="$(scripts/local-ca.sh)"
    {{ docker_host }}
    tofu -chdir=infra/local init -input=false
    tofu -chdir=infra/local apply -input=false -auto-approve \
      -var "git_revision={{ rev }}" -var "repo_url={{ repo }}" \
      -var "ca_cert_path=$ca/ca.crt" -var "ca_key_path=$ca/ca.key"
    kubectl config use-context kind-chat
    just platform-wait
    just cluster-secrets

# Delete the cluster and the registry container (the registry volume stays as a cache)
cluster-down:
    #!/usr/bin/env bash
    set -euo pipefail
    ca="$(scripts/local-ca.sh)"
    {{ docker_host }}
    rm -f infra/secrets/terraform.tfstate infra/secrets/terraform.tfstate.backup
    tofu -chdir=infra/local destroy -input=false -auto-approve \
      -var "ca_cert_path=$ca/ca.crt" -var "ca_key_path=$ca/ca.key"

# Wait until every platform Application is Synced and Healthy
platform-wait timeout="900":
    #!/usr/bin/env bash
    set -euo pipefail
    deadline=$((SECONDS + {{ timeout }}))
    for app in cert-manager envoy-gateway cloudnative-pg openbao external-secrets kube-prometheus-stack keda \
      platform-config loki tempo pyroscope opentelemetry-collector blackbox-exporter pyrra headlamp observability-config; do
      until [[ "$({{ kube }} -n argocd get application "$app" -o jsonpath='{.status.sync.status}/{.status.health.status}' 2>/dev/null)" == "Synced/Healthy" ]]; do
        if ((SECONDS >= deadline)); then
          echo "timed out waiting for $app" >&2
          {{ kube }} -n argocd get applications >&2
          echo "conditions and last operation of $app:" >&2
          {{ kube }} -n argocd get application "$app" -o jsonpath='{range .status.conditions[*]}{.type}: {.message}{"\n"}{end}operation {.status.operationState.phase}: {.status.operationState.message}{"\n"}' >&2
          echo "resources of $app that are not Synced:" >&2
          {{ kube }} -n argocd get application "$app" -o jsonpath='{range .status.resources[?(@.status!="Synced")]}{.kind} {.namespace}/{.name}: {.status} {.health.status} {.health.message}{"\n"}{end}' >&2
          exit 1
        fi
        sleep 5
      done
      echo "ok   $app"
    done

# Configure OpenBao (kv, policy, Kubernetes auth, Redis password) with OpenTofu
cluster-secrets:
    #!/usr/bin/env bash
    set -euo pipefail
    {{ kube }} -n openbao wait pod/openbao-0 --for=condition=Ready --timeout=300s
    {{ kube }} -n openbao port-forward svc/openbao 18200:8200 >/dev/null &
    forward=$!
    trap 'kill "$forward"' EXIT
    for _ in $(seq 30); do
      curl -fs -o /dev/null http://127.0.0.1:18200/v1/sys/health && break
      sleep 1
    done
    tofu -chdir=infra/secrets init -input=false
    tofu -chdir=infra/secrets apply -input=false -auto-approve
    {{ kube }} annotate clustersecretstore openbao "chat.localhost/configured-at=$(date +%s)" --overwrite
    {{ kube }} wait clustersecretstore/openbao --for=condition=Ready --timeout=120s

# Inner loop on the cluster: build to zot, deploy the chart, watch for changes
dev:
    tilt up

# Build and deploy once, then exit when everything is ready (used by CI)
dev-ci timeout="20m":
    tilt ci --timeout {{ timeout }}

# Trust the local development CA (macOS login keychain or the Linux system store)
trust-ca:
    #!/usr/bin/env bash
    set -euo pipefail
    ca="$(scripts/local-ca.sh)/ca.crt"
    case "$(uname -s)" in
      Darwin) security add-trusted-cert -r trustRoot -k "$HOME/Library/Keychains/login.keychain-db" "$ca" ;;
      Linux) sudo install -m 0644 "$ca" /usr/local/share/ca-certificates/chat-localhost.crt && sudo update-ca-certificates ;;
      *) echo "add $ca to your trust store manually" >&2; exit 1 ;;
    esac

# Smoke-test the cluster through Envoy over HTTPS
k8s-smoke base="https://chat.localhost":
    scripts/smoke.sh {{ base }} "$(scripts/local-ca.sh)/ca.crt"

# End-to-end WebSocket test against the cluster
e2e base="https://chat.localhost":
    E2E_BASE_URL={{ base }} E2E_CA_FILE="$(scripts/local-ca.sh)/ca.crt" go test -tags e2e -count=1 -v ./test/e2e/

# Restart the chat servers under an open socket; expect a 1001 close and a clean reconnect
e2e-drain base="https://chat.localhost":
    E2E_BASE_URL={{ base }} E2E_CA_FILE="$(scripts/local-ca.sh)/ca.crt" \
      E2E_RESTART_CMD="{{ kube }} -n chat rollout restart deployment/chatserver" \
      go test -tags e2e -count=1 -v -run TestGracefulDrain ./test/e2e/

# Drive WebSocket load through Envoy with k6 and fail unless KEDA scales chatserver out
load vus="150":
    VUS={{ vus }} scripts/load.sh "$(scripts/local-ca.sh)/ca.crt"

# Check that metrics, traces, logs, profiles, the dashboard, SLO rules and alert notifications arrived
obs-check:
    scripts/obs-check.sh

# Print the Grafana admin password (user admin)
grafana-password:
    @{{ kube }} -n observability get secret grafana-admin -o jsonpath='{.data.admin-password}' | base64 -d; echo

# Print the initial Argo CD admin password
argocd-password:
    @{{ kube }} -n argocd get secret argocd-initial-admin-secret -o jsonpath='{.data.password}' | base64 -d; echo

# Print a 24h Headlamp login token
headlamp-token:
    @{{ kube }} -n headlamp create token headlamp --duration 24h

# Let Argo CD deploy the chart from main with the published GHCR images
gitops:
    {{ kube }} apply -f deploy/argocd/apps/chat.yaml

# Remove the GitOps deployment
gitops-off:
    {{ kube }} delete -f deploy/argocd/apps/chat.yaml --ignore-not-found

# Write provider lock files for macOS and Linux on both architectures
tofu-lock:
    #!/usr/bin/env bash
    set -euo pipefail
    for dir in infra/local infra/secrets; do
      tofu -chdir="$dir" init -backend=false -input=false
      tofu -chdir="$dir" providers lock -platform=darwin_arm64 -platform=darwin_amd64 -platform=linux_amd64 -platform=linux_arm64
    done

# Open k9s on the cluster
k9s:
    k9s --context kind-chat
