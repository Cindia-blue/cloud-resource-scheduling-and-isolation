#!/usr/bin/env bash
# Build and test the disk-only IOCost components this fork actually
# maintains: pkg/iocostadapter, pkg/iocostintent, pkg/targetrecord,
# cmd/iocost-adapter, cmd/render-target, cmd/render-capability-record.
# Then build/vet/test the scheduler-plugin nested module separately --
# it pins its own k8s.io/kubernetes version line (see
# scheduler-plugin/go.mod) and is intentionally not part of this
# module's dependency graph. Run from the repo root.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

PACKAGES=(
  ./pkg/iocostadapter/...
  ./pkg/iocostintent/...
  ./pkg/targetrecord/...
  ./cmd/iocost-adapter/...
  ./cmd/render-target/...
  ./cmd/render-capability-record/...
  ./cmd/gate-wait/...
)

echo "== go build =="
go build "${PACKAGES[@]}"

echo "== go vet =="
go vet "${PACKAGES[@]}"

echo "== go test =="
go test -count=1 "${PACKAGES[@]}"

echo "== scheduler-plugin (nested module, own k8s.io/kubernetes v1.34.9 pin) =="
(
  cd scheduler-plugin
  go build ./...
  go vet ./...
  go test -count=1 ./...
)

echo "PASS_DISK_ONLY_COMPONENT_BUILD"
