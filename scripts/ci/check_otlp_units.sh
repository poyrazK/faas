#!/usr/bin/env bash
# Ensure every instrumented production daemon consumes the shared optional
# OTLP configuration. The check is intentionally static: the actual secret
# file is operator-owned and must never be present in the repository.

set -euo pipefail

root="${1:-$(git rev-parse --show-toplevel)}"
units=(
  "deploy/ansible/roles/control_plane_service/files/faas-apid.service"
  "deploy/ansible/roles/control_plane_service/files/faas-schedd.service"
  "deploy/ansible/roles/control_plane_service/files/faas-meterd.service"
  "deploy/ansible/roles/gatewayd_public_service/files/faas-gatewayd-public.service"
  "deploy/ansible/roles/githubd_service/files/faas-githubd.service"
  "deploy/ansible/roles/vmmd_service/files/faas-vmmd.service"
  "deploy/ansible/roles/gatewayd_internal_service/files/faas-gatewayd-internal.service"
  "deploy/ansible/roles/builderd_service/files/faas-builderd.service"
  "deploy/ansible/roles/compute_only_service/files/faas-imaged.service"
)

errors=0
for rel in "${units[@]}"; do
  path="${root}/${rel}"
  if [[ ! -f "$path" ]]; then
    echo "otlp-unit-check: missing ${rel}" >&2
    errors=$((errors + 1))
    continue
  fi
  if ! grep -Fqx 'EnvironmentFile=-/etc/faas/otel.env' "$path"; then
    echo "otlp-unit-check: ${rel} does not load the optional OTLP env file" >&2
    errors=$((errors + 1))
  fi
  if grep -Eq '^[[:space:]]*Environment=OTEL_EXPORTER_OTLP_(HEADERS|CLIENT_KEY)=' "$path"; then
    echo "otlp-unit-check: ${rel} contains inline OTLP credential material" >&2
    errors=$((errors + 1))
  fi
done

example="${root}/deploy/ansible/roles/otlp_exporter/files/otel.env.example"
if [[ ! -f "$example" ]]; then
  echo "otlp-unit-check: missing otel.env.example" >&2
  errors=$((errors + 1))
fi

if ((errors > 0)); then
  echo "otlp-unit-check: FAIL (${errors} finding(s))" >&2
  exit 1
fi

echo "otlp-unit-check: OK (${#units[@]} instrumented units)"
