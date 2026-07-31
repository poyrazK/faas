# Multi-host rollout — adding a second compute node

Issue #297 acceptance item 4. Operator procedure for adding a second
compute node to the fleet (the cut-over from single-host to
multi-host). This is the v1 reference for the multi-host topology —
the moment we go from one box (a single `default-local` row in
`compute_nodes`) to two boxes where each carries its own admission
ceiling, its own capacity reports, and its own share of the cluster
vCPU budget.

The shape mirrors `docs/ops/gatewayd-tls-cutover.md` — cut-over
sequence + rollback criterion + verification + escalation. The
sibling active-passive HA topology runbook is
`docs/runbooks/gate-a.md` (this doc covers the multi-host
horizontal-scale variant, not active-passive).

> [!CAUTION]
> **This runbook is staging-only until Tier 1 Phase 2 ships.**
>
> The following pre-conditions are NOT all met at this doc's
> publication time. Re-read the runbook before each cut-over;
> status flips are tracked at
> [docs/adr/025-decoupled-control-plane-and-compute.md §Tier 2
> pre-requisites](../adr/025-decoupled-control-plane-and-compute.md#tier-2-pre-requisites).
>
> - **Tier 1 Phase 1 (mTLS, issue #95 slice 2)** — ✓ shipped
>   in PR #445. Stdlib verifier does chain + SAN + EKU;
>   handler-layer peer binding is in place per ADR-052. Wire
>   package: `pkg/wire.DialContext` / `wire.Listen`. Cert
>   material under `/etc/faas/secrets/{ca,schedd,vmmd,...}/`
>   at 0400 root:root, generated via `gregale pki init`.
> - **Tier 1 Phase 2 (`node_signature` on `CapacityReport`)** —
>   ✓ shipped in PR #457 (issue #95 slice 2 follow-up). The
>   `CapacityReport` proto carries a `node_signature` field
>   (ECDSA-P256 over the canonical-JSON payload, key-id-bound
>   to the leaf cert's public key). `pkg/sched/capacity.go`
>   exposes `SignNodeReport` (vmmd-side) and
>   `VerifyNodeSignature` (schedd-side); `ErrEmptySignature`
>   / `ErrSignatureMismatch` are the typed error returns.
>   Schedd rejects reports with an empty signature on slice-3
>   nodes and any report whose signature doesn't verify under
>   the leaf-cert public key recorded in the `compute_nodes`
>   row. The chooser's bias is now cryptographically bound to
>   the vmmd that emitted the report — placement-trust gap
>   closed.
> - **Tier 1 Phase 3 (`OCIRegistryStorageBackend` end-to-end,
>   issue #95 slice 3)** — ✓ shipped in PR #457. Per-app
>   layers now live in an OCI registry instead of replicated
>   per-compute-node; the §4.6 two-drive storage economics
>   hold at fleet scale. See `docs/adr/053-capacity-report-node-signature.md`
>   for the signature surface and
>   `docs/adr/054-oci-storage-backend.md` for the OCI plumbing.
> - **Tier 1 Phase 4 (per-host egress policy templating)** —
>   ✓ shipped in ADR-055. The static
>   `policy_nftables.conf` is now a Jinja2 template
>   (`policy_nftables.conf.j2`) that substitutes
>   `{{ public_iface }}` and `{{ masquerade_cidr }}` at the
>   two substitution sites. A Hetzner compute node on
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
>   (vmmd↔schedd, schedd→vmmd, gatewayd→vmmd) now installs
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
> - **#250 (off-host Postgres backup)** — ✗ NOT shipped.
>   Multi-host without off-host PG backup means a CP-host
>   loss is unrecoverable. The runbook is staging-only on
>   this ground alone, even with Tier 1 fully shipped.
> - **#316 (`host.age` rotation runbook)** — ✓ shipped
>   (PR for issue #316, ADR-057). 30-day rotation-overlap
>   window via `gregale host-age {init,rotate,status,prune-previous}`,
>   all five unseal sites migrated to `secretbox.OpenMulti`.
>   See `docs/ops/host-age-rotation.md` for the operator
>   runbook. v2 re-seal follow-up filed as
>   `issue-316-followup-rekey`.

## Topology

Two physical boxes at one Hetzner FSN + HEL pair (per spec §14
"Regional expansion"). Wire identity:

| Role                | Hostname       | `compute_nodes.name` | `target_url`                          |
|---------------------|----------------|----------------------|---------------------------------------|
| Control plane + 1st compute | `faas-fsn-1` | `default-local`      | `unix:///run/faas/vmmd.sock` (legacy) |
| 2nd compute (new)   | `faas-fsn-2`   | `fsn-2`              | `tcp://vmmd-2.faas:50051` (mTLS)      |

Both boxes share the same Postgres. The new box runs the full
daemon fleet (apid, schedd, vmmd, imaged, meterd, gatewayd,
builderd) — no "control plane on one box, compute on another"
split until Gate-B. The control plane is on `fsn-1`; the new
compute node advertises itself via vmmd's
`Schedd.ReportCapacity` client-stream (ADR-025 §4.1).

The Postgres `compute_nodes` row for `fsn-2` is created by the
operator via `POST /v1/compute-nodes` (ADR-029); vmmd's startup
self-registration UPSERTs the same row. The synthetic
`default-local` row stays untouched (hard-delete refused with HTTP
409 `default_local_protected`).

## Pre-flight

```sh
# 1. Confirm the new node has the daemon fleet provisioned.
ssh faas-fsn-2 'systemctl is-system-running && \
  for d in apid schedd vmmd imaged meterd gatewayd builderd githubd; do \
    systemctl is-active faas-$d || exit 1; \
  done'

# 2. Confirm the cert material was generated by PR #445's pki cmd.
ssh faas-fsn-2 'ls -la /etc/faas/secrets/{ca,vmmd,schedd}/'
# expect: ca.crt (0444), vmmd/{vmmd.crt (0444), vmmd.key (0400)}, ...

# 3. Confirm the leaf cert CN matches the daemon role.
ssh faas-fsn-2 'openssl x509 -in /etc/faas/secrets/vmmd/vmmd.crt -noout -subject'
# expect: subject=CN = vmmd.faas

# 4. Confirm the tier-1-phase-1 mTLS handshake works against the CP.
ssh faas-fsn-2 'openssl s_client -connect faas-fsn-1:7070 \
  -cert /etc/faas/secrets/vmmd/vmmd.crt \
  -key /etc/faas/secrets/vmmd/vmmd.key \
  -CAfile /etc/faas/secrets/ca/ca.crt <<<"Q"'
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
# forward-chain allow and the postrouting MASQUERADE. A Hetzner
# compute node on a different NIC name (e.g. ens5) overrides here;
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
> `make bootstrap`.

### 2. `make bootstrap` on the new node

```sh
ssh faas-fsn-2 'cd /opt/onebox-faas && git pull && sudo make bootstrap'
```

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
`gregale pki init --force` on the new node.

### 3. `compute_nodes` row — operator-side POST

On the control plane (`faas-fsn-1`), pre-register the new box:

```sh
curl -fsS -X POST 'https://faas-fsn-1:8081/v1/compute-nodes' \
  -H 'Authorization: Bearer <admin-bearer>' \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "fsn-2",
    "target_url": "tcp://vmmd-2.faas:50051",
    "vpcpus": 160,
    "mem_mb": 56000,
    "max_concurrency": 200,
    "admission_ceiling_mb": 47600
  }'
```

The row is `active=true` by default. The `compute_node_changed`
pg_notify trigger (migration 00026) fires and evicts gatewayd's
per-node client cache for any prior `fsn-2` entry. The admin POST
is idempotent — re-POSTing with the same name UPSERTs the row
(ADR-029).

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
vmmd registered node_id=... name=fsn-2 target_url=tcp://vmmd-2.faas:50051
vmmd capacity_publisher starting 1s tick
```

vmmd UPSERTs the `compute_nodes` row on startup. The two UPSERTs
(admin POST + vmmd self-registration) are idempotent — the row
ends with vmmd's view of `vpcpus`/`mem_mb`/`target_url`, which
should match the operator POST. If they don't match, the admin
POST was wrong; re-POST with the right values.

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
| 4 | Leaf cert CN                                       | `openssl x509 -in /etc/faas/secrets/vmmd/vmmd.crt -noout -subject`                       | `subject=CN = vmmd.faas`      |
| 5 | At least one instance placed on `fsn-2`            | `psql -c "select count(*) from instances where node_id = (select id from compute_nodes where name='fsn-2')"` | `count(*) > 0`                |
| 6 | Cert mode 0400                                     | `stat -c '%a' /etc/faas/secrets/vmmd/vmmd.key`                                           | `400`                          |
| 7 | Cert mode 0444 (crt)                               | `stat -c '%a' /etc/faas/secrets/vmmd/vmmd.crt`                                           | `444`                          |
| 8 | vmmd self-registered                               | `journalctl -u faas-vmmd -n 50`                                                          | `vmmd registered node_id=...` |
| 9 | `node_capacity_table` freshness                    | PromQL: `time() - max(node_capacity_table_sampled_at_unix_ms) < 5`                       | `1`                            |
| 10| `gateway_wake_latency_seconds` p95 ≤ 1 s           | PromQL: `histogram_quantile(0.95, rate(gateway_wake_latency_seconds_bucket[5m]))`       | `< 1`                          |

### 8. Rollback

If the cut-over surfaces a regression (e.g. fsn-2 rejects every Nth
wake, capacity reports stop after 30 s, mTLS handshake fails):

```sh
# 1. Drain fsn-2 from placement.
psql -c "UPDATE compute_nodes SET active=false WHERE name='fsn-2';"
# The compute_node_changed trigger fires; gatewayd evicts the
# cached conn; schedd's watchdog treats the row as drained;
# placement skips it.

# 2. Stop vmmd on the new node.
ssh faas-fsn-2 'sudo systemctl stop faas-vmmd'

# 3. Verify the cluster returns to single-box state.
psql -c "select name, active from compute_nodes"
# expect: default-local (t), fsn-2 (f)
```

The rollback is non-destructive — the `compute_nodes` row stays
in place with `active=false`, the cert material under
`/etc/faas/secrets/vmmd/` is untouched, and a re-rollout
(`UPDATE compute_nodes SET active=true WHERE name='fsn-2'` +
`systemctl restart faas-vmmd`) returns fsn-2 to service.

### 9. Escalation

If the cut-over fails irrecoverably:

- **Node-id mismatch** between schedd view (`compute_nodes.id`)
  and vmmd's self-registration — `make bootstrap` against the
  failed node with `--force` re-issues the leaves; the vmmd leaf
  CN must match the operator-POST row's `target_url` host.
- **mTLS handshake failure** — re-run `gregale pki status` on
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

## Follow-ups (not in this runbook)

- **Tier 1 Phase 2 (`node_signature`)** — closes the
  "CapacityReport trust" gap in spec §6.4. Until it lands,
  this runbook is staging-only.
- **Tier 1 Phase 3 (`OCIRegistryStorageBackend` end-to-end)** —
  closes the "Snapshot locality" gap. Without it, every compute
  node holds a local copy of every app's per-app layer.
- **#250 (off-host Postgres backup)** — required before the
  runbook is production-safe.
- **#316 (`host.age` rotation runbook)** — shipped
  (ADR-057); 30-day overlap via `gregale host-age` CLI +
  `secretbox.LoadHostKeys` multi-identity plumbing. v2
  follow-up (`issue-316-followup-rekey`) covers the
  background re-seal of pre-rotation envelopes.

## Acceptance

This runbook is required by issue #297 acceptance item 4. The
acceptance test is operator-side: walk the procedure end-to-end on
a staging `compute-01`, every step produces the cross-linked
artifact (the `compute_nodes` row, the capacity report row, the
placement landing on the new host). The Pre-conditions block must
render as the `> [!CAUTION]` admonition above, not a paragraph.
