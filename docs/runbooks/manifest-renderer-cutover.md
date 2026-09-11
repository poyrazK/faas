# Manifest-renderer cutover — issue #911 / PR-7 (the last leaf of ADR-110)

The cutover from a legacy single-box installation to the split-box
world defined by
[`docs/adr/110-declarative-split-box-manifest.md`](../adr/110-declarative-split-box-manifest.md).
The 12 PRs that shipped before PR-7 (#912 / #913 / #914 / #915 /
#917 / #918 / #919 / #920 / #921 / #922 / #923 / #924) put the
**code** in place; this runbook is the operator narrative that
closes the cluster.

> Cross-links:
> - Operator reference (one-page): [`docs/ops/gregalectl-operator-quickstart.md`](../ops/gregalectl-operator-quickstart.md).
> - Multi-host horizontal-scale variant (2nd compute node): [`docs/runbooks/multi-host-rollout.md`](multi-host-rollout.md).
> - Active-passive HA topology (Tier A8, accepted 2026-08-16): [`ADR-083`](../adr/083-active-passive-ha-topology.md).
> - The split-box manifest schema: [`deploy/manifest/examples/splitbox.example.yaml`](../../deploy/manifest/examples/splitbox.example.yaml).

## Topology

Two hosts:

| FQDN | Role | Daemons (per `bootstrap.yml` plays) |
|---|---|---|
| `fsn-1` | control-plane | apid, schedd, meterd, gatewayd-public, githubd, postgres, postgres_backup |
| `fsn-2` | compute-only | schedd, vmmd, gatewayd-internal, builderd, imaged |

The cross-box mesh is the per-box `ansible_host` value living in the
manifest-generated host vars (Tailscale / Wireguard / internal LAN —
operator choice per fleet). The production inventory has no combined
`[box]` group; every host is assigned exactly one role.

## Pre-conditions

> [!CAUTION]
> All six checks must produce expected output before starting the
> cutover. Stop on first failure; fix the pre-condition and re-run.

1. **`gregalectl doctor` is on PATH** (cross-link to quickstart):
   ```
   gregalectl version
   ```
   expect: version string; binary at `/usr/local/bin/gregalectl` (or
   operator-supplied location).

2. **Legacy single-box daemons are stopped** (do not race the
   renderer):
   ```
   systemctl stop faas-apid faas-schedd faas-vmmd faas-gatewayd-public faas-gatewayd-internal
   systemctl status faas-apid faas-schedd faas-vmmd faas-gatewayd-public faas-gatewayd-internal
   ```
   expect: `inactive (dead)` for each.

3. **The split-box manifest is in place**:
   ```
   test -f deploy/manifest/splitbox.yaml
   ```
   expect: exit 0. The cluster ships a reference at
   `deploy/manifest/examples/splitbox.example.yaml`; copy + edit
   `nodes.host` / `nodes.fleet` / `release.git_sha` for the actual
   box FQDNs + git SHA.

4. **The manifest validates**:
   ```
   gregalectl manifest validate --file deploy/manifest/splitbox.yaml
   ```
   expect: exit 0.

5. **The release bundle is staged** (per host's `/opt/faas/releases/`):
   ```
   test -d /opt/faas/releases/<git-sha>/bin
   ```
   expect: dir exists. The bundle ships `apid` / `schedd` /
   `gatewayd-public` / `gatewayd-internal` / `vmmd` / `builderd` /
   `imaged` / `meterd` / `githubd` + `release-manifest.json`.

6. **The legacy `/etc/faas/*.toml` tree is backed up**:
   ```
   sudo tar czf /var/backups/faas-legacy-toml-$(date -u +%Y%m%dT%H%M%SZ).tar.gz /etc/faas/
   ```
   expect: non-zero archive size. The renderer overwrites the
   canonical `/etc/faas/*.toml` files; the backup is the
   "roll back to legacy single-box" path.

## Procedure

Each step is idempotent — re-running a step that already completed
must be a no-op.

### 1. Bootstrap the control-plane box (fsn-1)

```
sudo make bootstrap-control-plane
```

Runs `bootstrap.yml --limit control_plane` against the inventory.
Applies the shared preconditions + control-plane-only roles
(`postgres`, `postgres_backup`, `control_plane_service`,
`gatewayd_public_service`, `githubd_service`, `log_archive`).
Idempotent — second run is zero `changed`.

### 2. Bootstrap the compute-only box (fsn-2)

```
sudo make bootstrap-compute
```

Runs `bootstrap.yml --limit compute_nodes`. Applies the shared
preconditions + compute-only roles (`vmmd_service`,
`gatewayd_internal_service`, `compute_only_service`, `log_archive`).

### 3. Render the manifest (dry-run, then for-real)

Per box. `--dry-run` prints the rendered TOML / systemd / cgroup / PKI
trees without touching disk:

```
gregalectl manifest render \
    --manifest-file deploy/manifest/splitbox.yaml \
    --host fsn-1 \
    --dry-run
```

expect: dry-run prints the planned writes; exit 0. Drop `--dry-run`
to apply:

```
gregalectl manifest render \
    --manifest-file deploy/manifest/splitbox.yaml \
    --host fsn-1
```

Repeat with `--host fsn-2` on fsn-2.

### 4. Boot the PKI subset

Per-box, role-scoped:

```
# fsn-1
gregalectl pki init --box-role control-plane

# fsn-2
gregalectl pki init --box-role compute-only
```

expect: each box drops the per-role cert + key chain under
`/etc/faas/tls/` (apid / schedd / githubd on fsn-1; schedd plus
vmmd-client certs on fsn-2). Mode 0400 root:root per §11.

### 5. Initialize host.age

On fsn-1:

```
gregalectl host-age init
```

Copy `/etc/faas/host.age` + `/etc/faas/host.age.previous` (the
overlap key) to fsn-2 over the cross-box mesh. Mode 0400 root:root on
both boxes.

### 6. Provision the cosign sign-key pair

On fsn-1:

```
gregalectl sign-keys init
```

Copy `/etc/faas/secrets/cosign.key` + `/etc/faas/secrets/cosign.pub`
to fsn-2 over the cross-box mesh. The sign-key signs the OCI image
digests at build time; the verify-key is what the runners +
provisional-publisher list hold.

### 7. Initialize the per-node vmmd key

On fsn-2:

```
gregalectl node-key init
```

expect: `/etc/faas/secrets/vmmd/node.key` + `.pub` mode 0400.
fsn-1 reads the public key on first cross-box handshake.

### 8. Wire the off-host rclone config

On fsn-1:

```
gregalectl backup init
gregalectl backup unseal-rclone \
    --age-identity /etc/faas/host.age \
    --in /etc/faas/sealed/rclone.conf.age \
    --out /etc/faas/rclone.conf
```

expect: `/etc/faas/rclone.conf` mode 0400 root:root, the
`off_host_backup` remote ready for `pg_basebackup` push.

### 9. Install the release bundle

```
# Both boxes. Pass the deployment identity, not the cloud-provider VM name.
# For a compute-only box, the installer records the canonical <name>.faas key
# used by vmmd. FAAS_NODE_NAME is normally set by the rendered systemd
# drop-in; pass it explicitly in a shell if it is not exported here.
gregalectl release install --git-sha <git-sha> --node "${FAAS_NODE_NAME:-<manifest-host-name>}" \
    --role "${FAAS_BOX_ROLE}"
```

expect: each box flips `/opt/faas/current` to the new SHA, the
daemon binaries land under `/opt/faas/releases/<sha>/bin/`, the
manifest row inserts into `release_bundles`, and
`compute_nodes.release_id` writes the new SHA on the local upsert.

### 10. Run the doctor

Per box:

```
gregalectl doctor --node fsn-1
gregalectl doctor --node fsn-2
```

expect: exit 0 on each, `Counts.Error == 0`. Then with `--deep` to
re-hash the on-disk daemon binaries against the bundle row:

```
gregalectl doctor --node fsn-1 --deep
gregalectl doctor --node fsn-2 --deep
```

expect: exit 0 on each. `--deep` is the bit-identical check to
production.

### 11. Leak-check

```
sudo make leakcheck
```

expect: zero leaked netns / TAPs / jail uids / cgroups. The
release-bundle installer is supposed to leave the host as clean as
it found it.

### 12. End-to-end smoke

```
gregale deploy --app hello-faas
curl http://hello-faas.gregale.dev/
```

expect: HTTP 200 with the cluster-shipped `hello-faas` response.

## Validation matrix

| Step | Exit | Log signature |
|---|---|---|
| 1 (`bootstrap-control-plane`) | 0 | `PLAY RECAP … failed=0` |
| 2 (`bootstrap-compute`) | 0 | `PLAY RECAP … failed=0` |
| 3 (render) | 0 | `wrote /etc/faas/{apid,schedd,vmmd}.toml` |
| 4 (pki init) | 0 | `wrote /etc/faas/tls/{ca,role}/` |
| 5 (host-age init) | 0 | `wrote /etc/faas/host.age` |
| 6 (sign-keys init) | 0 | `wrote /etc/faas/secrets/cosign.{key,pub}` |
| 7 (node-key init) | 0 | `wrote /etc/faas/secrets/vmmd/node.key` |
| 8 (backup init) | 0 | `wrote /etc/faas/rclone.conf` |
| 9 (release install) | 0 | `flipped /opt/faas/current → <sha>` |
| 10 (doctor) | 0 | `Counts.Error == 0` |
| 11 (leakcheck) | 0 | `0 leaked netns / TAPs / jail uids / cgroups` |
| 12 (deploy + curl) | 0 / 200 | `wrote deployment id; HTTP 200` |

## Rollback

`gregalectl release install --git-sha <previous-sha>` restores the
previous release atomically (per `pkg/releaseinstall/install.go` →
`AtomicFlip`). The doctor re-reads the symlink target and validates
the on-disk bundle against the new SHA.

If the cutover itself fails before step 9 (no release install yet),
the legacy single-box binary tree is still on disk at
`/opt/faas/releases/<legacy-sha>/bin/`. The legacy backup tombstones
under `/var/backups/faas-legacy-toml-*.tar.gz` restore the legacy
`/etc/faas/*.toml` tree:

```
sudo tar xzf /var/backups/faas-legacy-toml-<date>.tar.gz -C /
sudo systemctl start faas-apid faas-schedd faas-vmmd faas-gatewayd-public faas-gatewayd-internal
```

## Acceptance

- `gregalectl doctor --node fsn-1` exit 0.
- `gregalectl doctor --node fsn-2` exit 0.
- `gregalectl doctor --node fsn-1 --deep` exit 0.
- `gregalectl doctor --node fsn-2 --deep` exit 0.
- `make leakcheck` exit 0.
- `gregale deploy` + cold-wake to the test app returns HTTP 200.
- Zero manual `/etc/faas/*.toml` edits.
- Zero direct SQL repairs.
- Zero ad-hoc binary copies.

## Follow-ups (not in this runbook)

Listed because an operator reading this will hit them:

- **PR-X `gregalectl secrets init`** — the per-secret bootstrap
  (env-var → canonical path) is still in the tombstoned
  `deploy/controlplane/bootstrap.sh`. A separate PR adds the
  `gregalectl` verb + the four parallel ansible roles. Until then,
  step 5 / 6 / 7 / 8 use the v0 sealed.env path.
- **#650 typed-state-machine** for `cd-controlplane.yml` — separate
  PR per ADR-075 / ADR-078. The workflow is now a release-bundle
  controller (PR-1 rewire); the typed-state-machine swap is
  incremental.
- **Metal harness (`make metal-lima-splitbox`)** — PR-7 ships the
  two-role Lima harness that runs the issue #911 acceptance chain
  on a developer laptop. Use that for the pre-prod rehearsal of
  this cutover before doing it on a real box.
