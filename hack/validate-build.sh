#!/usr/bin/env bash
# Build and test the disk-only IOCost components this fork actually
# maintains: pkg/iocostadapter, pkg/iocostintent, pkg/targetrecord,
# cmd/iocost-adapter, cmd/render-target. Run from the repo root.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

PACKAGES=(
  ./pkg/iocostadapter/...
  ./pkg/iocostintent/...
  ./pkg/targetrecord/...
  ./cmd/iocost-adapter/...
  ./cmd/render-target/...
)

echo "== go build =="
go build "${PACKAGES[@]}"

echo "== go vet =="
go vet "${PACKAGES[@]}"

echo "== go test =="
go test -count=1 "${PACKAGES[@]}"

echo "PASS_DISK_ONLY_COMPONENT_BUILD"
