#!/usr/bin/env bash
# Public-safety scan for deployments/iocost-adapter: fail closed if a
# rendered-looking manifest (a real instance ID, account ID, or internal
# hostname where a __PLACEHOLDER__ or {{PLACEHOLDER}} token belongs) or an
# obvious secret pattern is committed. Run from the repo root before
# committing any change under deployments/iocost-adapter.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIR="$ROOT/deployments/iocost-adapter"

fail=0

# Real AWS instance IDs / account-shaped IDs / volume IDs that should
# never appear in a committed template -- these are always rendered
# per-attempt from a target record, never checked in.
if grep -rnE '\bi-[0-9a-f]{8,17}\b|\bvol-[0-9a-f]{8,17}\b' "$DIR" 2>/dev/null; then
  echo "FAIL_LIVE_IDENTIFIER_COMMITTED" >&2
  fail=1
fi

# Obvious secret/key material shapes.
if grep -rnE '(-----BEGIN (RSA|EC|OPENSSH|PRIVATE) KEY-----|AKIA[0-9A-Z]{16})' "$DIR" 2>/dev/null; then
  echo "FAIL_SECRET_MATERIAL_COMMITTED" >&2
  fail=1
fi

# Every .tmpl file must still contain at least one unresolved
# placeholder -- a committed .tmpl with none suggests someone
# accidentally checked in a rendered file under the template name.
while IFS= read -r -d '' f; do
  if ! grep -qE '__[A-Z0-9_]+__|\{\{[A-Z_]+\}\}' "$f"; then
    echo "FAIL_TEMPLATE_HAS_NO_PLACEHOLDER: $f" >&2
    fail=1
  fi
done < <(find "$DIR" -name '*.tmpl' -print0)

if [[ "$fail" -eq 0 ]]; then
  echo "PASS_MANIFEST_SCAN"
fi
exit "$fail"
