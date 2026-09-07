# Multi-host rollout — adding a second compute node

Issue #297 acceptance item 4. Operator procedure for adding a second
compute node to the fleet (the cut-over from single-host to
multi-host). This is the v1 reference for the multi-host topology —
the moment we go from one node (a single `default-local` row in
`compute_nodes`) to two nodes where each carries its own admission
ceiling, its own capacity reports, and its own share of the cluster
vCPU budget.

The shape mirrors the historical TLS cut-over runbook under
`docs/ops/` (PR-A-excluded) — cut-over sequence + rollback criterion +
verification + escalation. The sibling active-passive HA topology
runbook is
[`docs/runbooks/active-passive-ha.md`](active-passive-ha.md)
(this doc covers the multi-host horizontal-scale variant, not
active-passive). Tier A8 / ADR-083 ships the active-passive
lex-min leader election, warm-standby pre-warming, and DNS
failover that closes the §14 M8 row "Gate-A runbook (2nd box
active-passive)".

> [!CAUTION]
> **Pre-conditions are mandatory.** Do not start the cutover
> unless every Pre-conditions check above produces its expected
> output. Stop on first failure; fix the pre-condition and re-run.
> The acceptance footer at line ~600 of this file pins this
> admonition as `> [!CAUTION]`, not `> [!NOTE]`, because the
> Pre-conditions block is the load-bearing input to the cutover
> (Gate-B acceptance item 4, issue #297).
> at
> [docs/adr/025-decoupled-control-plane-and-compute.md §Tier 2
> pre-requisites](../adr/025-decoupled-control-plane-and-compute.md#tier-2-pre-requisites).
>
> - **Tier 1 Phase 1 (mTLS, issue #95 slice 2)** — ✓ shipped
>   in PR #445. Stdlib verifier does chain + SAN + EKU;
>   handler-layer peer binding is in place per ADR-052. Wire
>   package: `pkg/wire.DialContext` / `wire.Listen`. Cert
>   material under `/etc/faas/tls/{ca,schedd,vmmd,...}/`
>   at 0400 root:root, generated via `gregalectl pki init`.
> - **Tier 1 Phase 2 (`node_signature` on `CapacityReport`)** —
>   ✓ shipped in PR #457 (commit `bfd1d2ca`, ADR-053 bundle).
>   vmmd signs the `CapacityReport` it emits via `pkg/wire.NodeSigner`
>   schedd verifies the leaf-CN → `compute_nodes.name` lookup before
>   the report enters the chooser's bias calc. The ledger's per-node
>   ceiling (`NodeLedger.Admit`) is unchanged; the trust path is
>   additive. ADR-053 status was flipped in the same PR.
> - **Tier 1 Phase 3 (`OCIRegistryStorageBackend` end-to-end,
>   issue #95 slice 4)** — ✓ shipped in the ADR-054 acceptance PR.
>   The driver (`pkg/storage/oci.go`), router (`pkg/storage/router.go`),
>   cache (`pkg/storage/cache.go`), and `BackendFromEnv` wiring live
>   in every daemon (`cmd/{imaged,vmmd,schedd}/main.go`). The cache
>   defaults to on at `/var/lib/faas/cache` for oci mode (multi-box
>   default-on); explicit `FAAS_STORAGE_CACHE_DIR=""` disables. The
>   stale-fallback branch is opt-in via `FAAS_STORAGE_CACHE_SERVE_STALE` —
>   the pre-acceptance fail-loud contract is preserved when the env
>   var is unset. The `StorageCacheStaleFallback` counter
>   (`pkg/wire/metrics.go`) surfaces the registry-outage rate on the
>   §12 dashboard. Each compute node no longer holds a local copy of
>   every app's per-app layer; the §4.6 two-drive storage economics
>   hold at fleet scale. ADR-054 itself was flipped from `proposed`
>   to `accepted (revised 2026-08-07)` with the Amendment section
>   documenting the three policy decisions.
> - **Tier 1 Phase 4 (per-host egress policy templating)** —
>   ✓ shipped in ADR-055. The static
>   `policy_nftables.conf` is now a Jinja2 template
>   (`policy_nftables.conf.j2`) that substitutes
>   `{{ public_iface }}` and `{{ masquerade_cidr }}` at the
>   two substitution sites. A reference compute node on
>   `ens5` sets `host_vars[fsn-2].public_iface: ens5` and
>   the rendered `/etc/nftables.conf` carries that value
>   through the forward-chain allow and postrouting
>   MASQUERADE. The Go render at
>   `pkg/netns.HostPolicy.Render()` is the source of truth;
>   `make egress-render-cross-check` byte-compares the Go
>   and Jinja2 surfaces for every supported pair. The
>   runtime `pg_notify` watcher
>   (`cmd/vmmd/egress_watcher.go`, migration 00078) keeps
>   `/etc/nftables.conf` live-reloadable without a
>   `make bootstrap` rerun.
> - **Tier 1 Phase 5 (`pkg/wire.NodeVerifier`)** —
>   ✓ shipped in ADR-056. Every cross-box mTLS leg
>   (vmmd↔schedd, schedd→vmmd, gatewayd-internal→vmmd) now installs
>   a `tls.Config.VerifyPeerCertificate` hook that augments
>   stdlib chain/SAN/EKU trust with a leaf-CN →
>   `compute_nodes.name` lookup. The verifier runs
>   `after` stdlib trust succeeds, so stdlib chain failures
>   still reject first (CodeQL #58 invariant: never touch
>   `InsecureSkipVerify`). A peer presenting a leaf-CN
>   that is not in the `compute_nodes` registry is
>   rejected at handshake — BEFORE any RPC dispatch.
>   Single-box dev mode (no `compute_nodes` row) skips the
>   verifier entirely; multi-box vmmd wires it gated on
>   `cfg.ComputeNode.NodeName != ""` (same gate as the
>   egress watcher). The wire factory variants
>   `wire.LoadServerTLSConfigWithVerifier` /
>   `wire.LoadClientTLSConfigWithVerifier` /
>   `*WithPrefixAndVerifier` are additive — the originals
>   stay byte-for-byte unchanged so single-box CI keeps
>   working. Defense-in-depth alongside `wire.PeerCN`
>   (ADR-052).
> - **#250 (off-host Postgres backup)** — ✓ shipped in
>   PR #784 (ADR-056 v1.0, accepted 2026-08-09). Compound
>   `archive_command` (cp + rclone to Hetzner Storage Box),
>   `faas-pg-basebackup-push.{service,timer}` pair, sealed-at-rest
>   `/etc/faas/secrets/storage-box/` credentials, the
>   `pg_backup_last_pushed_seconds` gauge + `PgBackupStale`
>   alert, and `pg-restore-verify.sh` T-7 throwaway verify are
>   all in tree. The Tier 2 pre-requisites bullet for #250 was
>   retired from ADR-025 the same day.
> - **#316 (`host.age` rotation runbook)** — ✓ shipped
>   (PR for issue #316, ADR-057). 30-day rotation-overlap
>   window via `gregalectl host-age {init,rotate,status,prune-previous}`,
>   all five unseal sites migrated to `secretbox.OpenMulti`.
>   See `docs/ops/host-age-rotation.md` for the operator
>   runbook. v2 re-seal follow-up filed as
>   `issue-316-followup-rekey`.

## Topology

Two physical nodes (per spec §14
"Regional expansion" — a FSN + HEL pair is one possible topology). Wire identity:

| Role                | Hostname       | `compute_nodes.name` | `target_url` (vmmd)                   | `schedd_target_url` (schedd)               |
|---------------------|----------------|----------------------|---------------------------------------|-------------------------------------------|
| Control plane + 1st compute | `gregale-fsn-1` | `default-local`      | `unix:///run/faas/vmmd.sock` (legacy) | `unix:///run/faas/schedd.sock` (backfill) |
| 2nd compute (new)   | `gregale-fsn-2`   | `fsn-2`              | `tcp://vmmd-2.faas:50051` (mTLS)      | `tcp://schedd-2.faas:7100` (mTLS)         |

Both nodes share the same Postgres. The new node runs the full
daemon fleet (apid, schedd, vmmd, imaged, meterd, gatewayd-public,
gatewayd-internal, builderd) — no "control plane on one box, compute on another"
split until Gate-B. The control plane is on `fsn-1`; the new
compute node advertises itself via vmmd's
`Schedd.ReportCapacity` client-stream (ADR-025 §4.1).

The Postgres `compute_nodes` row for `fsn-2` is created by the
operator via `POST /v1/compute-nodes` (ADR-029); vmmd's startup
self-registration UPSERTs the same row. The synthetic
`default-local` row stays untouched (hard-delete refused with HTTP
409 `default_local_protected`).

## Pre-flight

For snapshot locality, prepare one secret-backed `/etc/faas/storage.env`
payload and pass it to the provider-neutral join command as
`--storage-env /secure/storage.env`. The join pipeline installs the same
payload on the control plane and compute node. It must contain
`FAAS_STORAGE_BACKEND=oci` and `FAAS_OCI_REGISTRY`; leave `snap/` out of
`FAAS_STORAGE_LOCAL_PREFIXES` so snapshots remain in shared OCI storage and
the node-local cache can be warmed by vmmd. Leave
`FAAS_STORAGE_CACHE_DIR` unset for the default `/var/lib/faas/cache`, or set
it to a non-empty path; an explicitly empty value disables the required
prepositioning cache. Do not put registry credentials in the manifest,
inventory, or git.

```sh
# 1. Confirm the new node has the daemon fleet provisioned.
ssh faas-fsn-2 'systemctl is-system-running && \
  for d in apid schedd vmmd imaged meterd gatewayd-public gatewayd-internal builderd githubd; do \
    systemctl is-active faas-$d || exit 1; \
  done'

# 2. Confirm the cert material was generated by PR #445's pki cmd.
ssh faas-fsn-2 'ls -la /etc/faas/tls/{ca,vmmd,schedd}/'
# expect: ca.crt (0444), vmmd/{vmmd.crt (0444), vmmd.key (0400)}, ...

# 3. Confirm the leaf cert CN matches the daemon role.
ssh faas-fsn-2 'openssl x509 -in /etc/faas/tls/vmmd/vmmd.crt -noout -subject'
# expect: subject=CN = vmmd.faas

# 4. Confirm the tier-1-phase-1 mTLS handshake works against the CP.
ssh faas-fsn-2 'openssl s_client -connect faas-fsn-1:7070 \
  -cert /etc/faas/tls/vmmd/vmmd.crt \
  -key /etc/faas/tls/vmmd/vmmd.key \
  -CAfile /etc/faas/tls/ca/ca.crt <<<"Q"'
# expect: Verify return code: 0 (ok)
```

If any check fails, **stop.** Fix the pre-condition and re-run.
The cert material is the load-bearing trust story for the box-to-
box leg; a broken handshake means a forged-report-capable node.

## Procedure

### 1. Ansible inventory delta — add the new compute node

Edit `deploy/ansible/inventory/hosts.ini` to include `faas-fsn-2`
in the `[compute_nodes]` group:

```ini
[compute_nodes]
faas-fsn-1 ansible_host=...
faas-fsn-2 ansible_host=...
```

Add `host_vars/faas-fsn-2.yml` with the per-node overrides:

```yaml
---
# Per-host egress policy (ADR-055, Tier 1 Phase 4). public_iface is
# substituted into the Jinja2 template
# deploy/ansible/roles/nftables/files/policy_nftables.conf.j2 at the
# forward-chain allow and the postrouting MASQUERADE. A compute node on a
# different NIC name (e.g. ens5) overrides here;
# the rendered /etc/nftables.conf carries the new value through
# both substitution sites (pkg/netns.HostPolicy.Render() is the
# Go source of truth; `make egress-render-cross-check` byte-compares
# the two surfaces). The default-local node on faas-fsn-1 keeps
# eth0.
public_iface: ens5

# Per-host masquerade CIDR. Every compute node's bridged tenant VMs
# fall in this RFC1918 slice (10.100.x.y, x.y ≥ 0.2; the bridge IP
# .1 is reserved by pkg/fcvm/alloc.go). Distinct from fsn-1's CIDR
# so the overlay routes don't collide across the cluster.
#
# Pre-Phase-3: every compute node must hold a local copy of every
# app's per-app layer — the OCI snapshot backend that fixes the
# per-host layer duplication lands in Tier 1 Phase 3.
masquerade_cidr: 10.101.0.0/16
```

> **Note:** the inventory delta is now per-host for both fields.
> The runtime watcher (`cmd/vmmd/egress_watcher.go`, channel
> `egress_policy_changed`) keeps `/etc/nftables.conf` in sync with
> the audit row, so an operator-side UPSERT on `egress_policy`
> also hot-reloads the live ruleset without re-running
> `make bootstrap-compute`.

### 2. `make bootstrap-compute` on the new node

```sh
ssh gregale-fsn-2 'cd /opt/onebox-faas && git pull && sudo make bootstrap-compute'
```

> **Note:** the `/opt/onebox-faas` filesystem path is the bootstrap
> layout from `deploy/scripts/bootstrap.sh` (file does NOT exist;
> canonical path is `deploy/controlplane/bootstrap.sh`, RETIRED
> 2026-08-15 by issue #911 / PR-1; v2 path is `make bootstrap-compute` +
> `gregalectl manifest {validate,render}` + `gregalectl release install`).
> Filesystem layout is code-side, not part of the docs rebrand.
> A follow-up code-identity pass renames the path to match the host
> `gregale` user; until then the path is stable across all bootstrap
> invocations.

The bootstrap role provisions the daemon fleet + applies the
`overlay` role (Tailscale + Wireguard stub) + renders
`/etc/nftables.conf` from the host_vars above. Per-daemon stat
asserts (added in PR #445's `control_plane_service` and
`vmmd_service` roles) verify each leaf cert exists.

Expected output (within ~60 s):

```
TASK [control_plane_service : assert per-daemon leaf cert exists] ****
ok: [faas-fsn-2] => {
    "changed": false,
    "msg": "All assertions passed"
}
```

If any stat-assert fails, **stop** and re-run
`gregalectl pki init --force` on the new node.

### 3. `compute_nodes` row — operator-side POST

On the control plane (`faas-fsn-1`), pre-register the new box:

```sh
curl -fsS -X POST 'https://faas-fsn-1:8081/v1/compute-nodes' \
  -H 'Authorization: Bearer <admin-bearer>' \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "fsn-2",
    "target_url": "tcp://vmmd-2.faas:50051",
    "gateway_target_url": "tcp://fsn-2.gregale.dev:8080",
    "vpcpus": 160,
    "mem_mb": 56000,
    "max_concurrency": 200,
    "admission_ceiling_mb": 47600
  }'
```

> **This `target_url` is the source of truth for the dial
> target.** It MUST be a routable FQDN or IP that another host
> can dial — NOT `0.0.0.0` (which resolves to the local host's
> own vmmd, not the second box). See §3.5 for the
> `listen_addr`/`target_url` distinction. The field is operator-
> owned: vmmd's startup UPSERT preserves it on restart (it does
> NOT clobber the operator's value with whatever `listen_addr`
> contains — that was the pre-fix trap; see
> `pkg/state.UpsertComputeNodeFromVmmd`).

The row is `active=true` by default. The `compute_node_changed`
pg_notify trigger (migration 00026) fires and evicts gatewayd-internal's
per-node client cache for any prior `fsn-2` entry. The admin POST
is idempotent — re-POSTing with the same name UPSERTs the row
via `UpsertComputeNodeFromOperator` (ADR-029). The operator's
target_url wins on every field.

> **Note:** the synthetic `default-local` row is hard-delete
> refused with HTTP 409 `default_local_protected` (ADR-029). Do
> not try to `DELETE /v1/compute-nodes/default-local?hard=1`.

### 4. vmmd self-registration on the new node

```sh
ssh faas-fsn-2 'sudo systemctl restart faas-vmmd'
journalctl -u faas-vmmd -f
```

Expected output (within ~5 s):

```
vmmd: compute_node registered name=fsn-2 id=... target_url=tcp://vmmd-2.faas:50051 vpcpus=160 mem_mb=56000 admission_ceiling_mb=47600
vmmd capacity_publisher starting 1s tick
```

> The `target_url=tcp://vmmd-2.faas:50051` line MUST show the
> operator's POSTed FQDN — NOT the bind address
> (`tcp://0.0.0.0:50051`) and NOT the unix socket. If you see
> either of those, the operator's target_url was clobbered —
> re-POST and restart. vmmd's startup UPSERT preserves the
> operator's target_url via `UpsertComputeNodeFromVmmd`
> (`coalesce(compute_nodes.target_url, excluded.target_url)`);
> the pre-fix trap was that vmmd's UPSERT silently overwrote the
> operator's value with the bind form. See §3.5 for the
> distinction.

vmmd UPSERTs the `compute_nodes` row on startup via
`UpsertComputeNodeFromVmmd`. The two UPSERTs (admin POST via
`UpsertComputeNodeFromOperator` + vmmd self-registration) are
idempotent — the row ends with the operator's `target_url` (vmmd
does not touch it) and vmmd's view of `vpcpus` / `mem_mb`. If
they don't match the operator POST, the admin POST was wrong;
re-POST with the right values.

### 3.5. `listen_addr` vs `target_url` — the load-bearing distinction

Two fields, two concerns:

- **`listen_addr`** (top-level `vmmd.toml`) — the address vmmd
  BINDS to. `tcp://0.0.0.0:50051` is fine for listening on all
  interfaces; `tcp://[::]:50051` for IPv6. The bind address is
  what `net.ListenConfig.Listen(ctx, "tcp", t.Address)` resolves
  to.
- **`[compute_node].target_url`** — the address schedd/gatewayd
  DIAL to reach this vmmd. Must be a routable FQDN or IP that
  another host can resolve, e.g. `tcp://vmmd-2.faas:50051` or
  `tcp://100.64.0.1:50051`. NEVER `0.0.0.0` — that resolves to
  the dialer's own loopback / local vmmd, not the second box.

The two fields are now separate in `vmmd.toml` (post-PR-fix;
before this PR they were the same string, and `0.0.0.0` would
silently land on the local host's own vmmd). Pre-flight on
every `systemctl restart faas-vmmd`:

- vmmd logs a Warn if the resolved `target_url` is a `tcp://`
  form AND it equals `listen_addr` (the most common re-
  introduction of the conflation: operator sets
  `listen_addr = tcp://vmmd-2.faas:50051` and never sets
  `target_url` separately; works on dev but on multi-box the
  bind form is the dial form, which routes to wrong host).
- The fix is one line: add `[compute_node].target_url =
  "tcp://vmmd-2.faas:50051"` (or set `[compute_node].overlay_ip`
  for the auto-detected Tailscale IP fallback).

### 4.5. Operator POST preservation across vmmd restarts

`vmmd`'s startup UPSERT (`UpsertComputeNodeFromVmmd`) preserves
the operator's `target_url` on conflict — the existing
operator-POSTed value stays. The COALESCE in pgstore
(`coalesce(compute_nodes.target_url, excluded.target_url)`)
handles the cold-INSERT case where no operator POST has happened
yet: the seed row from migration 00024 carries a non-empty
target_url already, but a fresh box's first registration gets
vmmd's own view of its dialable address.

Concretely: an operator POST is the **source of truth** for
`target_url`. vmmd will not silently overwrite it on restart.
If you need to change the dial target (host swap, IP rotation),
re-POST with the new value — do NOT just edit `listen_addr`.

### 5. Capacity report visibility

On the control plane:

```sh
psql -c "select node_id, sampled_at_unix_ms, live_count, leased_count, used_mb, ram_headroom_mb, vcpu_busy \
         from node_capacity_table where node_id in \
           (select id from compute_nodes where name='fsn-2') \
         order by sampled_at_unix_ms desc limit 5"
```

Expected: at least 3 rows within the last 5 seconds. The
`CapacityReport` proto shape is `live_count` → `vcpu_busy`
(`pkg/scheddgrpc/server.go:459-509` handler; spec §6.4
"CapacityReport trust" row is gated on Tier 1 Phase 2
`node_signature` — see pre-conditions).

If the table is empty after 30 s, check:

- `journalctl -u faas-vmmd -n 100` on `fsn-2` for
  `capacity_publisher` errors.
- The mTLS handshake in step 4 of Pre-flight. A failed handshake
  silently drops capacity reports.
- The pg_notify channel — `pg_listening_channels()` should
  include `compute_node_changed`.

### 6. Placement test

```sh
# Deploy a small fixture app, watch it land on fsn-2 for at least
# one of the concurrent wakes.
faas app deploy --image ghcr.io/poyrazk/faas-test:hello
psql -c "select node_id, state from instances where app_id='hello' \
         order by created_at desc limit 5"
```

Expected: at least one row with `node_id` matching `fsn-2`'s
UUID. The chooser is `pkg/sched.choosePlacementLocked` at
`engine.go:519-578`; the bias is RAM headroom (less-loaded node
wins) plus the sticky-warm affinity hint (PR #429).

If no row lands on `fsn-2`, the placement is working but the
bias is wrong — check `node_capacity_table` (step 5) and
`pkg/sched/instancestats/poller.go` for staleness.

### 7. Validation matrix

| # | Check                                              | Command                                                                                  | Expected                       |
|---|----------------------------------------------------|------------------------------------------------------------------------------------------|--------------------------------|
| 1 | `fsn-2` row in `compute_nodes`                     | `psql -c "select name, active from compute_nodes where name='fsn-2'"`                    | `fsn-2, t`                    |
| 2 | Capacity reports within 5 s                         | `psql -c "select count(*) from node_capacity_table where sampled_at_unix_ms > extract(epoch from now() - interval '5 seconds') * 1000"` | `count(*) > 0`                |
| 3 | mTLS handshake CP↔compute                          | `openssl s_client -connect faas-fsn-1:7070 -cert ... -CAfile ...`                         | `Verify return code: 0`       |
| 4 | Leaf cert CN                                       | `openssl x509 -in /etc/faas/tls/vmmd/vmmd.crt -noout -subject`                       | `subject=CN = vmmd.faas`      |
| 5 | At least one instance placed on `fsn-2`            | `psql -c "select count(*) from instances where node_id = (select id from compute_nodes where name='fsn-2')"` | `count(*) > 0`                |
| 6 | Cert mode 0400                                     | `stat -c '%a' /etc/faas/tls/vmmd/vmmd.key`                                           | `400`                          |
| 7 | Cert mode 0444 (crt)                               | `stat -c '%a' /etc/faas/tls/vmmd/vmmd.crt`                                           | `444`                          |
| 8 | vmmd self-registered                               | `journalctl -u faas-vmmd -n 50`                                                          | `vmmd registered node_id=...` |
| 9 | `node_capacity_table` freshness                    | PromQL: `time() - max(node_capacity_table_sampled_at_unix_ms) < 5`                       | `1`                            |
| 10| `gateway_wake_latency_seconds` p95 ≤ 1 s           | PromQL: `histogram_quantile(0.95, rate(gateway_wake_latency_seconds_bucket[5m]))`       | `< 1`                          |
| 11| `schedd_target_url` populated on new node          | `psql -c "select name, schedd_target_url from compute_nodes where name='fsn-2'"`         | non-null                       |
| 12| `schedd` resolved its `OwnerNodeID`                | `ssh faas-fsn-2 'journalctl -u faas-schedd --since -1m \| grep -i owner'`                | one log line                   |
| 13| Apps split across owners within 60 s of a create   | `psql -c "select node_id, count(*) from apps group by node_id"`                          | both `default-local` and `fsn-2` non-zero |
| 14| `compute_nodes.target_url` matches operator POST (not the bind address) | `psql -c "select name, target_url from compute_nodes where name='fsn-2'"` | routable FQDN/IP, NOT `0.0.0.0` and NOT `unix://` |
| 15| `journalctl -u faas-vmmd` shows `compute_node registered target_url=tcp://<fqdn>:50051` | `journalctl -u faas-vmmd -n 50 \| grep "compute_node registered"` | the FQDN, not `0.0.0.0` |
| 16| vmmd does NOT log the `listen_addr==target_url` Warn | `journalctl -u faas-vmmd -n 50 \| grep -i "target_url equals listen_addr"` | no output (no conflation re-introduced) |

### 8. Rollback

If the cut-over surfaces a regression (e.g. fsn-2 rejects every Nth
wake, capacity reports stop after 30 s, mTLS handshake fails):

1. Drain `fsn-2` from Operations → Nodes, provide the rollback reason,
   and retain the returned intent ID. The `compute_node_changed` trigger
   fires after schedd applies the intent; gatewayd-internal evicts the
   cached connection and placement skips the node.

```sh
# 2. Stop vmmd on the new node after the intent succeeds.
ssh faas-fsn-2 'sudo systemctl stop faas-vmmd'

# 3. Verify the cluster returns to single-box state.
psql -c "select name, active from compute_nodes"
# expect: default-local (t), fsn-2 (f)
```

The rollback is non-destructive — the `compute_nodes` row stays
in place with `active=false`, the cert material under
`/etc/faas/tls/vmmd/` is untouched. A re-rollout restarts vmmd and uses
Operations → Nodes → **Activate**; its durable intent returns `fsn-2` to
service.

### 9. Escalation

If the cut-over fails irrecoverably:

- **Node-id mismatch** between schedd view (`compute_nodes.id`)
  and vmmd's self-registration — `make bootstrap-compute` against the
  failed node with `--force` re-issues the leaves; the vmmd leaf
  CN must match the operator-POST row's `target_url` host.
- **mTLS handshake failure** — re-run `gregalectl pki status` on
  both boxes; confirm the leaf CN matches the operator-side
  expected-CN map. The handler-layer peer binding (ADR-052) is
  the load-bearing enforcement that survives a forged leaf.
- **Capacity reports never land** — check the
  `egress_policy_changed` watcher on fsn-2
  (`journalctl -u faas-vmmd` for the watcher logs; the
  watcher re-renders with the host's compile-time defaults
  on every notification). If `host_vars[fsn-2].public_iface`
  was set to a value not present on the new node's NICs,
  `nft -c -f` will fail the syntax check and the watcher
  leaves the staging file on disk for inspection.
- **Page the on-call.** A multi-host rollout that fails is page-
  severity on the staging cluster, near-page on production
  (because the rollback above restores single-box state in
  <30 s).

## Tier A5 gate — cross-node live-instance migration

ADR-066 (`docs/adr/066-tier-a5-cross-node-live-migration.md`,
`Accepted (revised 2026-08-07)`) is the cross-node live-instance
handoff story. The Tier A5 gate exercises the full four-phase
commit end-to-end on a two-node fleet — the operator-facing
acceptance for §14 M9.

### Tier A5 pre-flight

- A two-node fleet already bootstrapped per the procedure
  above (`compute-01`, `compute-02`); both nodes have
  `active=true` and `node_signature` populated (Tier 1 Phase 2,
  shipped).
- `OCIRegistryStorageBackend` end-to-end (Tier 1 Phase 3,
  shipped via ADR-054 acceptance PR #457 + #716). Every
  compute node reads per-app layers and snapshot blobs from
  the registry; `LocalCacheBackend` defaults to
  `/var/lib/faas/cache` on multi-box fleets.
- An app already deployed + woken on `compute-01` with
  state=`running`.

### Tier A5 procedure

1. **Identify the test instance** —
   `psql -c "select id, app_id, node_id, state from instances
   where state='running' and node_id=(select id from
   compute_nodes where name='compute-01');"`
2. **Trigger the drain** in Operations → Nodes and retain the returned
   `node_drain` intent ID.
3. **Watch the handoff** — within
   `MigrateLiveLeaseSeconds` (90 s) + ~5 s:
   `psql -c "select id, app_id, node_id, state,
   migrated_from_node_id, migrated_at from instances where
   id='<test-instance-id>';"` should now show
   `state='running'` + `node_id` flipped to `compute-02`'s id +
   `migrated_from_node_id` populated.
4. **Verify the metric on `compute-02`** —
   `curl -s http://compute-02:9100/metrics | grep
   schedd_live_migration_decisions_total` shows
   `outcome="migrated"` ≥ 1 and `outcome="peer_failure" = 0`.
5. **Verify the metric on `compute-01`** — same query; the
   source should show `outcome="peer_failure" = 0` (clean
   handoff).
6. **Smoke-test the customer experience** —
   `curl https://<app>.compute-02.example.com/`. The response
   should arrive within ~350 ms of the successful `node_drain` intent
   (cold-boot from snapshot on the destination's wake path).

### Tier A5 validation matrix

| Check | Expected | Source |
|-------|----------|--------|
| `instances.node_id` after drain | flipped to `compute-02.id` | `select id, node_id, state from instances` |
| `instances.state` after drain | `'running'` | same query |
| `instances.migrated_from_node_id` | non-null, equals `compute-01.id` | same query |
| `instances.migrated_at` | non-null, within last 5 min | same query |
| `apps.migrated_at` | non-null, within last 5 min | `select id, migrated_at from apps` |
| `schedd_live_migration_decisions_total{outcome="migrated"}` on `compute-02` | ≥ 1 | `/metrics` |
| `schedd_live_migration_decisions_total{outcome="peer_failure"}` on `compute-01` | 0 | `/metrics` |
| `schedd_live_migration_decisions_total{outcome="no_headroom"}` | 0 (or 1 if the ceiling was tight; pre-flight must show headroom > 1 instance) | `/metrics` |
| `make leakcheck` | zero leaked netns/TAPs/cgroups on both nodes | `make leakcheck` |

### Tier A5 rollback

If the handoff stalls in `state='migrating'` past the lease
(`MigratingWatchdogTickLimit` × `MigratingWatchdogIntervalSeconds`
= 50 s):

- The Tier A6 watchdog (`pkg/sched/migrating_watchdog.go` →
  `Engine.ReconcileExpiredMigrations`) hard-deletes stuck
  instances; `apps.migrated_at` reverts on next re-attempt.
- Operator-facing: Operations → Nodes → **Activate** restores the source
  for retry; a new drain intent re-runs the handoff.
- If the typed operation cannot make progress, declare break-glass and
  follow [database repair](../break-glass/database-repair.md). Direct row
  deletion is never part of the normal rollout path.

### Tier A5 escalation

- **No metric increment on either node** — the live_migrator
  pg_notify subscriber (`cmd/schedd/main.go::subscribeLiveMigrator`)
  is disconnected. Check `journalctl -u faas-schedd` for
  `subscribe_live_migrator` errors.
- **`outcome="no_headroom"` > 0** — destination has no RAM
  headroom for the migration target. Drain another peer first,
  or temporarily bump the destination's
  `compute_nodes.admission_ceiling_mb`.
- **`outcome="lease_expired"` > 0** — wire latency between
  schedd and vmmd exceeds the lease window. Bump
  `FAAS_MIGRATE_LIVE_LEASE_SECONDS` on the destination schedd
  (the env override is `pkg/api/limits.go::MigrateLiveLeaseSeconds`).

## Follow-ups (not in this runbook)

- **#250 (off-host Postgres backup)** — ✓ shipped (PR #784,
  ADR-056 v1.0). No longer a production-safety blocker; the
  Tier 2 pre-requisites bullet was retired from ADR-025 on
  2026-08-09.
- **#316 (`host.age` rotation runbook)** — shipped
  (ADR-057); 30-day overlap via `gregalectl host-age` CLI +
  `secretbox.LoadHostKeys` multi-identity plumbing. v2
  follow-up (`issue-316-followup-rekey`) covers the
  background re-seal of pre-rotation envelopes.
- **Active-passive HA: ADR-083** (accepted 2026-08-16) — see
  `docs/runbooks/active-passive-ha.md` for the dedicated
  runbook (lex-min leader election over
  `compute_nodes.name WHERE active=true`, standby warm-up,
  drain protocol). Multi-host horizontal-scale (this runbook)
  is now orthogonal: you run BOTH for full HA coverage. A
  `make ha-failover-drill` target on the two-node Lima fleet
  (`deploy/lima/faas-metal-2node-ha.yaml`) exercises the
  failover end-to-end.

## Acceptance

This runbook is required by issue #297 acceptance item 4. The
acceptance test is operator-side: walk the procedure end-to-end on
a staging `compute-01`, every step produces the cross-linked
artifact (the `compute_nodes` row, the capacity report row, the
placement landing on the new host). The Pre-conditions block must
render as the `> [!CAUTION]` admonition above, not a paragraph.
