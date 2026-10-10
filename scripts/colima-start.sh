#!/usr/bin/env bash
# Starts (or reports) the Colima VM that provides Docker Engine on macOS.
# Sized for the full local stack; override with COLIMA_CPU/MEMORY/DISK.
set -euo pipefail

cpu="${COLIMA_CPU:-6}"
memory="${COLIMA_MEMORY:-14}"
disk="${COLIMA_DISK:-120}"

if ! command -v colima >/dev/null 2>&1; then
  echo "colima is not installed. Run: brew bundle" >&2
  exit 1
fi

if colima status >/dev/null 2>&1; then
  echo "Colima is already running:"
  colima list
  exit 0
fi

vm_args=(--cpu "$cpu" --memory "$memory" --disk "$disk")
if [[ "$(uname -s)" == "Darwin" && "$(uname -m)" == "arm64" ]]; then
  vm_args+=(--vm-type vz --vz-rosetta --mount-type virtiofs)
fi

colima start "${vm_args[@]}"
docker context use colima >/dev/null
docker info --format 'Docker Engine {{.ServerVersion}} on {{.OperatingSystem}} ({{.NCPU}} CPU, {{.MemTotal}} bytes)'
