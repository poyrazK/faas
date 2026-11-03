# Runbook · FaasBaseProvenanceUnverified

> **Severity:** warn. **Component:** imaged. **Metric:** `imaged_base_provenance_verifications_total{base, result}`.
> **Check:** `BaseProvenanceVerifier.Check` in `pkg/imaged/base_provenance.go` (ADR-20261029155852).

## Symptom

imaged built, or tried to build, a platform base (a runtime base, base-minimal,
base-debian-parent, the builder base or an execution profile) whose SLSA
provenance attestation did not verify. What happened next depends on
`FAAS_IMAGED_BASE_PROVENANCE`:

- `observe` (the default): the base was staged anyway. Nothing is broken; the
  fleet cannot prove where that base came from.
- `enforce`: the base was not built. Bases already staged keep serving. A node
  rolled onto a release whose bases it refuses stays drained.

## Why

Before it builds a base, imaged fetches the digest's Sigstore bundle from the
registry and runs the host's cosign offline against the trusted root its
release shipped. The attestation must come from
`.github/workflows/images.yml` on `refs/heads/main`, signed on a GitHub-hosted
runner. The `result` label says what failed:

| result | Meaning |
| --- | --- |
| `missing` | The registry holds no attestation for the digest. The digest predates images.yml attestations (#5835) and is not in the frozen legacy list, or it was not built by images.yml on main |
| `rejected` | cosign refused every attestation (another workflow, branch or digest) or one was signed on a self-hosted runner |
| `unavailable` | The host has no cosign at `FAAS_COSIGN_BIN` (default `/usr/local/bin/cosign`), or the release has no `sigstore-trusted-root.json` beside the imaged binary |
| `error` | The registry or cosign failed or timed out, or the manifest bytes did not match the digest |

## Triage

1. Read the `imaged: base provenance unverified` log line on the node in the
   alert: `journalctl -u faas-imaged | grep 'base provenance'`. It names the
   ref, digest, mode and cosign's last error line (`detail`).
2. Run the same check without staging, from the node:

   ```
   /opt/faas/current/bin/imaged verify-base-provenance \
     --ref ghcr.io/poyrazk/<image>@sha256:<digest> \
     --legacy /opt/faas/current/bin/base-provenance-legacy.txt
   ```

   It prints one JSON line per ref and exits 0 only when every ref is
   `verified` or `legacy`.
3. Compare the digest with the release's pin:
   `grep <image> /etc/faas/runtime-bases.env`. A digest that differs from the
   signed release's `runtime-bases.env` was pinned by hand.

## Fix

- `missing` or `rejected` on a release pin: the release gate (#5895) should
  have refused it, so treat it as a supply-chain incident. Do not switch the
  check off; restore the signed release's `runtime-bases.env` and find who
  changed it.
- `missing` on an execution profile (`FAAS_EXECUTION_PYTHON_DATA_V1_BASE_REF`)
  or another operator-set ref: re-pin it to a digest images.yml built on main
  after #5835.
- `unavailable`: stage the release-pinned cosign (node join's "Stage the
  release cosign verifier" task) and check that the release directory holds
  `sigstore-trusted-root.json`.
- `error`: check the node's egress to the registry. The next build retries;
  only `verified` results are cached.

`FAAS_IMAGED_BASE_PROVENANCE=off` (variable `faas_imaged_base_provenance_mode`)
turns the check off. Use it only to unblock a rollout while you investigate,
and record why.
