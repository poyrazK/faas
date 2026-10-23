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
without touching disk (use this for the native acceptance rehearsal described
in `docs/ops/native-m9-acceptance.md`).

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

### Operator authentication

apid enforces this for every account in `FAAS_ADMIN_EMAILS`
([ADR 20261023214253](../adr/20261023214253-operators-need-mfa-and-sessions-for-customer-data.md)):

- **MFA is mandatory.** Every operator sign-in starts `mfa_pending`. An
  operator without an authenticator can only enroll one. A session of an
  operator with no authenticator gets `403 mfa_required` on the reads
  below. `POST /v1/auth/login` does not mint keys for operators. Create
  automation keys from an MFA-completed session.
- **Customer data needs a session.** Reads that return tenant records,
  events, audit rows, traces or client IPs refuse API keys with
  `403 forbidden` ("operator session required"). These are tenants, tenant
  360 and activity, apps, node detail, node events, overview, anomalies,
  rate limits, audit-log search, events, traces, incidents with their
  evidence, context and recurrence, egress flows, managed Postgres usage and
  accounting, job runs, deployments, account flags and `/v1/audit-log/all`.
  Sign in with `gregalectl auth login` and use that session.
- **Fleet telemetry accepts a key.** Probes and scripts may keep an
  operator key (`FAAS_ADMIN_TOKEN`) for capacity, nodes, heartbeats, wake
  latency, builder heartbeats, `obs/health`, the notification, rule,
  recording and input health and coverage reads, collection history, and
  the platform-alert summary, response times and triage history.
- **One-release grace.** Setting `FAAS_OPERATOR_BEARER_READS=deprecated` on
  apid lets operator keys read customer data again, marked with
  `Deprecation: true` and a `Warning` header. Each such read writes an
  `auth.operator_bearer_read_deprecated` audit row. Use it only while you
  move automation to a session, then remove it. The next release drops it.

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

### Tenant and capacity summaries

Tenant detail and tenant 360 count live deployments directly, including live
releases older than 1,000 newer deployment history rows. Deleted apps and
soft-deleted deployments are excluded. Capacity resource profiles aggregate in
the database and retain the named-profile order, with arbitrary resource shapes
combined into `custom`. Reserved resources count each app once, even when it has
multiple live instances. Store read failures return 503 rather than zero totals.

### Telemetry health

Run `gregalectl obs health` to inspect each audit counter and the firing-alert
count independently. A `healthy` zero is observed zero activity. `no_samples`,
`stale`, and `unavailable` display an unavailable value with a reason rather
than presenting a fallback zero as evidence. `observed_at` is the underlying
scrape or rule-evaluation time; `generated_at` is only response generation.

The counter window is 300 seconds and the freshness limit is 120 seconds.
Alert counts require fresh `faas.rules.yml` evaluation metrics from the
Prometheus self-scrape, even when no alerts fire. Removed series or rule groups
can keep a signal stale until its bounded history expires. `healthy` describes
fresh source evidence; inspect the value to judge operational health.

JSON consumers should inspect `signals.<field>.state` before using each
Prometheus value. `prometheus_available` is true only when all three signals
are healthy. Legacy responses without `signals` use that overall flag.
SQL coverage remains readable during a Prometheus failure; a database failure
returns 503.

`alert_history.collection_state` distinguishes `disabled`, `never_collected`,
`current`, `failed`, `stale` and `unknown`. Its existing `state` and `reason`
continue to describe evidence availability. A first failed poll can have no
successful history; disabled collection does not imply historical evidence is
empty.

`alert_retention` reports independent cleanup, even when Prometheus is disabled:
attempt/success timestamps, nullable `last_pruned_episodes`, exact
`expired_episodes`, oldest eligible timestamp, and the observation's `as_of` and
`cutoff`. States are `never_run`, `current`, `failed`, `stale`, or `unknown`.
`current` means a successful batch within two minutes; inspect the backlog
separately. A backlog larger than the 500-episode batch takes multiple passes.
Each pass runs every 30 seconds with a ten-second budget. Failed health reads
return 503 instead of zero. Bundles retain this metadata and mark missing
legacy fields unknown.

The existing apid metrics registry now exposes
`apid_operator_alert_maintenance_attempts_total` and
`apid_operator_alert_maintenance_duration_seconds`, labelled by
`operation=collection|retention` (attempt counts also use
`outcome=success|failure`), plus producer-written last-attempt/last-success
timestamp gauges and `apid_operator_alert_collection_enabled`. Metrics reset on
process restart; the health API retains durable cleanup receipts. Use timestamp
freshness alongside scrape health. These metrics contain no resource IDs.

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
actions after reviewing the inbox.

Inspect an incident's current potential exposure and related evidence:

```
gregalectl obs context --incident compute_node/NODE_UUID
gregalectl obs context --incident deployment/DEPLOYMENT_UUID --limit 25 --history-limit 25 --json
gregalectl obs context --incident platform_alert/EPISODE_HASH --cursor OPAQUE_CURSOR --json
```

Counts cover the full current relationship set. Resource pages have a separate,
bounded timeline covering the incident scope and returned resource IDs, with
exact match metadata and trace links. `as_of` describes current relationships;
`incident_observed_at` describes the incident. These relationships establish
potential exposure, not historical impact or root cause. Unknown scope has
null counts and an explicit reason. The 24-hour timeline window stays fixed
while paging; cursors expire after fifteen minutes. Database failures return
503, and retained alert context works without contacting Prometheus.

### Portable incident bundles

Capture evidence, one related-resource page, and observability health for an
operator handoff or postmortem:

```bash
gregalectl obs bundle --incident deployment/DEPLOYMENT_UUID --output incident.json
gregalectl obs bundle --incident platform_alert/EPISODE_HASH --limit 100 --history-limit 100 --timeout 45s --output alert.json --json
```

The versioned JSON includes per-section source and collection timestamps,
coverage, redaction exclusions, and limitations. It keeps unavailable sources,
unknown scope/null counts, telemetry freshness, and alert-collection health
visible. Inspect `coverage` before drawing conclusions. Numeric fallback zeros
are unusable when their telemetry signal lacks healthy observation metadata.

This is three separate reads, with current relationships and bounded history;
it does not establish historical impact or causation. No resource pagination or
trace/runbook fetching occurs. Resource/history limits default to 25 and cap
at 100. Truncation is reported separately for evidence history, related resources,
and the related timeline. Alert series aliases are pinned to one episode.

The total collection timeout defaults to 30 seconds and caps at one minute.
Each response is capped at 1 MiB and the final artifact at 4 MiB. Failed reads,
invalid responses, and exceeded limits abort without creating the destination.
Explicitly degraded API snapshots remain exportable with their coverage gaps.

`--output` is required. Files are published owner-only and existing files or
symlinks are never overwritten. Publication requires hard-link support in the
destination directory. `--json` returns a file receipt; `--output -` instead
streams the complete JSON bundle to stdout. All existing admin/MFA/allowlist
gates apply. Credentials, raw payloads, and local configuration are excluded;
resource identifiers remain, so control access to the exported artifact.

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

`prune-previous` is the load-bearing CI gate for the native acceptance
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
ADR-058 deviation note in `main.go:15`, and the customer-side `gregale`
binary still exposes the same commands. Both write `app_trusted_signers`
through apid; imaged reads that table on every verify. The ADR-058
follow-up ("operator-vs-customer split") is filed separately.

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
## Incident evidence

Use `gregalectl obs rule-health --json` to identify currently loaded alert rules
whose evaluation is failed, stale, unavailable or invalid. It reports rule/group
identity, safe source basename, evaluation age and bounded repository runbook
links. Pending/firing counts are null for unhealthy rules; a freshly evaluated
inactive rule has known zero counts. The API accepts no query parameters.

Totals cover the complete bounded loaded-rule snapshot. The preview contains
at most 200 items, unhealthy first; `truncated` and `total_items` expose omitted
items. Empty or unavailable rule inventories cannot be healthy. Raw queries,
labels, absolute source paths and evaluation errors remain private. This current
read does not establish an expected deployed inventory or the cause of a past
collection failure, and it does not alter collector recovery rules.

Incident bundles include a fourth `rule_health` section when collection health
advertises the known private route. Older servers retain explicit unknown
rule-health coverage. All reads share the existing bundle deadline and size caps;
rule-health state and preview truncation appear in bundle coverage.

Inspect the platform notification pipeline with
`gregalectl obs notification-health --json`. It reports ten-minute integration
attempts/failures, notification drops, current queue pressure, target and config
reload health, and source freshness. Missing/stale values are null, not zero.
Integration counters include all receivers and watchdog traffic; they cannot
confirm delivery of a particular alert, human receipt or external watchdog
receipt. Healthy signal state describes evidence quality; overall degraded
reports an observed failure or pressure condition, and partial means evidence
is incomplete. The API accepts no query parameters.

Platform-alert evidence also includes current Alertmanager suppression when an
exact current episode and label identity can be verified. Changed labels,
unavailable sources, stale observations and ambiguous matches stay explicit;
resolved episodes carry historical/not-applicable suppression. Operator triage
snoozes remain separate from Alertmanager silences. This read creates no silences
and changes no routes. The read-only local source uses `FAAS_ALERTMANAGER_URL`
(control-plane default `http://127.0.0.1:9094`; unconfigured on other roles).
Only literal loopback HTTP(S) is accepted; `disabled` explicitly disables it.

Copy an ID from `gregalectl obs incidents --json`, then inspect its current
state and exact-resource history without direct database access:

```bash
gregalectl obs evidence --incident deployment/11111111-1111-4111-8111-111111111111 --limit 25 --json
gregalectl obs evidence --incident compute_node/11111111-1111-4111-8111-111111111111
```

The API is `GET /v1/admin/obs/incidents/{kind}/{id}/evidence?limit=25`.
Supported kinds are `deployment`, `job_run`, `compute_node`, and `platform_alert`;
alerts use the full 64-character hash from their inbox ID. All existing admin,
MFA, and email-allowlist gates apply. Resource evidence remains readable after
recovery. A missing/deleted resource or an unretained alert ID returns 404;
an unavailable required source returns 503.

`history` contains the last 24 hours of exact-resource event, deployment-audit,
and operator-intent metadata, newest first. Operator events can match either
their resource subject or their explicit resource reference in the payload.
It excludes account-wide activity
and raw payloads. `limit` defaults to 25 and is capped at 100. `truncated=true`
means more matching history exists in that window; increase the limit or use
the existing event/audit inspection routes. Trace links use the existing
`/v1/admin/obs/traces/{trace_id}` lookup.

Platform-alert episodes retain firing and resolution transitions for 30 days,
with immutable episode IDs, rule-specific runbooks, and validated resource
links. A legacy series ID resolves to its latest retained episode. These
transitions use the episode's activation window rather than the resource
history's 24-hour window. Retained evidence remains readable during a
Prometheus outage; unresolved episodes become `unknown` when collection is
unhealthy, while resolved episodes retain their historical state.
`platform_alert_history` in `unavailable_sources` identifies collection gaps.
Before the first successful history collection, the compatibility view uses
live alerts; `prometheus_alert_metadata` means annotations were unavailable.

### Investigate recurring alert series

Run `gregalectl obs recurrence --json` or add `--since RFC3339` to select an
inclusive activation cohort. The server defaults to 24 hours and accepts at
most 30 days. It groups immutable rule definitions and exact series separately,
then ranks series by repeat activation count and latest activation. Different
resources and changed rules remain separate. Each group includes canonical
evidence paths for recent episodes.

Totals cover the complete retained cohort. The series preview stops at 200
and each recent-episode preview at five; both expose truncation. More than
1000 cohort episodes returns an error: narrow `--since` rather than treating a
partial sample as complete. Totals are unknown before the first successful
collection; known quiet retained cohorts have zero counts.

Completed episode durations and recovery-to-next-activation gaps show sample
counts, minimum and nearest-rank median in whole seconds. Missing recovery,
overlapping resolution observations and invalid clocks are separate counts;
absent samples remain null. A confirmed zero-second gap is a valid sample.
Collection and retention health accompany the result, and unknown unresolved
observations remain explicit.

These are retained observations, not lifetime counts, false-positive scores or
notification volume. An episode outside the activation window or removed by
retention does not contribute a predecessor. Confirmed resolution is the time
recovery was observed, not exact repair time. Prospective collection-cadence coverage accompanies recurrence reports.
Periods before outcome recording, expired evidence and missing observations
remain unknown; recent poll outcomes do not prove uninterrupted availability. This command reads evidence without changing alert or silence policy.


### Inspect collection history and monitoring blind spots

Run `gregalectl obs collection-history --json` or add `--since RFC3339` for a
window up to 30 days (default 24 hours). It shows prospective success/failure
outcomes and unknown intervals. No older history is inferred from the latest
collection timestamps. The history survives restart and is retained for 30 days;
cleanup removes at most 5000 expired outcomes per existing retention pass.

Add `--details` to investigate individual failed attempts, for example
`gregalectl obs collection-history --since 2026-10-16T00:00:00Z --details --json`.
The API equivalent is `GET /v1/admin/obs/collection-history?details=true`.
`failure_details` contains at most 20 newest failed attempts inside the requested
window, with a complete `total_failures` and explicit truncation. These are
individual attempts; the cadence timeline can coalesce several attempts into
one interval. A carry-in receipt does not enter this failed-attempt preview.

For newly rejected readable snapshots, `diagnostics_state=recorded` includes
complete loaded-rule/group counts and up to 10 unhealthy rule/group items,
bounded to 16 KiB of compact JSON per attempt. Each item carries hashed identity,
safe names, closed issue codes, evaluation time/age at the attempt, error
presence and a repository-safe runbook when available. The preview is ordered
by issue priority then hashed group/item identity. It excludes raw queries,
evaluation errors, labels and absolute paths. Several issues can be observed;
they do not prove an underlying root cause or which issue first rejected the
snapshot. `collector_validation_failed` means the rejection has no matching
rule-level diagnostic; it does not manufacture a cause.

Older receipts, unreadable sources, timeouts, persistence failures and
unrepresentable diagnostic metadata use `diagnostics_state=unknown` and null
diagnostics. A recorded empty source uses `no_alert_rules_observed` with known
zero rule counts. Nothing is backfilled from today's rule-health response.
Recovery preserves earlier evidence, which expires with its 30-day receipt.
The opt-in details read shares the existing five-second snapshot budget and
operator authorization; the default view and analytics omit diagnostic bodies.

A recorded outcome supplies at most 60 seconds of cadence evidence, ending
sooner when the next outcome arrives. Missing polls, process downtime and
unrecordable storage failures become unknown after that evidence expires.
Known failure intervals use only closed codes: `collection_failed`,
`source_read_failed`, `snapshot_rejected`, `timeout` or `persistence_failed`.
Unknown intervals use `no_recent_outcome`; they do not identify a cause.

The three duration totals partition the requested window exactly in
microseconds. Success/failure counts include attempts on both window boundaries;
a retained earlier outcome may contribute carry-in duration without increasing
those counts. The full totals precede at most 200 coalesced intervals, newest
first; `total_intervals` and `truncated` make omitted older intervals explicit.
More than 100000 read outcomes returns a bounded error requiring a narrower
window. The carry-in read is also bounded by the 30-day retention horizon.

Recurrence and response-time reports include coverage from their same database
snapshot. Historical episode statistics remain retained observations. An
`observed` cadence window means recent successful poll evidence covered the
window under this freshness model, not continuous service availability,
expected-rule inventory completeness or full metric coverage. Older servers
may omit this added metadata; the CLI displays coverage as unknown in that case.
Disabled collection now does not erase its retained historical outcomes.

### Check expected alert definitions

Run `gregalectl obs rule-coverage --json` to see the available expectation sets.
Without selection, coverage stays unknown and Prometheus is not queried. Select
the set appropriate to the installation explicitly:

```sh
gregalectl obs rule-coverage --expected-set gregale-core-v1 --json
gregalectl obs rule-coverage --expected-set gregale-full-v1 --json
```

The API equivalent is
`GET /v1/admin/obs/rule-coverage?expected_set=gregale-core-v1`, with the usual
operator read scope, MFA and allowlist. Core expects `faas.rules.yml` alerts;
full also expects `bridge.rules.yml`, `pg_backup.rules.yml`,
`data_placement.yaml` and `data_repair.rules.yml`. These are minimum sets: additional valid rules are counted
and allowed. The catalog in the binary is not evidence of what was intended to
be installed. Each set includes its exact definition revision and file basenames.

`covered` means the selected alert definitions match; `degraded` identifies
missing, changed or ambiguous definitions. `partial` and `unknown` preserve
unreadable identities or definitions, absent fields and source failures. A
complete empty source has known missing rules. File basename, group and alert
name identify a rule; directory moves are ignored and basename/group collisions
are ambiguous. Changed fields distinguish expression, `for`, `keep_firing_for`
and rule labels. Formatting and label order are normalized, without claiming
semantic equivalence. Queries, labels, raw errors and absolute paths are omitted.

Totals cover the complete bounded source and expected set before a 200-item
preview, with ambiguity/change/absence ahead of matches. The three-second read
returns null totals if evidence is unavailable. Coverage is a current read:
it does not write history or prove metric inputs, recording dependencies,
evaluation health, notification delivery or deployment intent. Group intervals,
offsets and annotations are outside this comparison. Use `obs rule-health` and
`obs notification-health` for their respective evidence. Repository maintainers
update the catalog with `make obs-rule-catalog-generate` after source-rule edits;
`make obs-rule-catalog-check` and Go tests detect drift.

### Check recording definitions and their expected alert references

Run `gregalectl obs recording-coverage --expected-set gregale-full-v1 --json`
or select `gregale-core-v1` for the core file profile. Without `--expected-set`,
the command lists available metadata, stays unknown and does not read Prometheus.
The API is `GET /v1/admin/obs/recording-coverage?expected_set=gregale-full-v1`,
with existing operator authorization. This minimum file selection is explicit;
it does not establish installation intent or reject additional valid rules.

The recording catalog has its own exact revision. Definitions compare canonical
expressions and rule labels, reporting matching, missing, changed, ambiguous or
unknown. Prometheus may omit empty recording labels; explicit null stays unknown.
Totals cover the full bounded source and expected set before a 200-item preview.
An unavailable source has null totals; a complete empty source has known missing
recording definitions. Evaluation health, metric inputs, group intervals/offsets
and annotations remain outside this comparison.

Each item includes `dependent_alerts`: direct and transitive expected catalog
references, with the full known-reference count, at most five items and explicit
truncation. Uncertain mappings may leave additional unresolved dependencies. The
`dependencies` object binds the expected alert catalog revision and reports
complete/partial mapping, including full uncertain-alert counts and a five-alert
preview. Nonexact selectors, duplicate producer names and reachable cycles leave
uncertainty. Known references can coexist with unknown links. This expected
syntax mapping stays available during a live-source failure; it does not prove
deployed alerts, matching label sets, runtime impact or an incident's cause.
Covered definitions can coexist with partial mapping or unhealthy evaluations.

The read shares the three-second inventory budget and excludes raw queries,
labels, errors and absolute paths. It never writes incident or collection state.
Existing catalog generation/check targets now cover both generated catalogs;
Go tests also detect recording definition or dependency drift.

### Critical telemetry input health

```sh
gregalectl obs input-health --json
gregalectl obs input-health --expected-role single-box-v1
gregalectl obs input-health --expected-role control-plane-v1 --json
```

The first command advertises expectations and returns unknown without reading
Prometheus. Select the role explicitly; binary presence does not establish the
installed topology. Both roles expect at least one apid, public gateway
(`gatewayd` scrape job) and control-plane schedd target. Single-box adds vmmd.
Control-plane reads gatewayd-internal, schedd-compute and vmmd expectations from
fresh apid compute discovery snapshots, including omitted invalid target counts.
The reader uses the default `job="apid", exported_job="<compute-job>"` collision
mapping. A different label mapping or multiple discovery producers stays unknown.
A fresh successful zero-node registry can legitimately have no compute targets.

Read scrape coverage and metric evidence separately. Fresh `up=0`, stale scrape
samples and missing minimum targets are different counts. Unexpected dynamic
counts remain partial. Each job checks its own always-instantiated readiness
gauge on every fresh up target: a fresh sibling cannot conceal a missing or
stale sibling. A readiness value of zero is valid telemetry; this command does
not assess whether the daemon is ready. Traffic counters are not required at
idle. Missing samples mean none found within the ten-minute lookback; original
sample timestamps, with a 120-second freshness threshold, determine staleness.
Down targets remain in scrape evidence rather than becoming missing gauge faults.

Static expectations are minimums and cannot detect every missing replica.
Discovery counts cannot verify target identity membership. This first input set
covers apid, gateways, schedulers and VM managers, one readiness gauge per job;
other rule inputs remain unassessed. Use `rule-coverage`, `recording-coverage`,
`rule-health` and `notification-health` for their independent evidence. The API
is GET `/v1/admin/obs/input-health?expected_role=control-plane-v1` with existing
operator/MFA/allowlist controls, a shared three-second read budget and aggregate
counts only. It writes no history or configuration.
