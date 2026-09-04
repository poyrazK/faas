#!/usr/bin/env bash
# Static gate for the committed production systemd units (issue #585 /
# ADR-127). sealed.env carries apid-only secrets (Stripe/Paddle/Google/GitHub
# OAuth + apid HMAC keys + dev token + statuspage + sbom). Loading it from a
# non-apid daemon leaks the secrets via /proc/<pid>/environ and core dumps.
#
# The gate walks both production unit trees (deploy/systemd/* and the
# ansible role files/* mirrors) and refuses any line of the form:
#
#   EnvironmentFile=/etc/faas/sealed.env
#   EnvironmentFile=-/etc/faas/sealed.env          (optional form — still a leak)
#
# The only exception is faas-apid.service, which legitimately consumes the
# full sealed.env. Every other daemon must use LoadCredential= or load a
# per-daemon /etc/faas/secrets/secrets.env (0440 root:faas) instead.
#
# Mirrors scripts/ci/check_systemd_hardening.sh's fail-loud shape (errs counter,
# stderr-only diagnostics, exit 1 on any finding).
set -euo pipefail

root="${1:-.}"

# Production unit paths to scan. ansible role files/* are byte-identical to
# deploy/systemd/ for the same daemon; both must stay aligned. Drop a path here
# when a daemon is decommissioned — the gate will then refuse to scan a stale
# file, surfacing the deletion.
unit_paths=(
  "${root}/deploy/systemd"
  "${root}/deploy/ansible/roles/control_plane_service/files"
  "${root}/deploy/ansible/roles/builderd_service/files"
  "${root}/deploy/ansible/roles/compute_only_service/files"
  "${root}/deploy/ansible/roles/githubd_service/files"
  "${root}/deploy/ansible/roles/gatewayd_internal_service/files"
  "${root}/deploy/ansible/roles/gatewayd_public_service/files"
  "${root}/deploy/ansible/roles/vmmd_service/files"
)

# Allowed forms. Anything else with /etc/faas/sealed.env is a violation.
# - apid is the only legitimate consumer (carries the full sealed.env).
# - We don't whitelist the optional (`-`) form anywhere; even on optional
#   units, the env inheritance is a leak.
# `rel` is the unit path with the leading `${root}/` stripped, so apid's
# two homes show up as either `deploy/systemd/faas-apid.service` or
# `deploy/ansible/roles/control_plane_service/files/faas-apid.service`.
allowlist_re='^(deploy/systemd/faas-apid\.service|deploy/ansible/roles/control_plane_service/files/faas-apid\.service)$'

errors=0

for base in "${unit_paths[@]}"; do
  if [[ ! -d "$base" ]]; then
    echo "sealed-env-scope-check: missing dir ${base}" >&2
    errors=$((errors + 1))
    continue
  fi
  # Match the exact directive form (with or without leading `-`). The leading
  # `^` + no-trailing-junk anchors reject a comment line that mentions
  # sealed.env; this gate is about active directives only.
  while IFS= read -r -d '' unit; do
    rel="${unit#"${root}"/}"
    # Required form: EnvironmentFile=/etc/faas/sealed.env
    if grep -Eiq '^[[:space:]]*EnvironmentFile=/etc/faas/sealed\.env([[:space:]]|$)' "$unit"; then
      if ! [[ "$rel" =~ $allowlist_re ]]; then
        echo "sealed-env-scope-check: ${rel}: EnvironmentFile=/etc/faas/sealed.env is forbidden in non-apid units (use LoadCredential= or /etc/faas/secrets/secrets.env 0440 root:faas)" >&2
        errors=$((errors + 1))
      fi
    fi
    # Optional form: EnvironmentFile=-/etc/faas/sealed.env (still inherits the
    # full env when the file is present; same tripwire).
    if grep -Eiq '^[[:space:]]*EnvironmentFile=-/etc/faas/sealed\.env([[:space:]]|$)' "$unit"; then
      echo "sealed-env-scope-check: ${rel}: EnvironmentFile=-/etc/faas/sealed.env (optional form) is forbidden — the env inherits when the file is present" >&2
      errors=$((errors + 1))
    fi
  done < <(find "$base" -maxdepth 1 -type f -name 'faas-*.service' -print0)
done

if (( errors > 0 )); then
  echo "sealed-env-scope-check: FAIL (${errors} finding(s))" >&2
  exit 1
fi
echo "sealed-env-scope-check: OK (only faas-apid.service loads sealed.env)"
