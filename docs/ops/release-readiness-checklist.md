# Pre-1.0 Release Candidate & Fleet Operational Runbook

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

The deployment workflows consume these published assets; they do not rebuild
daemon binaries. After the release workflow is green, deploy the exact tag in
Step 4. The workflows verify the release checksum, Cosign identity, embedded
release manifest, and production-manifest hash before staging anything on a
host.

---

## Fleet Deployment & Installation

### Step 4: Deploy the Signed Release to the Fleet

Use the CD workflows for production installation. The control-plane workflow
downloads and verifies the signed assets, checks the embedded release identity,
installs the immutable bundle, runs migrations, and activates the release:

```bash
RELEASE_TAG=v0.1.18-rc.1
gh workflow run cd-controlplane.yml --ref main --field release_tag="$RELEASE_TAG"
```

After the control plane is healthy, dispatch `cd-compute.yml` once for each
compute node that should be active. Prefer a signed fleet enrollment bundle or
a checked-in `ComputeNodeClaim`. For a legacy rollout of an already-enrolled
node, provide the current provider address and its pinned SSH host-key
fingerprint:

```bash
gh workflow run cd-compute.yml --ref main \
  --field release_tag="$RELEASE_TAG" \
  --field node=fsn-2 \
  --field ssh_host="$FSN_2_SSH_HOST" \
  --field ssh_user=root \
  --field ssh_host_key_sha256="$FSN_2_SSH_HOST_KEY_SHA256"
```

The old `gregalectl release install --manifest --bundle --cosign-bundle
--apply-symlink` command is not a supported CLI shape. For an air-gapped local
install, use `gregalectl release install --git-sha=<40-hex-sha>
--tarball-path=<path>` after staging `release.cosign.bundle` and
`release.sbom.json` beside the tarball, as documented by
`gregalectl release --help`.

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
- `nodes`: Every node intended to serve traffic reports active status with a
  matching `release_id` and `manifest_hash`. Intentionally stopped capacity
  remains drained or inactive.
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

Verify the public liveness endpoint through Cloudflare:

```bash
curl -fsS https://api.gregale.dev/healthz
```

The public router does not expose a platform `/readyz`; a request to
`https://api.gregale.dev/readyz` is interpreted as an app route. Run deep
dependency checks on each host's loopback control listeners instead:

```bash
# Control plane: apid, schedd, gatewayd-public, githubd, meterd.
for port in 9101 9103 9092 8083 9106; do
  curl -fsS "http://127.0.0.1:${port}/readyz"
done

# Each compute node: builderd, imaged, vmmd, gatewayd-internal.
for port in 9105 9102 9104 9090; do
  curl -fsS "http://127.0.0.1:${port}/readyz"
done
```

All probes must return HTTP 200. The compute data listener also exposes a
shallow `http://127.0.0.1:8080/healthz` check.

### 3. SSD Snapshot-Restore Performance Gate

Run at least 100 controlled park-to-restore cycles on the reference SSD compute
node under normal traffic and bursts within host capacity. The release target
is p95 **below 350 ms** for the platform interval from
`wake.boot_started.at` through the matching `wake.boot_completed.at`.
Corroborate it with `wake.restore_breakdown.total_ms` and require every sample
to report a snapshot restore with no cold-boot fallback.

Public request, first-byte, application execution, Cloudflare, client network,
and physical-distance timings are diagnostics. They do not pass or fail the
350 ms platform restore gate. Do not combine HDD-node samples with the SSD
acceptance cohort. Preserve the raw event rows, percentile calculation, node
identity, disk rotational flag, release ID, and load shape with the release
evidence. See [SSD snapshot restore performance](snapshot-restore-performance.md)
for the measurement boundary and evidence format.

### 4. Backup & Restore Validation (Rclone)
Verify automated PostgreSQL basebackup and WAL archiving:
```bash
# Verify both timers and the newest local backup:
systemctl is-active faas-pg-basebackup.timer faas-pg-basebackup-push.timer
ls -1dt /var/lib/pgsql/basebackup/basebackup-* | head -1

# Verify both provider-neutral off-host destinations:
rclone lsd offhostbox:faas-pg-wal \
  --config /etc/faas/secrets/storage-box/rclone.conf
rclone lsd offhostbox:faas-pg-basebackup \
  --config /etc/faas/secrets/storage-box/rclone.conf

# Pull the newest off-host basebackup into a throwaway PostgreSQL cluster,
# replay WAL, and compare critical row counts:
sudo bash deploy/scripts/pg-restore-verify.sh
```

The destructive M8 live restore drill is a separate, explicitly scheduled
operation. Record its measured RPO/RTO in `docs/drills/`; do not describe an
`rclone check` as a restore test.

### 5. Cloudflare DNS & TLS Verification
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
