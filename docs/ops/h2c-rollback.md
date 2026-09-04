# H2C bridge rollback

Operator runbook for rolling back the bridge-side H2C terminator
(ADR-126 / PR #1050, hardened ADR-127 / G19.1). The v2 bridge now
reuses one process and H2C transport per live instance. Three
switches, in order of escalation:

- **Switch 0 — lifecycle rollback.** `FAAS_STREAM_BRIDGE_PERSISTENT=0`
  on vmmd. Keeps the v2 wire protocol but returns to process-per-RPC
  bridge startup. Use for a suspected reuse, socket, or child-lifecycle
  defect while preserving H2C framing.

- **Switch 1 — per-vmmd surgical rollback.** `FAAS_BRIDGE_PROTOCOL=h1`
  on the bridge process. Per-stream fallback to H1+chunked. Use for:
  a single bad app, a region, a vmmd, or an H2C-capable image.
- **Switch 2 — wholesale rollback.** `FAAS_STREAM_BRIDGE_VERSION=v1`
  on vmmd. Reverts to the v1 shell bridge (pre-PR #750). Use for: a
  v2 binary defect, a wire-shape regression, or a Go-stdlib H2C
  server bug.

All switches are env-var only; changing one requires restarting vmmd
so new bridge children receive the setting. The mirror-image rollout
runbook is at
`docs/ops/h2c-rollout.md`.

## When to roll back

| Signal | Source | Severity | Switch |
|--------|--------|----------|--------|
| `FaasBridgeFramingMismatch` warn alert sustained > 1h | Prometheus `bridge.rules.yml` | warn | Switch 1 (surgical) |
| `FaasBridgeRollbackStuck` page alert sustained > 4h | Prometheus `bridge.rules.yml` | page | Switch 1, then escalate to Switch 2 if it does not resolve |
| `bridge panic:` in `journalctl -u faas-vmmd` | slog (Layer 9 `defer recover()`) | page (always — `defer recover` is defense-in-depth, not a fix) | Switch 1 first; if panics recur after Switch 1, escalate to Switch 2 |
| App report: `app_protocol=grpc` returning H1 trailers on the wire | customer support escalation | high | Switch 1 on the app's vmmd |
| Region-wide H2C handshake failures | `bridge-protection` dashboard Panel 4 outlier | page | Switch 2 wholesale |
| vmmd process restart loop after a vmmd-stream-bridge upgrade | systemd journal | page | Switch 2 wholesale |

Use Switch 0 first when framing is correct but requests fail only after
the first invocation, or when stale bridge sockets/child processes are
observed. It does not change the customer-visible HTTP/1.1 vs H2C choice.

Default policy: **start with Switch 0 for lifecycle symptoms, otherwise
Switch 1**. Switch 0 is reversible and preserves the v2 wire shape;
Switch 1 is surgical,
takes ~10 s to roll out (per-vmmd `systemctl edit` + `daemon-reload`
+ `restart`), and is reversible in seconds. Switch 2 is wholesale
and reverts the entire fleet to the pre-ADR-126 shell bridge;
escalate to Switch 2 only if Switch 1 fails to resolve or if the
failure is at the vmmd binary level rather than the bridge.

## Switch 0 — lifecycle (`FAAS_STREAM_BRIDGE_PERSISTENT=0`)

```sh
sudo systemctl edit faas-vmmd
# In the editor, add:
#   [Service]
#   Environment=FAAS_STREAM_BRIDGE_PERSISTENT=0
sudo systemctl daemon-reload
sudo systemctl restart faas-vmmd
```

Verify:

```sh
sudo systemctl show faas-vmmd --property=Environment
# expect: ... FAAS_STREAM_BRIDGE_PERSISTENT=0 ...
```

Reverse it by removing the override and restarting vmmd. With no
override, persistent reuse is enabled for the v2 path.

## Switch 1 — per-vmmd surgical (`FAAS_BRIDGE_PROTOCOL=h1`)

Per-vmmd override; one box at a time. The bridge reads
`FAAS_BRIDGE_PROTOCOL` per-request via
`cmd/vmmd-stream-bridge/framing.go::currentBridgeFraming()`; setting
it to `h1` forces every request on this vmmd through `handleH1Stream`
regardless of the app's `app_protocol`. The forwarder still stamps
`x-faas-protocol` so dashboards keep showing what the customer's
intent was; the wire-shape is what changed.

```sh
sudo systemctl edit faas-vmmd
# In the editor, add or replace:
#   [Service]
#   Environment=FAAS_BRIDGE_PROTOCOL=h1
sudo systemctl daemon-reload
sudo systemctl restart faas-vmmd
```

Verify:

```sh
sudo journalctl -u faas-vmmd -f | grep 'framing selected'
# expect: framing=h1 app_protocol_env=h1 method=POST path=/<route> ...
```

Grafana signal: `bridge-protection` Panel 3 ("Active
`bridge_protocol=h1` (surgical-rollback apps)") ticks above 0 within
30 s. Panel 2 (MISMATCH rate) drops back to 0 within 5m (the alert
window). Panel 1's `http2/h2c/match` and `grpc/h2c/match` rows fall
to 0; `http2/h1/match` and `grpc/h1/match` rise in their place.

If Switch 1 does not resolve within 15 min, escalate to Switch 2.

### Reverse Switch 1

When the underlying issue is fixed (image version bump, app code
fix, stdlib upgrade), remove the override:

```sh
sudo systemctl edit faas-vmmd
# Remove the [Service] Environment=FAAS_BRIDGE_PROTOCOL=h1 block,
# OR set it back to h2c if promotion has happened:
#   [Service]
#   Environment=FAAS_BRIDGE_PROTOCOL=h2c
sudo systemctl daemon-reload
sudo systemctl restart faas-vmmd
```

Verify the framing-selection slog returns to `framing=h2c
app_protocol_env=h2c` and Panel 3 returns to 0.

## Switch 2 — wholesale (`FAAS_STREAM_BRIDGE_VERSION=v1`)

Fleet-wide fallback to the pre-ADR-126 shell bridge. Affects every
app on the vmmd regardless of `app_protocol`. Use only if Switch 1
fails to resolve or if the failure is at the vmmd binary level
(panic that survives Switch 1, repeated vmmd restart loop).

```sh
sudo systemctl edit faas-vmmd
# In the editor, add:
#   [Service]
#   Environment=FAAS_STREAM_BRIDGE_VERSION=v1
sudo systemctl daemon-reload
sudo systemctl restart faas-vmmd
```

Verify:

```sh
sudo systemctl show faas-vmmd --property=Environment
# expect: ... FAAS_STREAM_BRIDGE_VERSION=v1 ...

sudo journalctl -u faas-vmmd -f | grep -E 'bridge|stream'
# expect: v1 shell bridge spawn logs, no "framing selected" line
# (v1 doesn't emit the slog line at all — see ADR-028 amendment).
```

Grafana signal: `bridge-protection` Panel 1 falls to 0 across the
closed cross-product (v1 doesn't emit `vmmd_bridge_framing_total`).
Panel 3 falls to 0 (no h2c framing in flight). The dashboard
goes "dark" by design — the absence of data IS the signal that the
rollback succeeded.

Customers running `app_protocol ∈ {http2, grpc}` will receive their
trailers as plain `Trailer:` headers over H1+chunked during the
fallback — same pre-ADR-126 behaviour they had before PR #1050.
Document the incident in the `Rollout history` table in
`docs/ops/h2c-rollout.md`.

### Reverse Switch 2

```sh
sudo systemctl edit faas-vmmd
# Remove the Environment=FAAS_STREAM_BRIDGE_VERSION=v1 line.
sudo systemctl daemon-reload
sudo systemctl restart faas-vmmd
```

Verify: `journalctl -u faas-vmmd` shows `vmmd-stream-bridge` spawning
the v2 binary; `vmmd_bridge_framing_total{app_protocol=http2,bridge_protocol=h2c,framing=match}`
returns to non-zero within 30 s of the first request.

## Snapshot invalidation on rollback

The bridge wire-shape is **decoupled from the snapshot invalidation
policy**: snapshots on disk are not touched by either Switch 1 or
Switch 2. `app_protocol=http1` apps stay valid forever
(`pkg/state/pgstore.go::MarkAllSnapshotsStaleByAppProtocol` skips
them; only `app_protocol ∈ {http2, grpc}` rows are stale-marked on
the F3 sweep introduced in `pkg/imaged/loop.go::runFCSweep`).

Operator-driven invalidation on rollback is **not required** —
Switch 1 / Switch 2 only changes the wire-shape the bridge speaks,
not the image the guest runs. If a customer asks "will my data be
deleted?" the answer is no; snapshots stay parked until the next
cold-boot (which always works per ADR-005).

The `warm_snapshot_stale` audit rows tagged
`subject="app_protocol:v1"` are the F3 sweep's record of which
`{http2, grpc}` snapshots were marked stale. A reversal (i.e. a
re-promotion after Switch 2 was triggered) does **not** auto-clear
the audit rows — the F3 sweep is one-way per PR.

## Escalation ladder

1. **`FaasBridgeFramingMismatch` fires** (warn, 1h) → on-call
   inspects the dashboard; if scope is a single app, Switch 1 on
   that app's vmmd; if scope is a region, Switch 1 on every vmmd
   in the region.
2. **`FaasBridgeRollbackStuck` fires** (page, 4h) → Switch 1 fleet-wide;
   if mismatch rate does not drop to 0 within 15 min, Switch 2.
3. **`bridge panic:` lines in journalctl** → Switch 1 immediately;
   page the secondary on-call; escalate to Switch 2 within 15 min
   if panics recur.
4. **Sustained customer impact** (3+ customers reporting
   `app_protocol=grpc` trailers-broken) → Switch 2 wholesale without
   waiting for Switch 1; the bridge is broken at the wire-shape
   level, not the per-app level.
5. **vmmd process restart loop** → Switch 2 wholesale; if restart
   loop persists after Switch 2, the vmmd binary is broken (out of
   scope for this runbook) — escalate to the vmmd on-call rotation.

## Cross-links

- `docs/ops/h2c-rollout.md` — mirror-image rollout procedure.
- Spec §4.1 line 115 — `docs/faas_implementation_spec.md`.
- ADR-126 §Decision 7 — the two-rollback-switch design (`docs/adr/126-bridge-h2c-terminator.md`).
- ADR-127 §D2 — hardening overlay (`docs/adr/127-g19.1-h2c-bridge-hardening.md`).
- STATUS.md M8.6 — wire-shape + hardening overlay status.
- `deploy/ansible/roles/prometheus/files/bridge.rules.yml` —
  `FaasBridgeFramingMismatch` + `FaasBridgeRollbackStuck` rule
  definitions.
- `deploy/grafana/bridge-protection.json` — companion dashboard.

## Testimonials

> "Switch 1 saved our bacon during the [REDACTED-2026-08-XX]
> incident: one bad app, three minutes to scope, ten seconds to
> roll back the one vmmd." — on-call SRE
>
> "Switch 2 looks scary in writing but the rollback is mechanical;
> the dashboard going dark is the green signal, not the red one."
> — on-call SRE
