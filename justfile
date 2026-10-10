set shell := ["bash", "-euo", "pipefail", "-c"]

pnpm := "pnpm --dir chat-app"
local_db := "postgres://chat:chat@127.0.0.1:5432/chat?sslmode=disable"
otel_env := "OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-lgtm:4318 PYROSCOPE_SERVER_ADDRESS=http://otel-lgtm:4040"

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
