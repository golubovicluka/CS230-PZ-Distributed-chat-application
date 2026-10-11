# DevOps roadmap

This roadmap maps the 65-tool DevOps list onto this project: which tools are adopted, what job each one does here, and in which phase it lands.

## Status

| Phase | Scope | Status |
| --- | --- | --- |
| 0 | App readiness: Postgres, LB eviction, drain, origins, reconnect | Done |
| 1 | Containers, Compose, Colima, just, mise, linters, gitleaks, Renovate, OpenTelemetry, Pyroscope | Done |
| 2 | CI with Dagger, act, SBOM, scanning, signing | Done |
| 3 to 9 | Features, Kubernetes, SRE, delivery, security, IaC, stretch | Planned |

Notes from implementing Phases 0 and 1:

- The Go toolchain moved to 1.27 (module minimum 1.26) because current pgx, go-redis and OpenTelemetry releases require Go 1.26 and Go 1.24 is out of support.
- pnpm is 12.x; native build scripts (`@swc/core`, `@tailwindcss/oxide`, `esbuild`) are approved explicitly in `chat-app/pnpm-workspace.yaml`.
- Frontend major upgrades (Vite 8, TypeScript 7, ESLint 10) were intentionally left for Renovate to propose separately.
- Compose uses an nginx gateway as the single public origin, mirroring the role Envoy Gateway takes in Phase 4.

Notes from implementing Phase 2:

- **Pinning:** in March 2026 Trivy was compromised (CVE-2026-33634): a malicious v0.69.4 was published and most `aquasecurity/trivy-action` tags were force-pushed to credential-stealing code. Every action is therefore pinned to a commit SHA and every tool image to a digest. `trivy-action` is not used; Trivy runs as a digest-pinned image inside Dagger.
- **SBOM formats:** the CVE gate uses a Syft-native SBOM with Go function symbols, because SPDX does not carry them. The published and attested SBOM is SPDX.
- **Web image user:** it now declares `USER 101:101` explicitly, which Trivy's DS-0002 requires and which Kubernetes `runAsNonRoot` needs in Phase 4.
- **actionlint:** added as a check. It is not on the original list, but it catches workflow mistakes that yamllint cannot.
- **First real CI run:** the Grype gate failed the build on GO-2026-6612 (CVE-2026-78663), a critical HTTP/2 flow-control bypass in `golang.org/x/net` v0.59.0, pulled in indirectly through gRPC and the OTLP exporters. The fix was a bump to v0.61.0. The same run exposed a telemetry test that Dagger's injected `OTEL_EXPORTER_OTLP_*` variables could break; it now isolates its environment.

## Context

Starting state: one Go 1.24 module with `cmd/chatserver` (Gorilla WebSocket, SQLite via CGO `mattn/go-sqlite3`, Redis pub/sub fan-out, self-registers with the LB), `cmd/loadbalancer` (in-memory least-loaded registry: `POST /register`, `POST /update`, `GET /get`), and a React 19 + Vite 7 + Tailwind 4 SPA in `chat-app/` (npm). There are no Dockerfiles, CI, Kubernetes manifests, IaC, telemetry or authentication.

Goal: add tools from the 65-item list so the app can scale and be operated. Constraints: macOS with 24 GB RAM, Envoy replaces the custom LB inside Kubernetes, and three new app features (Keycloak auth, OpenFGA rooms, virus-scanned attachments).

### Defects that blocked scaling

| Defect in the starting code | Resolution |
| --- | --- |
| History was partitioned: each server stored only its own clients' messages in its own SQLite file, so clients on different servers saw different history. | Fixed in Phase 0: one shared PostgreSQL store. |
| The LB never evicted servers: a crashed server kept its last, usually lowest, load and kept receiving new clients. | Fixed in Phase 0: heartbeats, a 30s TTL and explicit deregistration. |
| The frontend hardcoded `http://localhost:9000/get` and rebuilt the history URL from a 4-digit port regex, which broke behind any proxy. | Fixed in Phase 0: relative URLs and runtime `/config.js`. |
| No authentication: identity came from `?username=`, `CheckOrigin` always returned true and CORS was `*`. | Origins fixed in Phase 0 (allowlist plus same-origin). Real auth arrives in Phase 3. |
| No client reconnect, so every rollout, scale-down or chaos experiment dropped users for good. | Fixed in Phase 0: jittered backoff, re-discovery and history reload. |
| `github.com/go-redis/redis/v8` was the old import path; `redisotel` targets v9. | Fixed in Phase 0. |
| `mattn/go-sqlite3` needed CGO, blocking static multi-arch images and Tilt live update from macOS. | Fixed in Phase 0: SQLite removed, `CGO_ENABLED=0` distroless images. |

### Scope warning

65 tools on a 3-binary app is breadth over depth. At the defense, each tool has to answer "what failure or question does this address in this app". Every kept tool below has a concrete job; tools without one are skipped. If time runs short, Phases 0, 1, 2, 4 and 5 are the defensible core. Phases 6 to 9 are additive.

## Decisions

Constraints chosen up front:

- macOS: Colima (`--vm-type vz --vz-rosetta`) runs Docker Engine. Docker CLI, buildx and compose plugins come from Homebrew. Podman is skipped.
- Kubernetes: Envoy Gateway balances chatserver pods with a `LeastRequest` BackendTrafficPolicy (WebSockets are long-lived, so this approximates least-connections). The Go LB stays for Compose mode only (`loadbalancer.enabled=false` in the chart).
- Features: Keycloak + Mailpit, OpenFGA rooms, attachments (GCS emulator + Pub/Sub emulator + ClamAV).
- 24 GB RAM: Colima VM gets 14 GB / 6 CPU. The platform is split into profiles that are never all on at once.

Further decisions:

- Postgres only. SQLite is removed. Driver `pgx/v5`, migrations embedded with `goose`.
- kind (1 control plane + 2 workers) over k3d: it is upstream-conformant and is what the Istio, Envoy Gateway and Chaos Mesh docs test on.
- Argo CD owns the platform. In dev, Tilt owns the app namespace. In "gitops mode", Argo CD owns the app too. Never both at once.
- `mise.toml` pins CLI versions (not on your list) because Homebrew can't pin about 30 CLIs. Renovate updates it.
- No Bitnami charts or images (Broadcom moved them to paid or legacy in Aug to Sep 2025). Use official images and charts.
- The unexplained asterisks are treated as "preferred where it fits".

## Item verdicts

Phase numbers refer to the phases below.

| # | Pick | Verdict | Job in this project |
| --- | --- | --- | --- |
| 1 | kind | Add P4 | 3-node cluster, `extraPortMappings` 80/443, zot as containerd mirror |
| 2 | Engine, Buildx, Compose | Add P1 | distroless multi-arch images via `docker buildx bake`, `compose.yaml` with profiles |
| 3 | Colima | Add P1 | Docker Engine VM on macOS; Podman skipped |
| 4 | Tilt | Add P4 | inner loop on kind: build to zot, `live_update` Go binaries and SPA assets |
| 5 | Dagger (Go SDK) | Add P2 | the CI pipeline itself; GitHub Actions, Forgejo and `just` only call it |
| 6 | just | Add P1 | single entrypoint for every workflow |
| 7 | act | Add P2 | run `.github/workflows/ci.yaml` locally against the Colima socket |
| 8 | Forgejo + Runner | Stretch P9 | Compose profile mirror; the runner executes the same Dagger pipeline, which proves CI portability |
| 9 | Renovate | Add P1 | gomod, pnpm, Dockerfile, Helm, Actions, OpenTofu, mise |
| 10 | OpenTofu | Add P4, P8 | `infra/local` (Argo CD bootstrap, OpenBao seeding), `infra/gcp` plan-only prod blueprint |
| 11 | TFLint | Add P8 | google + terraform rulesets |
| 12 | Checkov | Add P8 | HCL, Dockerfiles, rendered Helm, workflows |
| 13 | Infracost | Add P8 | monthly cost of the GCP blueprint, posted as a PR comment |
| 14 | Helm, kubectl | Add P4 | `deploy/helm/chat` chart |
| 15 | Gateway API, Envoy Gateway, Envoy | Add P4 | north-south TLS, WebSocket routes, least-request LB, rate limit |
| 16 | Istio ambient | Add P7 | ztunnel mTLS east-west, waypoint + AuthorizationPolicy (only chatserver/scanner SAs reach Postgres/OpenFGA) |
| 17 | cert-manager | Add P4 | local CA issuer, wildcard `*.chat.localhost`; `just trust-ca` adds it to the macOS keychain |
| 18 | External Secrets | Add P4 | sync DB, Keycloak client and OpenFGA secrets from OpenBao (Vault-compatible provider) |
| 19 | OpenBao | Add P4 | secrets backend, Kubernetes auth for ESO |
| 20 | Coraza | Add P7 | OWASP CRS on the gateway via EnvoyExtensionPolicy; inspects HTTP and the upgrade request, not WS frames |
| 21 | CoreDNS | Add P4 | rewrite `*.chat.localhost` to the Envoy service so pods see the same OIDC issuer URL as browsers; PowerDNS skipped |
| 22 | nginx | Add P1 | serves the built SPA, single origin + path routing in Compose; Varnish skipped |
| 23 | Postgres, CloudNativePG | Add P0, P4 | shared store for messages, rooms and attachments; CNPG 2 instances + barman backups |
| 24 | MongoDB / FerretDB | Skip | no data that Postgres doesn't already hold well; a second database would be hard to justify at the defense |
| 25 | Pub/Sub emulator | Add P3 | `attachment.uploaded` events to the scanner (at-least-once with ack, in contrast to Redis at-most-once fan-out) |
| 26 | GCS emulator (fake-gcs-server) | Add P3 | attachment blobs; Firebase emulator skipped (it overlaps Keycloak, GCS and Pub/Sub) |
| 27 | Floci | Add P6 | S3 target for CNPG and Velero backups; MiniStack as fallback |
| 28 | Mailpit | Add P3 | Keycloak verify and reset emails, Alertmanager email receiver |
| 29 | OpenFGA + fga CLI | Add P3 | room owner/admin/member relations, attachments inherit from their room; `fga model test` in CI |
| 30 | Keycloak | Add P3 | OIDC PKCE for the SPA, realm import JSON, test users; Zitadel skipped |
| 31 | ClamAV | Add P3 | clamd INSTREAM scan in `cmd/scanner`; e2e test with the EICAR test file |
| 32 | OTel Collector + SDKs | Add P1 (SDK), P5 (Collector) | traces with `traceparent` carried inside Redis messages, plus metrics and logs (`otelslog`) |
| 33 | Prometheus, Alertmanager | Add P5 | kube-prometheus-stack with OTLP ingestion |
| 34 | Grafana | Add P1, P5 | dashboards as code |
| 35 | Loki | Add P5 | logs |
| 36 | Tempo | Add P5 | traces |
| 37 | Pyroscope | Add P1 (SDK), P5 | `pyroscope-go` push, span profiles |
| 38 | grafana/otel-lgtm | Add P1 | Compose observability backend in one container |
| 39 | SigNoz | Skip | duplicates 33 to 38 |
| 40 | kube-state-metrics, node-exporter, cAdvisor | Add P5 | from kube-prometheus-stack (cAdvisor via kubelet) |
| 41 | blackbox exporter, Uptime Kuma | Add P3 (Kuma in Compose), P5 (blackbox) | probe `/healthz` and the WS upgrade (expect 101) |
| 42 | Pyrra | Add P5 | SLO CRDs: WS handshake availability 99.5%, fan-out p99 latency; Sloth skipped |
| 43 | k6 | Add P5 | WS load through the gateway, measuring cross-server delivery latency; drives the KEDA demo |
| 44 | Chaos Mesh | Add P6 | kill chatserver pods, partition chatserver from Redis, add Postgres latency, fail over the CNPG primary; Litmus skipped |
| 45 | Toxiproxy | Add P3 | Compose profile with latency or cuts between chatservers and Redis/Postgres |
| 46 | OpenCost | Add P8 | per-namespace cost with custom pricing set to the GCP blueprint's rates |
| 47 | Argo CD | Add P4 | app-of-apps for the platform, plus gitops mode; Flux skipped |
| 48 | Argo Rollouts | Add P6 | chatserver canary through the Gateway API plugin with a Prometheus AnalysisTemplate (caveat: only new WS connections shift) |
| 49 | Kyverno | Add P7 | resource limits, non-root, no `:latest`, verifyImages for cosign; Kyverno CLI runs in CI on the rendered chart |
| 50 | Crossplane | Skip (stretch P9) | overlaps OpenTofu + Argo CD; the only honest use is a `ChatTenant` composition |
| 51 | Backstage | Partial P9 | `catalog-info.yaml` only; a running instance isn't worth about 2 GB here |
| 52 | KEDA | Add P5 | chatserver on connections per pod (Prometheus scaler), scanner on pending rows (PostgreSQL scaler) |
| 53 | Velero | Add P6 | namespace backups to Floci, plus a restore drill |
| 54 | vcluster | Stretch P9 | per-branch preview environment inside kind |
| 55 | Headlamp, k9s | Add P4 | cluster UIs |
| 56 | zot | Add P4 | kind registry mirror for Tilt, holds cosign signatures locally; Harbor skipped |
| 57 | Trivy, Syft, Grype | Add P2, P7 | Syft makes the SBOM, Grype gates on it, Trivy checks Dockerfile misconfig and scans the cluster with `trivy k8s` |
| 58 | cosign | Add P2 | keyless signing + SBOM attestation in GitHub Actions |
| 59 | gitleaks | Add P1 | pre-commit hook + CI |
| 60 | Falco | Add P7 | `modern_ebpf` driver, Falcosidekick webhook to ntfy; best effort on the Colima kernel |
| 61 | kube-bench | Add P7 | Job; report saved under `docs/security/` |
| 62 | buf | Add P3 | proto contracts for the WS envelope and the Connect RPC APIs; `buf lint`, `buf breaking`, Go + TS codegen |
| 63 | golangci-lint, shellcheck, yamllint, markdownlint | Add P1 | linters |
| 64 | Playwright, pnpm, Node | Add P1 (pnpm, Node 24 LTS), P3 (Playwright) | two-user, cross-server e2e |
| 65 | ntfy | Add P5 | Alertmanager (via the ntfy-alertmanager bridge), Argo CD notifications, Falco, CI failures; Mattermost skipped |

Result: 61 of 65 numbers get at least one pick in core phases. Item 39 is an alternative to 38. Items 24 and 50 are skipped on purpose. Items 8, 51 and 54 are stretch.

## Target layout

```text
cmd/{chatserver,loadbalancer,scanner}/
internal/{chat,postgres,registry,loadbalancer,httpapi,telemetry,authn,authz,attachments}/
proto/chat/v1/*.proto  buf.yaml  buf.gen.yaml  gen/go/
chat-app/ (pnpm, Dockerfile, nginx/, src/gen/)
build/Dockerfile  docker-bake.hcl  compose.yaml
deploy/kind/  deploy/argocd/  deploy/platform/<component>/  deploy/helm/chat/
deploy/policies/  deploy/chaos/
infra/local/  infra/gcp/  .tflint.hcl  infracost.yml
ci/ (Dagger module)  .github/workflows/{ci,release}.yaml
auth/keycloak/realm-chat.json  authz/{model.fga,store.fga.yaml}
observability/{dashboards,rules}/
tests/e2e/ (Playwright)  tests/load/ (k6)
justfile  mise.toml  Tiltfile  renovate.json
.golangci.yml  .yamllint.yaml  .markdownlint-cli2.yaml  .gitleaks.toml
```

## Phases

Each phase leaves the project runnable and lands as its own pull request.

### Phase 0: App readiness (Go + React)

- `internal/postgres`: pgx v5 pool, embedded goose migrations, `Save`/`Recent` that satisfy the existing `chat.MessageSaver` and `chat.HistoryReader` seams (`internal/chat/hub.go:13`, `internal/chat/handler.go:21`). Delete `internal/sqlite`. Store tests use `TEST_DATABASE_URL` and are skipped when it is unset. Hub and handler tests keep their fakes.
- Migrate to `github.com/redis/go-redis/v9` in `internal/chat/redis.go` and `cmd/chatserver/main.go`.
- `cmd/chatserver`:
  - flags fall back to env (`DATABASE_URL`, `REDIS_ADDR`, `REGISTRY_URL`; empty disables registration)
  - `/healthz` and `/readyz` (Redis + DB ping)
  - SIGTERM drain: mark not-ready, close sockets with 1001 Going Away, then shut down
  - the advertise value accepts a path such as `/cs/1` for proxied mode
- `log` replaced by `log/slog` JSON in both binaries.
- LB: servers heartbeat via `POST /update` every 10s and entries expire after 30s; add `/healthz` and eviction tests in `internal/loadbalancer/loadbalancer_test.go`.
- CORS and `CheckOrigin` use an allowlist flag instead of `*` / always-true.
- SPA:
  - relative URLs only
  - runtime config from `/config.js` (`mode: "lb" | "direct"`)
  - reconnect with exponential backoff + jitter
  - remove the port regex
- Verify: `go test -race ./...`, `go vet ./...`, `pnpm lint && pnpm build`. Manual: 2 servers + LB, kill one server, confirm new clients stop going to it within 30s and history is identical on both servers.

### Phase 1: Containers, local loop, hygiene

- `just colima` runs `colima start --vm-type vz --vz-rosetta --cpu 6 --memory 14 --disk 120`.
- Images:
  - `build/Dockerfile` with targets for chatserver, loadbalancer and scanner: `CGO_ENABLED=0`, distroless static nonroot
  - `chat-app/Dockerfile`: pnpm build, then nginx-unprivileged with `nginx/default.conf.template`
  - `docker-bake.hcl` for linux/arm64 + amd64
- `compose.yaml`: postgres, redis, loadbalancer, chatserver-1/2, web, plus otel-lgtm (profile `obs`). nginx routes `/api/lb/` to the LB and `/cs/N/` to each chatserver, so the custom LB keeps working behind one origin.
- Telemetry:
  - `internal/telemetry`: OTLP traces, metrics and logs; `otelhttp`, `redisotel`, `otelpgx`
  - W3C `traceparent` added to the message envelope, so one trace spans sender, Redis and receivers
  - `pyroscope-go` push
- npm to pnpm (`packageManager` field). Node 24 LTS now; Node 26 enters LTS on 2026-10-28 and Renovate will propose the bump.
- `mise.toml`, `justfile` (`up`, `down`, `lint`, `test`, `e2e`, `cluster`, `dev`, `load`, `chaos`, `trust-ca`, `doctor`, `hooks`).
- Lint configs: `.golangci.yml` (v2 format), `.yamllint.yaml`, `.markdownlint-cli2.yaml`, shellcheck on `scripts/`, `.gitleaks.toml`.
- `renovate.json` (the Renovate GitHub App must be enabled on the repo).
- Verify: `just lint`, `just up`, open <http://localhost:8080> and chat across both servers. Grafana (:3000) shows one trace crossing chatserver-1, Redis and chatserver-2.

### Phase 2: CI with Dagger

- `ci/` Dagger Go module with these functions:
  - `Lint` and `Test` (Postgres + Redis as Dagger services)
  - `Build` (arm64 + amd64)
  - `Sbom` (Syft) and `Scan` (Grype fails on high+, Trivy config)
  - `Publish` (ghcr.io)
  - `Proto` and `Policy` (added in later phases)
- `.github/workflows/ci.yaml` (PRs) and `release.yaml` (main: publish, `cosign sign` keyless, `cosign attest` SBOM; `id-token: write`). Workflows only install Dagger and call functions.
- `just act` runs act with the Colima `DOCKER_HOST`; publish and sign steps are skipped under `env.ACT`.
- Verify: `dagger call test`, `just act`, then green checks on the PR.

### Phase 3: New features and their tools (Compose first)

- buf:
  - `proto/chat/v1/{events,rooms,attachments}.proto`, `buf.yaml`, `buf.gen.yaml`
  - Go output (connect-go) to `gen/go`, TS output (protobuf-es / connect-es) to `chat-app/src/gen`
  - WS frames become protojson `ClientEvent` / `ServerEvent`, which also removes the nested-JSON workaround in `websocket.ts`
- Keycloak:
  - `quay.io/keycloak/keycloak` with realm import `auth/keycloak/realm-chat.json`, SMTP to Mailpit
  - SPA uses oidc-client-ts PKCE
  - Go `internal/authn` checks the issuer against the public URL and fetches JWKS from the internal URL (the usual Docker issuer pitfall)
  - WebSocket auth uses a single-use 30s ticket stored in Redis (`POST /api/ws-ticket`), because browsers can't send headers on WS
- OpenFGA:
  - `authz/model.fga` and `authz/store.fga.yaml` with tests, Postgres datastore in a separate database
  - Go SDK checks on join, send, history and download; ListObjects for the room list
- Rooms: a `rooms` table, `room_id` on messages, fan-out filtered by the rooms each server's clients are in.
- Attachments:
  1. Multipart upload to fake-gcs-server
  2. Row stored with `status=pending`
  3. Event published to a Pub/Sub emulator topic
  4. `cmd/scanner` pulls the event, streams the blob to clamd and updates the status
  5. A status event goes out on Redis to clients
  6. Download requires an FGA check and `status=clean`
- Compose profiles: `auth`, `files`, `chaos` (Toxiproxy in front of Redis/Postgres), `uptime` (Uptime Kuma), `mail`.
- Playwright `tests/e2e`:
  - alice and bob on different servers exchange messages
  - a non-member is denied access to a room
  - an EICAR upload is marked infected
- Verify: `just up --full && just e2e`, `fga model test`, `buf lint`, `buf breaking --against .git#branch=main`.

### Phase 4: Kubernetes platform on kind

- `deploy/kind/cluster.yaml`: 1 control plane + 2 workers, ports 80/443 mapped to Envoy NodePorts, containerd mirror pointing at a zot container on the kind network. Created by `just cluster`.
- `infra/local` (OpenTofu): `helm_release` for Argo CD, root Application, and OpenBao setup (kv mount, policies, Kubernetes auth, seeded secrets) via the Vault provider.
- `deploy/argocd/root.yaml`: app-of-apps with profile toggles. `deploy/platform/*` contains:
  - Gateway API CRDs (installed once, standard channel), Envoy Gateway
  - cert-manager + CA ClusterIssuer, CoreDNS patch
  - CNPG operator, OpenBao (dev mode), ESO + ClusterSecretStore
  - Keycloak, OpenFGA, Mailpit, fake-gcs, Pub/Sub emulator, ClamAV
  - Redis (official image manifest), Headlamp
- `deploy/helm/chat` chart:
  - chatserver, web and scanner workloads
  - CNPG `Cluster` (2 instances), ExternalSecrets
  - Gateway + HTTPRoutes for `chat.localhost` and `auth.chat.localhost`, BackendTrafficPolicy `LeastRequest`
  - PDB, topology spread, probes, preStop drain
- `Tiltfile`: `docker_build` to zot, deploy the Helm chart, `live_update` with a cross-compiled Go binary sync.
- `just trust-ca` exports the CA and adds it to the macOS keychain.
- Verify: `just cluster && just platform && just dev`, then <https://chat.localhost> loads and logs in. Delete a chatserver pod and the client reconnects. `kubectl cnpg status` is healthy and the ExternalSecrets are Ready.

### Phase 5: Observability and SRE in Kubernetes

- Backends: kube-prometheus-stack (OTLP receiver on), Loki, Tempo and Pyroscope (single-binary modes).
- OTel Collector Deployment routes OTLP to each backend. Dashboards come from `observability/dashboards/`.
- blackbox exporter Probes, Pyrra `ServiceLevelObjective`s in the chart.
- Alerts: Alertmanager to the ntfy-alertmanager bridge to ntfy, plus Mailpit email.
- KEDA ScaledObjects for chatserver (Prometheus) and scanner (PostgreSQL).
- `tests/load/ws-fanout.js` (k6 `k6/websockets`) with Prometheus remote-write output; test users come from Keycloak direct grant in a test-only client.
- Verify: `just load` makes KEDA scale chatserver from 2 to N, Pyrra shows the error budget burn, and an alert reaches the ntfy app.

### Phase 6: Delivery and resilience

- Argo Rollouts + Gateway API traffic-router plugin. chatserver becomes a `Rollout` with canary steps 10/50/100 and an AnalysisTemplate on handshake 5xx; KEDA retargets to the Rollout.
- Chaos Mesh experiments in `deploy/chaos/*.yaml`, run with `just chaos <name>`. The expected outcome of each is documented.
- Floci in the cluster. CNPG backs up to Floci S3 with the barman-cloud plugin, and Velero (AWS plugin, path-style) runs a schedule. `just restore-drill` exercises both.
- Argo CD notifications go to ntfy.
- Verify:
  - a rollout with a deliberately broken image aborts on its own
  - chaos runs either hold the SLO or show the expected burn
  - the restore drill brings back a deleted namespace and does Postgres point-in-time recovery

### Phase 7: Security

- Istio ambient: label the app and Envoy Gateway namespaces, add a waypoint and AuthorizationPolicies.
- Coraza with CRS on the gateway, detection-only first, then blocking.
- Kyverno policies in `deploy/policies/`, plus a `Policy` Dagger function running the Kyverno CLI against `helm template`.
- Falco + Falcosidekick to ntfy, a kube-bench Job, and a `trivy k8s` report.
- Verify:
  - a SQL injection probe gets a 403
  - an unsigned image is rejected
  - `kubectl exec` into chatserver triggers a Falco alert in ntfy
  - a pod outside the allowed service accounts can't reach Postgres

### Phase 8: IaC blueprint and FinOps

- `infra/gcp` (plan-only, never applied) maps each local stand-in to its managed GCP service:

  | Local | GCP |
  | --- | --- |
  | kind | GKE Autopilot |
  | CNPG | Cloud SQL (HA) |
  | Redis | Memorystore |
  | fake-gcs-server | GCS |
  | Pub/Sub emulator | Pub/Sub |
  | Coraza | Cloud Armor |
  | OpenBao | Secret Manager |
  | zot | Artifact Registry |

- Tooling: `.tflint.hcl` with the google plugin, a Checkov config, `infracost.yml` + a usage file, and a Dagger `Iac` function. Infracost posts a PR comment; it needs a free `INFRACOST_API_KEY` GitHub secret.
- OpenCost with custom pricing.
- Verify: `tofu validate`, `tflint` and `checkov` pass (or have documented skips), and `infracost breakdown --path infra/gcp` prints a monthly estimate.

### Phase 9: Stretch

- Forgejo + runner as a Compose profile running `.forgejo/workflows/ci.yaml`, which calls Dagger.
- `just preview <branch>` with vcluster.
- `catalog-info.yaml` for Backstage.
- Crossplane `ChatTenant` only if there is time left.

## Resource profiles (Colima VM 14 GB)

These are estimates; I'm not confident in them until measured with `kubectl top`.

| Profile | Contents | Approx. RAM |
| --- | --- | --- |
| core | kind, Envoy Gateway, cert-manager, CNPG, Redis, Argo CD, app | 5 GB |
| features | Keycloak, OpenFGA, ClamAV, emulators, Mailpit | 2.5 GB |
| obs | kube-prometheus-stack, Loki, Tempo, Pyroscope, Collector | 3.5 GB |
| mesh | Istio ambient | 0.6 GB |
| security | Kyverno, Falco | 1 GB |
| resilience | Rollouts, Chaos Mesh, Velero, Floci | 0.8 GB |

core + features + obs fits, plus one more profile at a time.

## Risks to verify during implementation

- Coraza: Envoy Gateway 1.8+ documents a dynamic-module path. I need an arm64 build of the module; fallback is coraza-proxy-wasm.
- ClamAV: arm64 support of the official image is unclear. Check with `docker buildx imagetools inspect`; Rosetta amd64 is the fallback.
- Istio ambient on kind inside the Colima VM: istio-cni and ztunnel kernel prerequisites.
- Gateway API CRD versions: Envoy Gateway 1.8 pairs with Gateway API v1.5.1. Pick one version compatible with Istio 1.31 and disable chart-managed CRDs.
- Floci S3 compatibility with barman-cloud and Velero/Kopia multipart uploads. Fallback is MiniStack.
- k6 `k6/websockets` is still labeled experimental in the docs.
- OpenBao dev mode is in-memory, so `just secrets` re-seeds it after a restart.
- Tool versions checked today: Tilt v0.37.8, Dagger v0.21.10, Envoy Gateway v1.8, Istio 1.31, otel-lgtm v0.29.x, Pyrra v0.10.x. Everything else is pinned in `mise.toml` at implementation time.

## Verification

- Without Docker:
  - `go test -race ./...`, `golangci-lint run`
  - `pnpm lint && pnpm build`
  - `buf lint`, `fga model test`
  - `helm template | kyverno apply`, `checkov`
  - `tofu validate`, `tflint`
  - `docker compose config`, plus `dagger call` if a Docker daemon is available
- On your Mac:
  - `just doctor`, then `just up && just e2e`
  - `just cluster && just platform && just dev`
  - `just load`, `just chaos pod-kill`, `just restore-drill`
  - check Grafana and ntfy
