# Pre-1.0 Release Candidate & Fleet Operational Runbook

The GitHub-controlled production approval and CI gate are documented in
[`docs/process/release.md`](../process/release.md). Complete that workflow
before using the host-level checks below.

This document defines the authoritative checklist and operational runbook for cutting a **pre-1.0 release candidate** (e.g. `v0.1.3-rc.1`), validating the split-box deployment manifest, signing release artifacts, installing bundles across production nodes, and executing verification gates.

> [!IMPORTANT]
> - Do **NOT** tag `1.0.0`. The platform is pre-1.0.
> - Production nodes must transition from temporary `live-e2e-*` builds to a verified, signed release bundle.
> - Preserved untracked operator files (`.claude/`, `.commandcode/`, `.repair-box-tables.sql`) must remain untouched.

---

## Pre-Release Gates & Manifest Anchoring

### Step 1: Run Automated Pre-Release Verification
Run the pre-release automated gate from the repository root. It materializes
the production manifest automatically from the topology template:
```bash
./scripts/pre-release-check.sh
```
This script asserts:
- Manifest schema validity via `gregalectl manifest validate`.
- The generated release identity matches the current commit.
- Clean execution of all unit tests (`./pkg/...`, `./cmd/...`, `./guest/...`).
- Successful local build of the canonical daemon tarball.
- Computes the exact manifest hash used by the signed release bundle.

### Step 2: Create and Push the Release Candidate Tag
Tag the approved release commit on `main` and push:
```bash
# Example tag: v0.1.3-rc.1
git tag -a v0.1.3-rc.1 -m "Release candidate v0.1.3-rc.1"
git push origin v0.1.3-rc.1
```

### Step 3: Monitor CI Build & Signing Pipeline
Track the release workflow in GitHub Actions (`.github/workflows/release.yml`):
- Cross-compilation of `gregale` CLI binaries.
- Creation of `release.tar.gz` and SPDX SBOM (`release.sbom.json`).
- Keyless signing with GitHub OIDC via Cosign/Rekor (`release.cosign.bundle`).
- Release publication with SHA256 checksums.

The control-plane CD workflow consumes these published assets; it does not
rebuild daemon binaries. After the release workflow is green, select the
exact tag when dispatching `cd-controlplane`:

```bash
gh workflow run cd-controlplane.yml --ref main -f release_tag=v0.1.18-rc.1
```

`cd-controlplane` first verifies a successful `ci.yml` run for the exact tag
commit, then pauses at the protected `production` environment for reviewer
approval. Dispatches are serialized, so a second release waits for the first
to finish instead of cancelling it.

The workflow verifies the release checksum, Cosign identity, embedded release
manifest, and production-manifest hash before staging anything on the host.

---

## Fleet Deployment & Installation

### Step 4: Install Release Bundle on Production Nodes
On both `fsn-1` (control-plane) and `fsn-2` (compute-only), install the signed release:
```bash
gregalectl release install \
  --manifest=/etc/faas/manifest.yaml \
  --bundle=/opt/faas/releases/v0.1.3-rc.1/release.tar.gz \
  --cosign-bundle=/opt/faas/releases/v0.1.3-rc.1/release.cosign.bundle \
  --apply-symlink
```

### Step 5: Execute Deep Diagnostic Checks
Verify that the on-disk tree, `release_bundles` table, and `compute_nodes` table are synchronized and healthy:
```bash
# Run local diagnostic:
gregalectl doctor

# Run full deep diagnostic across all cluster nodes:
gregalectl doctor --deep --database-dsn="$DATABASE_URL"
```
**Pass criteria**:
- `symlink`: `/opt/faas/current` points to the new release.
- `bundle`: Manifest and daemon binary hashes match.
- `lockstep`: Daemon counts match catalog expectations.
- `nodes`: Both `fsn-1` and `fsn-2` report active status with matching `release_id` and `manifest_hash`.
- `secrets`: On-disk credentials and certificates match fingerprints.
- `node-hashes`: Remote hashes match canonical bundle.

---

## External Operational Prerequisites

### 1. Host Certificate Fingerprints & mTLS Whitelist
Ensure mTLS node certificates are registered in `compute_nodes`:
```sql
SELECT name, role, active, cert_fingerprint, release_id, manifest_hash 
FROM compute_nodes 
ORDER BY name;
```

### 2. Dependency-Aware Readiness Verification
Verify both shallow liveness and deep readiness across the control-plane:
```bash
# Shallow liveness (no DB call):
curl -fsS https://api.gregale.dev/healthz

# Deep readiness (verifies PostgreSQL pool & connection acquisition):
curl -fsS https://api.gregale.dev/readyz
```

### 3. Backup & Restore Validation (Rclone)
Verify automated PostgreSQL basebackup and WAL archiving:
```bash
# Verify WAL destination is accessible:
rclone lsd faas-backups:gregale-pg-wal/

# Verify nightly snapshot restore dry-run:
rclone check /var/backups/faas faas-backups:gregale-pg-backups/
```

### 4. Cloudflare DNS & TLS Verification
Ensure the public wildcard `*.apps.gregale.dev` and `api.gregale.dev` resolve to the public edge IP, while private hostnames (`fsn-1.gregale.dev`, `fsn-2.gregale.dev`) are restricted to internal/managed `/etc/hosts` resolution.

---

## Rollback Runbook (Emergency Procedure)

If an anomaly is detected post-install:
1. Revert symlink to the previous working release:
   ```bash
   gregalectl release rollback --to=<previous_release_id>
   ```
2. Restart services across affected node:
   ```bash
   systemctl restart faas-*
   ```
3. Verify cluster recovery:
   ```bash
   gregalectl doctor --deep
   ```
