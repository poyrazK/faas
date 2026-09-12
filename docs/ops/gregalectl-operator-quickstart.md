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
writes the `release_bundles` row, and UPSERTs `compute_nodes.release_id`.

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

## 5. Day-2 fleet ops

### Compute node state machine

```
# Establish the cookie session used by provider mutations. Run this on the
# same control-plane host that will run deployctl; sessions are IP-bound.
gregalectl auth login --email operator@gregale.dev

# Pre-register a new compute-only box
gregalectl compute-nodes add \
    --name fsn-2 \
    --target-url tcp://vmmd-2.faas:50051 \
    --gateway-target-url tcp://fsn-2.gregale.dev:8080 \
    --vpcpus 32 --mem-mb 65536 --max-concurrency 200 \
    --admission-ceiling-mb 55705 \
    --reason fleet_expansion

# List every registered node (--json for CI gates)
gregalectl compute-nodes list [--active-only] [--json]

# Show one node's row + live_instance_count
gregalectl compute-nodes show --node fsn-2 [--json]

# Drain, verify the maintenance hold, then explicitly reactivate
gregalectl compute-nodes drain --node fsn-2 --reason planned_kernel_upgrade
gregalectl compute-nodes drain-status --node fsn-2   # exit 0 in maintenance/retired
gregalectl compute-nodes activate --node fsn-2 --reason planned_kernel_upgrade

# Permanent decommissioning: drain first, then retire with explicit approval
gregalectl auth step-up
gregalectl compute-nodes retire --node fsn-2 --reason hardware_eol --yes
```

`add`, `list`, and `show` use the authenticated operator API by default. Add
returns a trace ID and emits `operator.action.node_enroll`; re-enrollment
preserves PKI, release, topology, and routing metadata not present in the
request. Use `--defer-activation` to commit the row as unavailable until the
readiness workflow activates it.

The Operations console and `gregalectl` lifecycle commands use the same
authenticated API. Lifecycle mutations require an MFA-stepped-up operator
session and create a durable `operator_intents` receipt retaining actor,
reason, trace, preflight impact, and terminal outcome. Refresh the five-minute
proof with `gregalectl auth step-up`; inspect or revoke it with `auth status` /
`auth logout`.

`retired` is terminal and excluded from placement and automatic recovery. The
row stays available for audit; do not re-enroll replacement hardware under the
same name.

A successful drain finishes in the non-admitting `maintenance` lifecycle; it
never automatically reactivates the node. Direct database mutation exists only
as `--break-glass-db --yes` and is reserved for the reviewed
[`database-repair`](../break-glass/database-repair.md) procedure.

### Instance recovery

The destructive instance-recovery commands use the same operator session and
return only after their durable schedd intent reaches a terminal state:

```
gregalectl instances force-park --instance-id <uuid> --yes --reason incident_123
gregalectl instances force-cold-boot --app-slug <slug> --yes --reason incident_123
gregalectl instances force-restart --instance-id <uuid> --yes --reason incident_123
```

Each command emits an intent ID and trace ID for incident correlation. During
an apid outage, `--break-glass-local --yes --reason <incident_slug>` preserves
the former direct schedd/database path and prints a loud unaudited-action
warning.

Look up the complete operator-side trail without SSH or SQL:

```
gregalectl audit trace --trace-id <32-char-lowercase-hex>
```

The command uses exact indexed reads to correlate the durable intent with its
live audit events. Add `--json` for incident tooling.

### Build recovery

Sweep builder jobs that remained running beyond the incident threshold:

```
gregalectl auth step-up
gregalectl builds sweep-stuck --older-than 15m --reason builder_vm_timeout --yes
```

The command uses the authenticated operator API, emits a trace ID, and never
opens a database connection. Use `gregalectl audit trace --trace-id <id>` to
inspect its audit event.

### Job-run incidents

Find capacity-consuming job runs, inspect their bounded task metadata, and
cancel a stuck run without PostgreSQL access or a customer credential:

```
gregalectl jobs active
gregalectl jobs active --account-id <uuid>
gregalectl jobs inspect --run-id <uuid> --task-limit 100

gregalectl auth step-up
gregalectl jobs cancel --run-id <uuid> --reason jobs_queue_incident --yes
```

The read paths omit job inputs, environment overrides, commands, images, and
task lease tokens. Reads are audited; cancellation requires a recent MFA
step-up, idempotency key, explicit reason and confirmation, and returns a trace
ID for `gregalectl audit trace`.

### Deployment incidents

Inspect failed or stuck deployment attempts through the bounded operator
projection. The default view includes pending, building, imaging,
snapshotting, and failed rows; add `--status all` when a full terminal history
is needed:

```
gregalectl deployments active
gregalectl deployments active --account-id <uuid> --app-id <uuid>
gregalectl deployments inspect --deployment-id <uuid>
```

When the incident is understood, retry from a specific pipeline stage or
cancel queued work. Both commands require the same stepped-up operator session,
explicit confirmation and reason, and return a trace ID:

```
gregalectl auth step-up
gregalectl deployments retry --deployment-id <uuid> \
    --from-stage source_download --reason deploy_incident_123 --yes
gregalectl deployments cancel --deployment-id <uuid> \
    --reason deploy_incident_123 --yes
gregalectl audit trace --trace-id <trace-id>
```

The projection excludes source paths, image/rootfs handles, commands,
environment values, and log-spool locations. These actions use the existing
deployment queue state machine; they do not require direct SQL and do not
change the deployment hot path.

### Incident inbox

Start triage from one bounded, read-only view that correlates deployment,
job-run, compute-node, and Prometheus alert signals. The response is cursor
paginated and can be filtered by signal family or severity:

```
gregalectl obs incidents
gregalectl obs incidents --severity error
gregalectl obs incidents --type deployment --since 2026-09-12T00:00:00Z
gregalectl obs incidents --json
```

Each row carries a stable incident/dedupe ID, safe resource identifiers, an
inspection path, and a runbook link. Use the existing `deployments`, `jobs`,
and `compute-nodes` commands for explicit retry, cancellation, or drain
actions after reviewing the inbox. Triage metadata is durable and audited,
but never changes the underlying workload state. After `gregalectl auth
step-up`, acknowledge or resolve a row with its dedupe key:

```
gregalectl auth step-up
gregalectl obs incidents ack --dedupe-key deployment:<id> \
    --reason triage_started --owner oncall --yes
gregalectl obs incidents resolve --dedupe-key deployment:<id> \
    --reason fixed --note "rollback completed" --yes
```

The API requires a strict stepped-up operator session, idempotency key, and
same-origin checks. A signal observed after a resolve is shown as open again
so recurring node or alert conditions cannot be hidden by stale annotations.

### Fleet overview and capacity

Read the provider-wide KPI and placement snapshots without querying the
database directly. Both commands are bounded, read-only views and use the
same admin/MFA gate as the incident inbox:

```
gregalectl obs overview
gregalectl obs capacity
gregalectl obs capacity --json | jq '.summary.admission_margin_mb'
```

`overview` combines account, application, node-health, activation-funnel, and
recent-failure counters. `capacity` reports aggregate headroom plus safe
per-node counters; it does not return customer workload rows or change the
deployment path.

### GitHub recovery

Inspect and retry failed GitHub webhook or Check Run work without SSH or
database credentials:

```
gregalectl github status --status dead
gregalectl auth step-up
gregalectl github retry-delivery --delivery-id <uuid> --reason incident_123 --yes
gregalectl github retry-check --deployment-id <uuid> --reason incident_123 --yes
```

The CLI calls apid, which delegates queue ownership to githubd. Retry commands
are MFA-gated, idempotent, and emit trace-linked operator audit events. Webhook
payloads never cross the operator API.

### Account support

Routine tenant investigation and lifecycle changes go through the authenticated
operator API; they do not require SSH or direct database access:

```
gregalectl accounts list --status suspended
gregalectl accounts show --account-id <uuid>
gregalectl accounts 360 --account-id <uuid> --month 2026-09
gregalectl accounts activity --account-id <uuid> --limit 100

gregalectl auth step-up
gregalectl accounts suspend --account-id <uuid> --reason abuse_incident_123 --yes
gregalectl accounts restore --account-id <uuid> --reason appeal_approved_123 --yes
gregalectl accounts revoke-sessions --account-id <uuid> --reason credential_reset_123 --yes
```

Email is redacted unless `--include-pii` is supplied; PII access is audited by
apid. Every mutation requires a recent MFA step-up, an explicit reason and
confirmation, and emits a trace ID for correlation with its audit row. There is
no routine direct-database fallback for account mutations; use the reviewed
[`database-repair`](../break-glass/database-repair.md) procedure only during an
apid outage.

### Compute-node enrollment and inventory

Routine fleet enrollment and reads use the authenticated operator API and do
not require database credentials:

```
gregalectl compute-nodes add --name <fqdn> ... --reason fleet_expansion
gregalectl compute-nodes list
gregalectl compute-nodes show --node <fqdn>
```

The add command's direct PostgreSQL path requires the loud
`--break-glass-db --yes --reason <incident_slug>` combination. Use it only
during an apid outage under the reviewed database repair procedure. These
commands are outside the customer deployment hot path.

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
