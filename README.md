# Distributed Chat Application

A horizontally scalable chat built for CS230: several Go chat servers hold WebSocket connections, share messages through Redis pub/sub, persist history in PostgreSQL, and are balanced by a small Go load balancer. The React frontend discovers a server, reconnects on failure, and works behind a single public origin.

The DevOps roadmap that this repository follows lives in [docs/devops-roadmap.md](docs/devops-roadmap.md).

## Architecture

```mermaid
flowchart LR
    browser[Browser SPA] --> gateway[nginx gateway :8080]
    gateway -->|/| web[web: nginx + static SPA]
    gateway -->|/lb/| lb[loadbalancer]
    gateway -->|/api/| cs1
    gateway -->|/cs/1/ws| cs1[chatserver-1]
    gateway -->|/cs/2/ws| cs2[chatserver-2]
    cs1 & cs2 -->|register + heartbeat| lb
    cs1 & cs2 <-->|pub/sub fan-out| redis[(Redis)]
    cs1 & cs2 -->|messages| pg[(PostgreSQL)]
    cs1 & cs2 & lb -.->|OTLP + profiles| lgtm[otel-lgtm: Grafana, Prometheus, Loki, Tempo, Pyroscope]
```

1. The SPA asks the load balancer (`GET /lb/get`) for the least-loaded live chat server.
2. It opens a WebSocket to that server. Each server stores a message in PostgreSQL, then publishes it to Redis; every server delivers it to its own clients.
3. Chat servers heartbeat their load every 10s. The load balancer drops servers silent for 30s, and a server deregisters itself before a graceful shutdown.
4. On shutdown a server becomes unready, deregisters, waits `CHAT_DRAIN_DELAY`, then closes sockets with code 1001; browsers reconnect through the load balancer with jittered backoff and reload history.

## Quickstart on macOS

```bash
brew bundle          # colima, docker CLI, buildx, compose, mise
mise install         # pinned Go, Node, pnpm, just and linters
just colima          # Docker Engine in a Colima VM (no Docker Desktop)
just up              # build and start everything
open http://localhost:8080
```

Linux needs only Docker Engine with the buildx and compose plugins plus `mise install`. Details, Colima sizing and troubleshooting are in [docs/local-development.md](docs/local-development.md).

## Kubernetes

```bash
just cluster      # kind + zot + Argo CD (OpenTofu), platform sync, OpenBao configuration
just trust-ca     # once per machine
just dev          # Tilt builds into zot and deploys the Helm chart
open https://chat.localhost
```

The same app runs on a three-node kind cluster. The pieces:

- Envoy Gateway balances WebSockets across chatserver pods with `LeastRequest`, replacing the Go load balancer.
- cert-manager issues TLS from a name-constrained local CA.
- CloudNativePG runs a two-instance PostgreSQL.
- The Redis password flows from OpenBao through External Secrets.
- Argo CD owns the platform, and Headlamp and k9s give a view into the cluster.

Details are in [docs/kubernetes.md](docs/kubernetes.md).

## Observability

```bash
just up-obs
open http://localhost:3000   # Grafana, no login
```

All three OpenTelemetry signals and continuous profiles go to a single `grafana/otel-lgtm` container:

- **Traces (Tempo):** one trace follows a message from `chat.message.receive` on the sending server, through the Postgres `INSERT` and Redis `publish`, to `chat.message.fanout` on every server. The W3C trace context travels inside the Redis envelope.
- **Metrics (Prometheus):** `chat_connections_active`, `chat_messages_received_total`, `chat_messages_delivered_total`, `chat_clients_dropped_total`, `chat_message_fanout_duration_seconds`, `lb_servers_available`, Go runtime metrics, and Redis and Postgres client metrics.
- **Logs (Loki):** JSON on stdout, also exported over OTLP with trace correlation.
- **Profiles (Pyroscope):** CPU, allocations, heap and goroutines for both binaries.

Telemetry is off unless `OTEL_EXPORTER_OTLP_ENDPOINT` (and, for profiles, `PYROSCOPE_SERVER_ADDRESS`) is set.

On the kind cluster the same signals go through an OpenTelemetry Collector to Prometheus, Loki, Tempo and Pyroscope.

- Grafana has a Chat dashboard.
- Pyrra tracks two SLOs, and Alertmanager pages through ntfy and Mailpit.
- KEDA scales the chat servers on open sockets, and `just load` proves it with a k6 WebSocket test. See [docs/kubernetes.md](docs/kubernetes.md#observability-and-sre).

## Failure drills

```bash
just crash chatserver-1               # SIGKILL: LB evicts it after 30s, clients reconnect
docker compose stop chatserver-1      # SIGTERM: deregister, drain 2s, close with 1001
docker compose start chatserver-1
just smoke                            # health, config, LB, history through the gateway
```

## Running on the host

```bash
just deps                 # Postgres and Redis in containers
just dev-lb               # load balancer on :9000
just dev-server 8080      # chat server 1
just dev-server 8081      # chat server 2
just dev-web              # Vite on http://localhost:5173, proxies /lb and /api
```

## Configuration

Every chat server flag can also be set with an environment variable; flags win.

| Flag | Environment | Default | Purpose |
| --- | --- | --- | --- |
| `-name` | `CHAT_SERVER_NAME` | hostname | Name stored with messages and logs |
| `-listen` | `CHAT_LISTEN` | `127.0.0.1:8080` | HTTP and WebSocket listen address |
| `-advertise` | `CHAT_ADVERTISE` | `ws://127.0.0.1:8080` | `ws(s)://` URL or same-origin path such as `/cs/1` sent to clients by the LB |
| `-db` | `DATABASE_URL` | `postgres://chat:chat@127.0.0.1:5432/chat?sslmode=disable` | PostgreSQL URL; migrations run at startup |
| `-redis` | `REDIS_ADDR` | `127.0.0.1:6379` | Redis address |
| none | `REDIS_PASSWORD` | empty | Redis password; environment only, so it never shows up in process arguments |
| `-redis-channel` | `REDIS_CHANNEL` | `chat-messages` | Pub/sub channel |
| `-registry` | `REGISTRY_URL` | `http://127.0.0.1:9000` | Load balancer URL; empty disables registration |
| `-heartbeat` | `CHAT_HEARTBEAT_INTERVAL` | `10s` | Load report interval |
| `-allowed-origins` | `CHAT_ALLOWED_ORIGINS` | `http://localhost:5173,http://127.0.0.1:5173` | Cross-origin browsers allowed besides same-origin |
| `-drain-delay` | `CHAT_DRAIN_DELAY` | `0s` | Time spent unready before closing sockets on SIGTERM |

The load balancer takes `-address` (`:9000`), `-server-ttl` (`30s`), `-allowed-origins` and HTTP timeout flags. Both binaries expose `GET /healthz`; chat servers also expose `GET /readyz`, which checks Postgres and Redis and fails while draining.

## Development commands

`just` lists every recipe. The common ones:

| Command | What it does |
| --- | --- |
| `just ci` | Every CI check through Dagger, exactly as GitHub Actions runs it |
| `just test` | Go unit tests with the race detector |
| `just test-integration` | All Go tests against the Compose Postgres and Redis |
| `just lint` | golangci-lint, gofmt/goimports, ESLint, tsc, yamllint, markdownlint, shellcheck, gitleaks |
| `just images-scan` | Builds all images for amd64 and arm64, then gates on fixable critical CVEs |
| `just act` | Runs the GitHub `ci` workflow locally with act |
| `just verify-image <ref>` | Checks the cosign signature and SBOM attestation of a published image |
| `just hooks` | Enables the pre-commit hook (gitleaks on staged changes, Go formatting) |

Dependency updates are proposed by Renovate (`renovate.json`) once the Renovate GitHub App is enabled for the repository.

## CI and supply chain

The pipeline is a Dagger module in `ci/` (Go SDK). GitHub Actions only installs Dagger and calls it, so `just ci` on a laptop runs the same containers as CI.

| Check | What it enforces |
| --- | --- |
| `go-lint` | golangci-lint, Go formatting, `go mod tidy` |
| `go-test` | All Go tests with `-race` against PostgreSQL and Redis service containers |
| `web` | ESLint, `tsc -b` and the production build |
| `yaml`, `markdown`, `shell`, `workflows` | yamllint, markdownlint, shellcheck and actionlint |
| `secrets` | gitleaks over the working tree and git history |
| `config` | Trivy misconfiguration scan of Dockerfiles, HIGH and CRITICAL fail |
| `images` | Builds all three images for amd64 and arm64; Grype fails on fixable critical CVEs |
| `manifests` | `helm lint` and kubeconform against the Kubernetes 1.36 and CRD schemas for the chart, the platform and the Argo CD apps |
| `tofu` | `tofu fmt` and `tofu validate` for `infra/local` and `infra/secrets` |

`k8s-e2e.yaml` covers the cluster end to end on every pull request that touches the app, chart, platform or infrastructure:

1. It builds the whole kind platform for the PR's revision.
2. It deploys with `tilt ci`.
3. It runs the smoke test and two Go WebSocket tests: one for cross-connection fan-out and history, one for a graceful 1001 drain during a rolling restart.

- **SBOM precision:** the CVE gate uses a Syft SBOM that captures Go function symbols. Grype then flags a Go CVE only when the vulnerable function is actually in the binary.
- **Releases:** on `main` and `v*` tags, `release.yaml` pushes multi-arch images to `ghcr.io/golubovicluka/chat-*`. It signs them keyless with cosign through GitHub OIDC and attaches an SPDX SBOM attestation.
- **Pinning:** every GitHub Action is pinned to a commit SHA and every tool image to a digest; Renovate updates both. This is a direct response to the March 2026 Trivy compromise (CVE-2026-33634), when attackers force-pushed `trivy-action` version tags to credential-stealing code.

## Backend design

- `cmd/chatserver` and `cmd/loadbalancer` are composition roots. They parse configuration, create concrete dependencies, set up telemetry, and own startup and graceful shutdown.
- `internal/chat` owns chat sessions. One hub goroutine owns client membership; registration, removal, broker broadcasts and load snapshots move through channels.
- `internal/postgres` returns a concrete message store backed by a pgx pool. Embedded goose migrations run under an advisory lock, so several servers can start at once.
- `internal/registry` reports load to the load balancer. Its heartbeat serializes reports so a stale load never overwrites a newer one, and it stops reporting once the server deregisters.
- `internal/loadbalancer` keeps the latest report per server under a `sync.RWMutex` and ignores entries older than the TTL.
- `internal/httpapi` holds the origin allowlist (CORS and WebSocket `CheckOrigin`) and the health and readiness handlers.
- `internal/telemetry` installs OpenTelemetry providers and the Pyroscope profiler from standard environment variables.
- Consumer packages accept narrow interfaces only where production and test adapters both exist. Constructors return concrete structs.
