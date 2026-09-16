# Standby write-redirect runbook (Tier A9 / ADR-089)

> **Acceptance status:** this historical drill is retained for the ADR-089
> evidence trail. Its local virtualization harness is retired. Run the
> supported native x86_64 acceptance gates from
> [`docs/ops/native-m9-acceptance.md`](../ops/native-m9-acceptance.md); do not
> use the legacy shell wrapper on a production host.

This runbook is the operator-facing counterpart to
`docs/adr/089-standby-write-redirect.md`. It closes the §14
M9 row "Standby write-redirect" (was deferred in
ADR-083 §"Open follow-ups" #3) and covers the day-2
operations of the writeGate that sits in
`cmd/gatewayd-internal`'s `apidProxy`.

The writeGate makes a standby's `apid` surface **deterministic**
relative to the leader: a mutating request that lands on a
standby is either relayed to the leader over mTLS (bearer /
anonymous) or 307-redirected to the leader's public URL
(cookie). The customer never sees a 5xx from a stale-DNS
write.

## Pre-flight

Before relying on the writeGate, verify:

1. **Tier A8 / ADR-083 is shipped and green.** The runbook
   assumes the active-passive topology is in place: lex-min
   leader election in `pkg/gateway/leader`, the
   `gatewayd_public_gateway_standby_state` gauge, the
   `compute_node_changed` pg_notify subscription, and the
   drain protocol. Without these, the writeGate's resolver
   has no leader to redirect to.
2. **Tier A7 / ADR-070 is shipped.** The runbook assumes
   `gatewayd-public` is the plain-HTTP public ingress behind the upstream
   Caddy/Cloudflare TLS edge and
   `gatewayd-internal` is the routing + wake + proxy layer.
   The gate sits in `gatewayd-internal`'s `apidProxy` —
   if the Tier A7 split hasn't landed, the gate is wired
   into the wrong daemon.
3. **mTLS material is in place on every box** at the
   canonical path:
   - `/etc/faas/tls/gatewayd/egress-client.crt`
   - `/etc/faas/tls/gatewayd/egress-client.key`
   - `/etc/faas/tls/gatewayd/ca.crt`
   This is the ADR-052 PR-C+D keep set — the cert CN/Directory
   stays `gatewayd` for this slice. The cert layout is the
   operator-level mTLS hop material, not the gatewayd-daemon-
   specific material; do not invent a new cert CN or
   Directory in this slice.
4. **`FAAS_LEADER_REDIRECT_TLS_CERT` is set on every box.**
   The gate is opt-in: it is constructed only when this
   env var is present in the operator's
   `~/.config/faas/env`. Without it, the gate is bypassed
   and every box accepts writes locally (the
   pre-Tier-A9 behaviour). A bare-bones dev box on a
   single-node fleet can deliberately leave this unset.
5. **`compute_nodes.public_ip` is populated for every
   active node.** The 307-redirect's `Location:` header is
   built from this column; an empty `public_ip` would
   produce a malformed URL. Set via the
   `gregalectl compute-nodes set-public-ip <name> <ip>` CLI.
6. **The four `StandbyWrite*` quotas are wired in
   `pkg/api/limits.go`** (they ship with Tier A9 — no action
   needed unless you've explicitly zeroed them):
   - `StandbyWriteRedirectTimeoutMS = 5000` — outbound
     mTLS hop timeout.
   - `StandbyWriteRetryAfterSeconds = 5` — `Retry-After`
     on `redirect_307` / `mTLS_failure` / `error`.
   - `StandbyWriteLeaderURLCacheTTLSeconds = 5` — resolver
     cache TTL.
   - `StandbyWriteNoLeaderRetryAfterSeconds = 60` —
     `Retry-After` on `leader_unreachable`.

## Procedure

The operator's day-2 surface for Tier A9 is the **drill**.
Tier A9 is a read-only improvement over the pre-Tier-A9
behaviour; the drill is the §14 M9 acceptance gate, and a
green drill is the steady-state signal.

```sh
# Pre-flight: tier-A8 drill must have run at least once.
make ha-failover-drill

# Tier A9 drill. The drill is read-only — it does NOT
# toggle compute_nodes.active. The leader identity is
# whatever the prior tier-A8 drill + ongoing election
# has settled on. The drill exits 0 only when:
#   - $standby.{relayed,bearer} counter advanced by ≥ 1
#   - $standby.{redirect_307,cookie} counter advanced by ≥ 1
#   - $standby.{leader_unreachable,*} unchanged
#   - the 307 response carries Retry-After: 5 +
#     Location: https://<leader>/...
make ha-write-redirect-drill
```

The historical drill drove the public listener on each box (loopback
`https://127.0.0.1:8080/v1/apps`), sends
one bearer write + one cookie write to the standby, and
asserts the closed-vocabulary counter increments in
`gatewayd_internal_write_redirect_total`. Its exit-code contract is retained
for historical incident review; the production acceptance flow is native-only.

## Validation matrix

This is the load-bearing artifact. Every row is a closed
`outcome` × `auth_kind` cell that a green runbook execution
asserts. The matrix is the operator's on-call surface — when
a customer reports "writes are failing", the row tells you
which counter to look at.

| Scenario                                | outcome               | auth_kind | Status                  | Retry-After |
|-----------------------------------------|-----------------------|-----------|-------------------------|-------------|
| Leader handles write                    | `same_box`            | any       | local                   | —           |
| Standby bearer write                    | `relayed`             | `bearer`  | leader response         | —           |
| Standby anonymous write                 | `relayed`             | `anonymous` | leader response       | —           |
| Standby cookie write                    | `redirect_307`        | `cookie`  | 307 + Location: leader  | 5           |
| No active leader                        | `leader_unreachable`  | any       | 503                     | 60          |
| DB / pg-notify error                    | `leader_unreachable`  | any       | 503                     | 60          |
| Inbound `X-Faas-Forwarded-Leader`       | `loop_prevented`      | any       | 508                     | —           |
| mTLS handshake failure                  | `mTLS_failure`        | any       | 503                     | 5           |
| Outbound relay transport error          | `error`               | any       | 503                     | 5           |
| Read method / carve-out / non-apid path | (bypass — silent)     | any       | next.ServeHTTP          | —           |

Notes on the matrix:

- **`same_box` is the leader's local path.** A request that
  arrives at the leader's `apid` is incremented once (with
  the customer's auth_kind) and dispatched normally. This
  cell is silent on the standby's dashboard.
- **`redirect_307` is cookie-only.** A relay that stamped
  the cookie through mTLS would leak it to the leader's
  request log. The 307 keeps the cookie on the user's
  browser.
- **`leader_unreachable` is shared** between empty-active-set
  and DB/notify error. Per ADR-089 §Decision #4, the
  customer-facing response is identical; the dashboard
  surfaces the precise cause via the metric label only.
- **`loop_prevented` is a configuration error, not a
  transient.** No `Retry-After`; the customer SDK should
  treat 508 as a hard failure and surface it to the operator.
- **`bypass` is intentional silence.** Reads, carve-outs
  (logs handler, webhook HMAC), and non-apid paths do NOT
  increment the counter. The `cold-boot-must-always-work`
  invariant (ADR-005) is preserved — the gate is a
  write-only concern.
- **Per-account cardinality is out of scope.** The closed
  `auth_kind` set keeps cardinality bounded at 7×3=21 cells.
  Per-account labels would explode the metric.

## Rollback / recovery

Tier A9 is read-only: the gate is constructed in addition
to the existing apid handler chain, not in place of it. To
disable the gate across the fleet:

1. **`FAAS_LEADER_REDIRECT_TLS_CERT=` (empty) on every
   box.** The env var is the opt-in flag; clearing it
   causes `cmd/gatewayd-internal/run.go` to construct no
   gate, and the apidProxy dispatches writes directly to
   the local apid.
2. **Restart `cmd/gatewayd-internal`** on every box. The
   gate is constructed once at boot from the env var.
3. **Verify the metric is silent.** The
   `gatewayd_internal_write_redirect_total` counter is
   pre-instantiated for all 21 cells; a fully-disabled
   fleet surfaces zero from every box.

The fleet is now in the pre-Tier-A9 state: every box
accepts writes locally. The active-passive topology
(ADR-083) is still in place — the read side is unchanged.
The rollback is a one-env-var flip; no code change
required.

## Escalation

- **Drill counter regression** (a row that previously
  asserted `≥ 1` now asserts `0`): check the mTLS material
  on the relay box (the egress-client cert may have
  expired). The `gregalectl pki rotate` CLI handles renewal;
  the gate's `mTLS_failure` outcome + cross-link on
  `gateway_active_passive_failovers_total` surfaces this.
- **Leader drift** (the standby is now being treated as the
  leader, or vice versa): the resolver cache may be stale.
  The `pg_notify` subscription on `compute_node_changed`
  drains the cache; if the subscription is broken, the
  drill's pre-flight will surface a `leader_unreachable`
  cell advancement.
- **Cert expiry** (relay hops fail with TLS errors): renew
  via the existing `gregalectl pki rotate` flow. The
  `mTLS_failure` outcome is the dashboard signal; the
  runbook's matrix row documents the response.
- **`loop_prevented` cell advances in production** (a 508
  is reaching a customer): this is a code bug, not a
  transient. The operator's drill + runbook pin the relay
  stamp to the outbound; a leaked inbound stamp means the
  receiving leader's symmetric check is firing. Page the
  on-call; do NOT roll back (rollback leaves the cluster
  in the silent-corruption-adjacent state Tier A9 was
  designed to fix).

## References

- ADR-089 (this slice's source of truth) —
  `docs/adr/089-standby-write-redirect.md`
- ADR-083 (Tier A8 active-passive topology) —
  `docs/adr/083-active-passive-ha-topology.md`
- ADR-070 (Tier A7 edge split) —
  `docs/adr/070-gatewayd-public-internal-split.md`
- ADR-052 (PKI / cert layout, keep set) —
  `docs/adr/052-pki-and-cert-layout.md`
- §14 M9 row: `docs/faas_implementation_spec.md`
- Tier A8 sibling runbook:
  `docs/runbooks/active-passive-ha.md`
- Historical drill script: retired local harness (not supported for production)
- Makefile target: `make ha-write-redirect-drill`

## Acceptance

This runbook is closed when §14 M9 flips green:

- [x] ADR-089 written (`docs/adr/089-standby-write-redirect.md`).
- [x] `pkg/gateway/writegate` package with closed vocabulary
      (PR-A / PR #761).
- [x] `pkg/apid/router.go::IsApidPath` shared predicate
      (PR-B / PR #797).
- [x] `CachedLeaderResolver` + `MTLSLeaderClient` +
      `writeGate` (PR-B / PR #797).
- [x] `make ha-write-redirect-drill` Makefile target.
- [x] Historical read-only drill script (retired local harness).
- [x] Standalone runbook (this file).
- [x] Property test `tests/property/write_redirect_test.go`
      with 5 invariants.
- [x] Runbook e2e `cmd/e2e/standby_write_redirect_e2e_test.go`.
- [ ] Native x86_64 manual smoke: relay + 307 + leader flip +
      zero 5xx within 30s (remaining M9 evidence).
