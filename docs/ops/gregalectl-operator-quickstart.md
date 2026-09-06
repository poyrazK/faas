# `gregalectl` operator quickstart

One-page operator reference for the operator-side binary. The
customer-side binary is `gregale` (sealed to the customer CLI surface
since PR-6.5). For the full first-time cutover, see
[`docs/runbooks/manifest-renderer-cutover.md`](../runbooks/manifest-renderer-cutover.md).

> Cross-links:
> - The split-box manifest schema: [`deploy/manifest/examples/splitbox.example.yaml`](../../deploy/manifest/examples/splitbox.example.yaml).
> - The cutover runbook (covers the per-host bootstrap chain + the
>   PR-X `secrets init` gap): [`docs/runbooks/manifest-renderer-cutover.md`](../runbooks/manifest-renderer-cutover.md).
> - The cluster architecture: [`docs/adr/110-declarative-split-box-manifest.md`](../adr/110-declarative-split-box-manifest.md).
> - The PR-6.5 atomic split: [`docs/adr/110-declarative-split-box-manifest.md#pr-65-atomic-split`](../adr/110-declarative-split-box-manifest.md).

## 1. Install `gregalectl`

Curl + SHA-256 pin (per `pkg/webhook/sealbytes-namespace.md`):

```
curl -fsSL https://dl.gregale.dev/cli/gregalectl/<git-sha>/gregalectl.linux-amd64 \
    -o /usr/local/bin/gregalectl
echo "<sha256-of-binary>  /usr/local/bin/gregalectl" | sha256sum -c -
chmod 0755 /usr/local/bin/gregalectl
```

The `<git-sha>` and `<sha256-of-binary>` come from the release
artifact published by `cd-controlplane.yml` (PR-1 rewired CD). The
artifact is bit-identical to the per-release `bin/gregalectl` under
`/opt/faas/releases/<sha>/bin/`.

## 2. First-boot narrative (one verb per concern)

Once the binary is on `$PATH` and the manifest is rendered, the
**bootstrap chain** seeds every on-disk credential in a fixed sequence so
later steps can assume their inputs exist. Each verb is independent —
an operator re-running any of them out of order is almost always a
mistake (init refuses overwrite by default; the rotate variant is the
intended replacement path).

```
# 1. Local PKI (per-box CA + 9+ per-daemon leaves)
sudo gregalectl pki init --root-dir /etc/faas/tls

# 2. cosign keypair (image signing; PR-3 image-rollout gate)
sudo gregalectl sign-keys init \
    --sign-key /etc/faas/secrets/sign.key \
    --verify-key /etc/faas/secrets/sign-pub.pem

# 3. Per-node CapacityReport signing keypair (ADR-053)
sudo gregalectl node-key init

# 4. host.age keypair (session encryption; sealed at rest)
sudo gregalectl host-age init

# 5. Backup-credentials stub (operator-side rclone.conf + archive-creds.json)
sudo gregalectl backup init

# 6. Unseal rclone + archive credentials (one-shot; reads from ansible-vault-encrypted bundle)
sudo gregalectl backup unseal-rclone --bundle <vault-bundle.tar.age>
sudo gregalectl backup unseal-archive-creds --bundle <vault-bundle.tar.age>

# 7. Post-bootstrap secrets batch (5 files; PR-X / issue #911)
sudo gregalectl secrets init --pg-dsn "$FAAS_PG_DSN"

# 8. Install the release bundle on the local box
sudo gregalectl release install --git-sha $(git rev-parse HEAD) --role control-plane
```

The order is load-bearing:

- `pki init` must precede `manifest render` (the renderer writes per-box PKI leaves under `/etc/faas/tls/<dir>/`).
- `sign-keys init` must precede `release install` (the install path's `Verify` re-hashes the cosign pub into the `release_bundles.sign_pub_sha256` column).
- `secrets init` must precede the first `gregale deploy` (the gateway refuses to start with `host.age` missing).
- `backup unseal-*` reads the bundle the ansible role dropped at `/var/lib/faas/vault/`; running it before `backup init` fails with `ErrVaultBundleMissing`.

Each verb has a `--json` flag (where applicable) so CI gates can assert
the bootstrap chain ran end-to-end without parsing human output.

## 3. Manifest workflow

The manifest is the declarative source of truth for the cluster.
`gregalectl` ships three verbs that read or render it.

### `manifest validate --file <yaml>`

Schema + cross-key checks. Run before every commit that touches the
manifest and inside the CI gate `make manifest-validate`. Exit 0 on
success, exit 3 on schema violation (the report names the field).

### `manifest render --manifest-file <yaml> --host $(hostname)`

Materialises `/etc/faas/*.toml`, systemd units, cgroup subtree_control,
and the per-box PKI leaves. `--dry-run` prints the planned writes
without touching disk (use this for `make metal-lima-splitbox`).

### `manifest ansible --manifest-file <yaml> [--output-dir DIR]`

Generates `deploy/ansible/.generated/inventory/hosts.ini` + the
`host_vars/<fqdn>.yml` tree. Consumed by:

- `make manifest-ansible MANIFEST=deploy/manifest/splitbox.yaml`
- `make ANSIBLE_INVENTORY=deploy/ansible/.generated/inventory/hosts.ini bootstrap-control-plane`
- `make ANSIBLE_INVENTORY=deploy/ansible/.generated/inventory/hosts.ini bootstrap-compute`

The `--force` flag is refused by default — a re-run on a dirty tree
is operator error. The generated inventory owns `ansible_host`, node
identity, private service aliases, and `faas_vmmd_target_url`; a new
bare-metal host therefore requires a manifest change and regeneration,
not a hand-edited IP in the repository.

## 4. Release workflow

### `release bundle --bin-dir <dir> --git-sha <sha> --manifest-hash sha256:<hex>`

Materialises a release bundle from a pre-built `bin/` directory and
INSERTs the `release_bundles` row. Run by CI on every tag (the
`release.yml` workflow).

### `release install --git-sha <sha> [--role control-plane|compute-only]`

Installs a release on the local box. Flips `/opt/faas/current` to the
new SHA, lands the daemons under `/opt/faas/releases/<sha>/bin/`,
writes the `release_bundles` row, records each daemon in the
`daemon_deployments` ledger, and UPSERTs `compute_nodes.release_id`.
Use `--reason "..."` to leave incident or change-ticket context in that
ledger.

`--role` is dual-purpose:

- **First-boot**: templates drop-ins + starts the role subset.
- **Day-2 mutation** (PR-B / ADR-113): triggers drain-gate →
  `Mutate(stop+start)` → role UPSERT on a running box with a
  different existing role.

Reads `/etc/faas/first-boot.env`'s `FAAS_BOX_ROLE` when `--role` is
unset.

### `release kgv rotate --git-sha <sha> [--from-zero]`

Refreshes the `sbom-baseline.json` (operator escape hatch from
ADR-113's fail-closed SBoM gate). The KGV is the "known good version"
baseline the install path compares against; `rotate` re-stamps it from
the on-disk release SBoM. `--from-zero` writes `KGVZero` (zero
CRITICAL/HIGH) without parsing the on-disk SBoM.

### `release kgv init`

Alias for `release kgv rotate --from-zero`. Prints a deprecation line
on stdout. Will be removed once operators stop muscle-memorying the
old name; the dispatcher refuses new code paths to call it.

### `release history [--daemon <name>] [--limit <n>]`

Reads the durable `daemon_deployments` ledger and shows who attempted each
daemon release, when it started and finished, and whether it succeeded or
failed. The output also includes the local `/opt/faas/current` pointer so a
host-side activation can be reconciled with its database history. Add
`--json` for automation.

### `release inspect <sha>`

Read-only inspection of one `release_bundles` row. Reports whether the
release directory exists, whether its manifest can be read, and whether the
release is the current symlink target. This is safe to run during an
incident and does not activate or remove anything.

## 5. Day-2 fleet ops

### Compute node state machine

```
# Pre-register a new compute-only box
gregalectl compute-nodes add \
    --name fsn-2 \
    --target-url tcp://vmmd-2.faas:50051 \
    --gateway-target-url tcp://fsn-2.gregale.dev:8080 \
    --vpcpus 32 --mem-mb 65536 --max-concurrency 200

# List every registered node (--json for CI gates)
gregalectl compute-nodes list [--active-only] [--json]

# Show one node's row + live_instance_count
gregalectl compute-nodes show --node fsn-2 [--json]

# Drain before a reboot
gregalectl compute-nodes drain --node fsn-2
gregalectl compute-nodes drain-status --node fsn-2   # exit 1 if live instances remain

# Re-activate
gregalectl compute-nodes activate --node fsn-2

# Force-drain a stuck node (operator-acknowledged)
gregalectl compute-nodes force-drain --node fsn-2 --yes
```

`list` / `show` are read-only introspection added in Cluster C1
(gregalectl mega-PR). The state package owns the underlying
`ListComputeNodes` / `ComputeNodeByName` calls; the dispatcher never
bypasses the schema.

`target_url` is the VM manager endpoint. `gateway_target_url` is the
separate private HTTP data-plane endpoint; the manifest/Ansible pipeline
derives it from the node hostname, so normal node joins do not require a
second hand-written address.

### Fleet topology coordinator

```
# Add a node to the fleet: write host_vars + hosts.ini + git commit +
# ssh bootstrap + POST compute_nodes
gregalectl deploy add-node \
    --role compute-only \
    --ansible-host fsn-2.example.com \
    --public-iface eth0 \
    --masquerade-cidr 10.244.2.0/24 \
    --target-url tcp://vmmd-2.faas:50051 \
    --yes
```

Closes multi-host scale-out gap #2 (companion to `compute-nodes add`
which closes gap #1). The pre-flight prompt lists every side-effect;
`--yes` is the unattended-mode acknowledgement.

### host.age rotation

```
gregalectl host-age rotate           # rotates current → previous, generates a new current
gregalectl host-age status --json    # current: {path, mode, mtime, sha256, key_id}; previous: {…|null}
gregalectl host-age prune-previous [--dry-run] [--json]  # removes the previous file when safe
```

`prune-previous` is the load-bearing CI gate for `make metal-lima-splitbox`
— the `--dry-run` lets CI validate prune safety without mutating. The
JSON shape includes `would_prune: <bool>` + a `kept: [{path, reason}]`
array so gates can branch on individual kept siblings.

### PKI introspection + rotation

```
gregalectl pki init --root-dir /etc/faas/tls
gregalectl pki status
gregalectl pki list [--daemon <name>] [--box-role <role>] [--json]
gregalectl pki rotate --daemon <name> [--box-role <role>] [--root-dir DIR] [--force]
```

`pki list` (added in Cluster C2) emits a stable wire shape
`{box_role, daemon, ca:{present,path,mode,serial,not_after}, leaves:[{directory,filename,cn,sans,…}]}`
so CI gates can introspect without parsing the human renderer. Missing
files report `present=false`; paths are always echoed so operators can
see WHAT would be inspected.

### cosign keypair (sign-keys)

```
gregalectl sign-keys init --sign-key <p> --verify-key <p>
gregalectl sign-keys status [--json]
gregalectl sign-keys rotate [--keep-old-pub] [--json] [--force]
```

`--keep-old-pub` archives the existing pub to `<path>.<unix-ts>` BEFORE
the new keypair is generated — let verifier-side mid-rotation re-pin
the old pub without re-running rotate. The `--json` report includes
`kept_old_pub`, `old_pub_sha256`, `new_pub_sha256`, `key_id` (the
first 16 hex chars of the new pub's SHA-256) so audit logs can quote
a short fingerprint.

### Per-node keypair (node-key)

```
gregalectl node-key init
gregalectl node-key rotate
gregalectl node-key status
```

Used by `vmmd` to sign per-node CapacityReports (ADR-053). Path
defaults to `/etc/faas/secrets/node-{priv,pub}.pem`.

## 6. Backup / secrets ops

### Backup credentials

```
gregalectl backup init                  # create /etc/faas/secrets/storage-box/ stub (0700 root:root)
gregalectl backup unseal-rclone --bundle <vault-bundle.tar.age>
gregalectl backup unseal-archive-creds --bundle <vault-bundle.tar.age>
```

`init` creates the directory stub the unseal verbs expect and emits
the two known placeholders that `doctor` already detects. Refuses
overwrite unless `--force`.

### Post-bootstrap secrets batch

```
gregalectl secrets init --pg-dsn "$FAAS_PG_DSN"   # 5 files: host.age, session.key, box-age-key, rclone.conf, archive-creds.json
gregalectl secrets stamp --host <fqdn>            # stamp the existing vmmd TLS certificate without rotating
gregalectl secrets rotate --host <fqdn>           # delegates to host-age rotate
gregalectl secrets status --json                  # mode/mtime/sha256 for all 5
```

The 5-file batch replaces v1 `bootstrap.sh` step 11d (RETIRED
2026-08-15). When database stamping is enabled, the two compute-node
attestation columns contain the public vmmd mTLS leaf and its canonical
`sha256:` DER fingerprint. `--no-db` skips that write for file-only/local
bootstrap flows.

## 7. Diagnostic

```
gregalectl doctor
```

Exit codes:

| Code | Meaning |
|---|---|
| 0 | Healthy — no error findings |
| 1 | Usage error (mutually-exclusive flag combo) |
| 3 | Drift detected — findings in the report |

Flags:

- `--node NAME` — filter to a single `compute_nodes.name` row.
- `--release SHA` — filter to a single `release_bundles.git_sha`.
- `--deep` — re-hash on-disk daemon binaries against the bundle
  row. Slow on large fleets; the per-box check is the same as the
  release-bundle install path's `Verify`.
- `--fail-on {warn,error}` — exit non-zero threshold (default
  `error`).
- `--json` — machine-readable report.

**Never panics on bad input.** A malformed TOML or unreadable file
emits a WARN finding and continues with the defensive default; exit
3 reflects accumulated findings, not a signal-killed process.

## 8. Shell integration

```
# Generate a shell completion script and source it
gregalectl completion bash > /etc/bash_completion.d/gregalectl
gregalectl completion zsh  > "${fpath[1]}/_gregalectl"

# Generate the man page (or per-command page)
gregalectl man                  # gregalectl(1)
gregalectl man pki              # gregalectl-pki(1)
gregalectl man host-age rotate  # gregalectl-host-age-rotate(1)
```

The completion script is generated from `cli_meta.go` (Cluster A3
fixes a long-standing drift between the comment header and the
`cliCommands` slice). The manifest-drift guard
`commands_completion_test.go::TestCompletion_ManifestDrift` fails CI
when `main.go`'s dispatcher diverges from `cli_meta.go`.

## Trusted-publishers (note)

`trusted-publishers add|remove|list` is dispatched by `gregalectl` per
ADR-058 deviation note in `main.go:15`, but the on-disk
`/etc/faas/secrets/trusted-publishers/<name>.pem` writes still happen
from the customer-side `gregale` binary. The ADR-058 follow-up
("operator-vs-customer split") is filed separately and out of scope for
this PR.

## Where to go next

- **First-time cutover from a legacy single-box?** Read
  [`docs/runbooks/manifest-renderer-cutover.md`](../runbooks/manifest-renderer-cutover.md).
- **Adding a second compute node to a working split-box fleet?**
  Read [`docs/runbooks/multi-host-rollout.md`](../runbooks/multi-host-rollout.md).
- **Troubleshooting drift?** Run `gregalectl doctor --deep` and
  read the JSON report; the `target` field on each finding points
  at the object (node name, git_sha, daemon name).
- **host-age rotation details?** Read
  [`docs/ops/host-age-rotation.md`](host-age-rotation.md).
- **Release-bundle anchor (PR-3 / ADR-113)?** Read
  [`docs/ops/release-manifest-anchor.md`](release-manifest-anchor.md).
- **Per-secret rotation cadence?** Read
  [`docs/ops/secrets-rotation.md`](secrets-rotation.md).
