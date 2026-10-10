# Local development

## macOS with Colima

Docker Desktop is not required. Colima runs Docker Engine inside a lightweight Lima VM.

1. Install host tools and pinned CLIs:

   ```bash
   brew bundle
   mise install
   ```

2. Let the Docker CLI find the Homebrew plugins. Add this to `~/.docker/config.json`:

   ```json
   {
     "cliPluginsExtraDirs": ["/opt/homebrew/lib/docker/cli-plugins"]
   }
   ```

3. Start the VM:

   ```bash
   just colima
   ```

   The default size is 6 CPUs, 14 GB RAM and a 120 GB disk, which leaves about 10 GB for macOS on a 24 GB machine. Override it with `COLIMA_CPU`, `COLIMA_MEMORY` and `COLIMA_DISK`. On Apple Silicon the script uses the `vz` hypervisor with Rosetta, so `linux/amd64` images also run.

4. Check everything is wired up:

   ```bash
   just doctor
   ```

Colima keeps the `colima` Docker context active, so `docker`, `docker compose` and `docker buildx` talk to the VM. Ports published by Compose on `127.0.0.1` are forwarded to the Mac automatically.

## Linux

Install Docker Engine with the `docker-buildx-plugin` and `docker-compose-plugin` packages from Docker's apt or dnf repository, then run `mise install`. Colima is not needed.

## Day-to-day loop

| Goal | Command |
| --- | --- |
| Whole stack in containers | `just up`, then <http://localhost:8080> |
| With Grafana and telemetry | `just up-obs`, then <http://localhost:3000> |
| Backend on the host, deps in containers | `just deps`, `just dev-lb`, `just dev-server 8080`, `just dev-server 8081`, `just dev-web` |
| Tests | `just test`, `just test-integration` |
| Everything CI will lint | `just lint` |

The Vite dev server proxies `/lb` to `LB_URL` (default `http://127.0.0.1:9000`) and `/api` to `CHAT_API_URL` (default `http://127.0.0.1:8080`). The SPA reads its runtime settings from `/config.js`. In containers the `web` image renders that file from the `CHAT_DISCOVERY`, `CHAT_LB_URL`, `CHAT_HISTORY_URL` and `CHAT_WS_PATH` environment variables.

## Troubleshooting

- **Port already in use.** Compose binds 5432, 6379, 8080, and with `obs` also 3000, 4317, 4318 and 4040, on `127.0.0.1`. Stop local Postgres or Redis, or change the left-hand port in `compose.yaml`.
- **`toomanyrequests` from Docker Hub.** Anonymous pulls are rate-limited. Run `docker login`, or wait and retry.
- **Browser stays on "Reconnecting...".** Run `just smoke`. If `/lb/get` fails, no chat server has registered; check `just logs chatserver-1 loadbalancer`.
- **WebSocket closes with 403.** The page origin is neither the server's own host nor in `CHAT_ALLOWED_ORIGINS`.
