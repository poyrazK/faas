# ADR-070 · Tier A7 edge split — gatewayd-public / gatewayd-internal

- **Status:** accepted (revised 2026-08-04 — TLS lives upstream; see
  "TLS at Caddy + Cloudflare" below)
- **Date:** 2026-08-03 (revised 2026-08-04)
- **Decision:** Split the monolithic `cmd/gatewayd` daemon into two
  single-purpose daemons connected by a unix-socket hop. The split
  is **in-process on a single box**: one box runs one `gatewayd-public`
  (plain-HTTP edge, the only public listener) fronting N
  `gatewayd-internal` replicas (routing + wake + proxy). Cross-box
  HA is achieved by having N boxes each with one `gatewayd-public`
  in front of their local `gatewayd-internal` set — NOT by putting
  multiple public listeners on one box.
- **Revised 2026-08-04:** `gatewayd-public` serves plain HTTP on
  `127.0.0.1:8080` (loopback only). TLS termination is the upstream
  Caddy + Cloudflare job (`api.gregale.dev`). certmagic + Hetzner
  DNS-01 and per-replica cert-bundle replication (sections 6 and
  the "Cert replication" decision) never made it into code — the
  actual production deployment shipped without them and the legacy
  `cmd/gatewayd/` was the consumer of `pkg/gateway/certsync` and
  the certmagic packages during the migration window.
- **Why:** The Tier A multi-box primitives (per-node schedd, snapshot
  de-localization, cross-node rebalance, cross-node live migration,
  migrating-instance watchdog — ADRs 062–067) are all in code. What's
  missing is the outer edge: how a customer HTTPS request lands on
  the right `gatewayd` when N gatewayd replicas exist on N boxes.
  Today `cmd/gatewayd` is monolithic — it owns TLS termination,
  certmagic, hostname→app routing, the wake gate, the per-node
  forwarder, and the cert storage. Putting N copies of that daemon
  behind an external LB gives us the §11 "single public listener"
  invariant for free but breaks per-process state (rate limiter
  buckets split, warm hints split, cert storage split, mTLS-to-vmmd
  not handled). Splitting it in-process — one `gatewayd-public`
  (plain-HTTP edge behind Caddy+Cloudflare) fronting N
  `gatewayd-internal` replicas (routing + wake + proxy) over a
  unix-socket hop — keeps the existing forwarder shape and lets us
  keep "the only public listener" as a per-box invariant
  (`gatewayd-public` is still ONE process surface per box).
- **Consequences:**
  - Two new daemons: `cmd/gatewayd-public` and `cmd/gatewayd-internal`.
    Legacy `cmd/gatewayd` stays in-tree for the migration window
    (the legacy and split daemons run side-by-side; the LB points
    at `gatewayd-public` once the operator flips the env).
  - `pkg/gateway/internal_proxy.go` (public→internal reverse-proxy
    over unix socket) and `pkg/gateway/readiness.go` (probe + PG
    ping + staleness signals) ship in production. `pkg/gateway/
    certsync/certsync.go` ships as the legacy daemon's leader
    election only — sweep in PR-C.
  - Three new migrations: 00116 `warm_hint` (sticky-warm hint
    table + `warm_hint_published` pg_notify), 00117
    `pg_ratelimit_counters` (centralized rate-limit counters,
    opt-in via `[ratelimit] mode = "central"`), 00118 reserve
    slot (follow-on ADR-069/070/071 fences).
  - Four new constants in `pkg/api/limits.go`:
    `GatewayDrainGraceSeconds=25`, `ReplicaHeartbeatIntervalSeconds=5`,
    `WarmHintCacheSize=1000`, `CertSyncIntervalSeconds=30`. The
    first three are consumed by the production daemons;
    `CertSyncIntervalSeconds` is currently consumed only by the
    legacy `cmd/gatewayd/` daemon's `pkg/gateway/certsync` path
    (sweep in PR-C).
  - CLAUDE.md §"Component ownership" invariant rewrites:
    "gatewayd is the only public listener on the box" →
    "the platform exposes exactly ONE public ingress per box; that
    ingress is `gatewayd-public` (plain-HTTP behind Caddy+Cloudflare).
    Internal-only `gatewayd-internal` listens on a unix socket
    inside the box. Cross-box remote ingress is `gatewayd-public`
    on the other box's public IP."
  - The pre-split `/readyz` always-200 default is **inverted** to
    always-503 when no probe is wired. The legacy daemon is
    migrated to a real probe (`deps.pgStore != nil`). The new
    daemons wire the full probe from `pkg/gateway/readiness.go`.
  - Two new systemd units (`faas-gatewayd-public.service`,
    `faas-gatewayd-internal.service`) + two new ansible roles
    (`gatewayd_public_service`, `gatewayd_internal_service`).
    `faas-gatewayd-public.service` is plain-HTTP at `127.0.0.1:8080`,
    `RestrictAddressFamilies=AF_UNIX AF_INET`, no
    `AmbientCapabilities=CAP_NET_BIND_SERVICE`, no
    `ReadWritePaths=/var/lib/faas/certs`.
  - Slot-neutral for the LEGACY migration set; **this PR cluster
    ships 116–118 as the new edge-tier slots** (the 116 reservation
    from the open issue #517 follow-up is preserved; we add 116
    fresh here). Cross-PR slot fence pattern documented below.

## Architectural decisions

1. **In-process split, not multi-process on one box.** We chose to
   keep "one public listener per box" as the load-bearing invariant
   (CLAUDE.md §11). The split happens INSIDE the public-listener
   process: `gatewayd-public` owns the box-side public-listener
   surface; everything inside (routing, wake, forwarder, rate
   limit) lives in `gatewayd-internal`. TLS terminates upstream
   (Caddy + Cloudflare — revised 2026-08-04 after the certmagic
   strip); this matches the §11 single-public-listener rule without
   inventing a new perimeter.
2. **Unix-socket hop, same box only in v1.0.** `gatewayd-public`
   dials `/run/faas/gatewayd-internal.sock` (ADR-015/018 pattern,
   mode 0660 + group faas) to forward every inbound request. The
   unix-socket hop is HTTP/1.1 over the unix socket — same shape
   as `pkg/sched/loop.go::httpGatewaySynth` already uses for the
   cron synth server. Cross-box mTLS hop is Gate-B work.
3. **Sticky-warm routing.** `gatewayd-public` reads the warm hint
   for the app and picks the internal replica whose
   `compute_node.id` matches the hint. The hint is published via a
   new `warm_hint` table (migration 00116) + `warm_hint_published`
   pg_notify channel; both daemons subscribe independently. On
   cache miss / no warm, the public daemon hashes `host||ip` to a
   replica (consistent hashing on the internal replica registry)
   and retries on the next replica if the chosen one fails
   `/readyz`.
4. **Internal replica registry.** Each `gatewayd-internal` posts a
   registration at startup over a unix socket
   (`/run/faas/gatewayd-public-replica.sock`) carrying
   `{compute_node_id, advertise_addr, started_at}`. The public
   daemon keeps a `nodeID → replica` map with a 5 s heartbeat
   (driven by `api.ReplicaHeartbeatIntervalSeconds`). Stale
   (>10 s without heartbeat) → marked unready → excluded from hash
   and warm-hint resolution. No new pg_notify channel; pure local
   pubsub.
5. **Centralized rate limit (opt-in).** Today's per-process token
   bucket (`pkg/gateway/ratelimit.go`) is correct for single-node. After
   the split, sticky-by-warm-node routing does NOT pin a single
   replica, so per-process buckets see a fraction of customer
   traffic and the limit leaks. The fix is opt-in via
   `[ratelimit] mode = "central"` in TOML and uses a Postgres-
   backed counter (migration 00117 — `pg_ratelimit_counters`).
   Default stays "process" (today's behaviour); operators flip when
   they go multi-box. Same staged-rollout risk mitigation as
   ADR-040.
6. **Cert replication (revised 2026-08-04 — never shipped).** The
   original ADR prescribed per-replica FileStorage +
   leader-by-lex-min (`pkg/gateway/certsync`) so N `gatewayd-public`
   replicas on N boxes could each terminate TLS with a fresh cert.
   The actual production deployment terminates TLS at Caddy +
   Cloudflare upstream, so the per-replica FileStorage + leader
   election never landed in `gatewayd-public`. The legacy
   `cmd/gatewayd/` daemon still consumes `pkg/gateway/certsync`
   during the migration window; sweep both together with the
   certmagic packages in PR-C.
7. **Readiness inversion.** The pre-split `/readyz` always-200
   default was a latent bug (cmd/gatewayd/main.go:878 wired `nil`).
   After the split, a partial-boot daemon must NOT accept traffic
   even if the LB scrape is intermittent. The new contract is:
   - `nil` probe → 503 (daemon forgot to wire a probe — wiring bug)
   - registered probe that returns false → 503 (with the most
     recent reason from any failing component)
   - registered probe that returns true → 200
   The pre-split daemon is migrated to a real probe
   (`deps.pgStore != nil`); the new daemons wire the full probe
   from `pkg/gateway/readiness.go` (PG ping + cache hydration +
   schedd router readiness + internal proxy dial).
8. **Migration slots.** This PR cluster ships 116–118:
   - 00116 `warm_hint` (table + pg_notify channel)
   - 00117 `pg_ratelimit_counters` (central rate-limit table)
   - 00118 reserve slot (follow-on ADR-069/070/071 fence)
   The pre-existing 116 reservation from the open issue #517
   follow-up is **preserved as 116 here** (same slot — the
   reservation is dropped on rebase per ADR-041). Cross-PR slot
   fence pattern (PR #391): rename + add no-op `select 1;` fence so
   the embedded set stays contiguous 1..N.
9. **Drain order.** Internal-first, public-second. `gatewayd-internal`
   drains on SIGTERM (sets `/readyz=503`, stops accepting from the
   unix socket, waits in-flight, posts `deregister` to
   `gatewayd-public`, exits). `gatewayd-public` then drains (sets
   `/readyz=503`, stops accepting on `127.0.0.1:8080`, waits in-flight,
   exits). The order matters: if the public daemon drains first,
   in-flight requests die on the unix socket.
10. **Network surface hardening.**
    `faas-gatewayd-internal.service` carries
    `RestrictAddressFamilies=AF_UNIX` — even a buggy code path
    can't reach an external IP from the internal daemon. The
    legacy `faas-gatewayd.service` was `AF_INET AF_INET6 AF_UNIX`
    because the legacy daemon dials external IPs through the
    forwarder; the internal daemon's only outbound is gRPC to
    per-node schedd/vmmd via `pkg/wire.DialContext` (loopback mTLS
    or unix socket). `faas-gatewayd-public.service` is
    `RestrictAddressFamilies=AF_UNIX AF_INET` (revised 2026-08-04
    after the certmagic strip — drop AF_INET6 because the bind is
    loopback v4).
11. **Listener boundaries.** `gatewayd-public` owns (revised 2026-08-05
    per PR-J after the :9090 bind-conflict with gatewayd-internal):
    - `127.0.0.1:8080` plain-HTTP listener (TLS terminates at
      Caddy upstream; certmagic + `:80`/`:443` are gone)
    - `/healthz`, `/readyz`, `/metrics` on loopback `127.0.0.1:9092`
      (was `127.0.0.1:9090` until PR-J; moved to break the
      bind-conflict with gatewayd-internal which owns :9090)
    - `pkg/httpsec` outer wrapper (HSTS / CSP nonce /
      X-Frame-Options / Referrer-Policy / X-Content-Type-Options /
      Permissions-Policy) — note that HSTS is benign over plain
      HTTP (RFC 6797 §8.1)
    `gatewayd-internal` owns:
    - `/run/faas/gatewayd-internal.sock` (HTTP/1.1)
    - `/healthz`, `/readyz`, `/metrics` on loopback `:9090`
      (`FAAS_GATEWAY_CONTROL_LISTEN`)
    - the entire `pkg/gateway/handler.go` surface
      (hostname→app, wake gate, rate limit, forwarder)

## Configuration

| Env var | Default | Purpose |
|---|---|---|
| `FAAS_PUBLIC_LISTEN_ADDR` | `127.0.0.1:8080` | `gatewayd-public`'s plain-HTTP listener (loopback; TLS at Caddy upstream). |
| `FAAS_PUBLIC_CONTROL_ADDR` | `127.0.0.1:9092` | `gatewayd-public`'s control listener (was `:9090` until PR-J — moved to break the bind-conflict with gatewayd-internal which owns `:9090`). |
| `FAAS_INTERNAL_SOCKET` | `/run/faas/gatewayd-internal.sock` | Unix socket the public daemon dials. |
| `FAAS_INTERNAL_CONTROL_ADDR` | `127.0.0.1:9090` | `gatewayd-internal`'s control listener. |
| `[ratelimit] mode` | `process` | `process` (legacy) or `central` (Postgres-backed counter). |
| `FAAS_NODE_ID` / `FAAS_NODE_NAME` | (PG lookup) | Legacy override for `compute_nodes` rows when the legacy daemon was bootstrapped off a fresh box. No consumer in `gatewayd-public` after the certmagic strip; the legacy daemon keeps the lookup, the public daemon does not. |
| `FAAS_HETZNER_DNS_TOKEN_PATH` / `FAAS_CERT_STORAGE_DIR` / `FAAS_APPS_DOMAIN` / `FAAS_ACME_CONTACT_EMAIL` | (deleted) | Were the certmagic DNS-01 surface; deleted in PR #633 alongside the certmagic strip. |

Constants live in `pkg/api/limits.go` (canonical hard-limits table;
never inline a limit per CLAUDE.md).

## State surface

- `warm_hint` table (migration 00116) — sticky-warm publication.
  Primary key `(app_id)`; CHECK on `written_at <= now()+1 minute`
  for clock-skew safety; partial index on `node_id` WHERE
  `node_id IS NOT NULL` for the future "list all apps warm on node
  X" query (operator dashboard).
- `warm_hint_published` pg_notify channel — published by schedd's
  `Broadcaster` (pkg/sched/warmhint.go) on every successful emit.
  Consumed by `gatewayd-public` (route-cache mirror at the edge)
  and `gatewayd-internal` (the existing StreamWarmHints consumer,
  migrated to the new pg_notify path).
- `pg_ratelimit_counters` table (migration 00117) — opt-in
  centralized rate-limit counters. PRIMARY KEY
  `(scope, subject_id, plan)`; CHECK on `scope ∈ {'app','account'}`;
  CHECK on `plan ∈ {free,hobby,pro,scale}`; CHECK on `tokens >= 0`;
  partial index on `subject_id` WHERE `scope = 'app'`. The
  consume path is `INSERT … ON CONFLICT … DO UPDATE SET tokens =
  tokens + delta … RETURNING tokens` wrapped in
  `pg_advisory_xact_lock(hashtext((scope,subject_id,plan)::record))`
  so two replicas contending on the same row serialise.

## Wire / proto

- **Zero new proto.** All wire changes are local to
  `pkg/gateway/` (the reverse-proxy library and the cert-sync wire
  format). The unix-socket hop is HTTP/1.1 over the existing
  `pkg/sched/loop.go::httpGatewaySynth` pattern. The cert-sync
  wire format is a 24-byte fixed header + concatenated PEMs
  (defined in `pkg/gateway/certsync/certsync.go::EncodeWire`).

## Critical files

- `pkg/gateway/readiness.go` (new) — `ReadySignal`, `ReadyzProbe`,
  `NewStalenessSignal`, `NewPGPingSignal`.
- `pkg/gateway/readiness_test.go` (new) — race-clean unit tests.
- `pkg/gateway/routes_hydration.go` (new) — `RouteCacheHydration`
  tracker + `RouteCacheLoader` seam.
- `pkg/gateway/routes_hydration_test.go` (new) — race-clean unit tests.
- `pkg/gateway/control.go` (edited) — inverted nil-ready default.
- `pkg/gateway/control_test.go` (edited) — updated nil-ready
  subtest to assert 503.
- `pkg/gateway/internal_proxy.go` (new) — public→internal reverse
  proxy over unix socket.
- `pkg/gateway/internal_proxy_test.go` (new) — hop-by-hop,
  X-Forwarded-For append, dial-failure 502, upstream 5xx pass-through.
- `pkg/gateway/certsync/certsync.go` (new) — leader election,
  peer sync wire format, file writer. **Legacy only** as of
  2026-08-04 — sweep in PR-C.
- `pkg/gateway/certsync/certsync_test.go` (new) — lex-min election,
  wire round-trip, magic + version rejection, file writer. Legacy
  coverage; PR-C cleans the package.
- `cmd/gatewayd/main.go` (edited) — wired real `ReadyFunc` at
  line 878 (was `nil`). Legacy daemon only.
- `cmd/gatewayd-public/main.go` (new) — plain-HTTP edge daemon
  (revised 2026-08-04: drops certmagic, secrets.go, certsync block,
  resolveNodeIdentity, pgNodeLister, domainLookup, runCertSyncLoop;
  binds `127.0.0.1:8080` loopback).
- `cmd/gatewayd-public/` — bootstraps httpsec, the internal-proxy,
  and the readiness probe (single PG-ping signal).
- `cmd/gatewayd-public/secrets.go` — deleted 2026-08-04 (the Hetzner
  token loader was the only consumer of `loadSecretFile`; the
  legacy daemon keeps its identical loader).
- `cmd/gatewayd-internal/main.go` (new) — routing + wake + proxy
  daemon (skeleton; the handler file moves land in a follow-on PR).
- `pkg/api/limits.go` (edited) — 4 new constants. `GatewayDrainGraceSeconds`,
  `ReplicaHeartbeatIntervalSeconds`, `WarmHintCacheSize` are
  production consumers; `CertSyncIntervalSeconds` is legacy-daemon
  only (sweep in PR-C).
- `migrations/00116_warm_hint.sql` (new) — `warm_hint` table +
  CHECK + partial index.
- `migrations/00117_pg_ratelimit.sql` (new) —
  `pg_ratelimit_counters` table.
- `migrations/00118_reserve_slot.sql` (new) — follow-on ADR fence.
- `deploy/systemd/faas-gatewayd-public.service` (new) —
  plain-HTTP edge unit (revised 2026-08-04: drops
  `CAP_NET_BIND_SERVICE` + `/var/lib/faas/certs` ReadWritePaths;
  binds `127.0.0.1:8080`; `RestrictAddressFamilies=AF_UNIX AF_INET`).
- `deploy/systemd/faas-gatewayd-internal.service` (new) — unix-only
  internal unit with `RestrictAddressFamilies=AF_UNIX`.
- `deploy/ansible/roles/gatewayd_public_service/` (new) — ansible
  role for unit drop (revised 2026-08-04: drops
  `/var/lib/faas/certs` + `/var/lib/faas/ca` directory-creation
  tasks).
- `deploy/ansible/roles/gatewayd_internal_service/` (new) — ansible
  role for the unix-socket-only unit.
- `CLAUDE.md` (edited) — Component ownership invariant rewrite
  (plain-HTTP semantics, not TLS-only).

## Tests

- `pkg/gateway/readiness_test.go`:
  - `TestReadyzProbe_All_EmptyReturnsTrue` — empty probe reports
    ready (pre-split behaviour preserved for early-boot compatibility).
  - `TestReadyzProbe_Register_DefaultsNotReady` — every newly
    registered signal starts at not-ready.
  - `TestReadyzProbe_All_FoldsSignals` — fan-in: every signal must
    be ready for All() to return true.
  - `TestReadyzProbe_All_ConcatsReasons` — operator-visible
    reasons joined with `"; "`.
  - `TestReadyzProbe_ReadyFunc_StableUnderConcurrency` — race-clean
    read path.
  - `TestNewPGPingSignal_*` — flips on success, flips on error,
    stopper flips not-ready on drain.
  - `TestNewStalenessSignal_*` — fresh touch keeps ready; staleness
    flips not-ready; touch recovers.
- `pkg/gateway/routes_hydration_test.go`:
  - `TestRouteCacheHydration_*` — defaults, MarkHydrated,
    MarkUnhydrated, idempotency.
  - `TestRouteCacheLoader_Contract_OnSuccess` / `_OnFailure` —
    success hydrates + populates; failure keeps not-hydrated and
    surfaces the reason.
- `pkg/gateway/control_test.go` (updated):
  - The pre-existing `ready by default` subtest is renamed to
    `not-ready when no callback registered` and asserts 503 with
    body containing `no probe registered`.
- `pkg/gateway/internal_proxy_test.go`:
  - Hop-by-hop header stripping.
  - X-Forwarded-For append (not replace).
  - Dial failure → 502 with `internal dial failed` body.
  - Upstream 5xx → propagated unchanged.
  - Nil dialer / nil target → 502 wiring bug.
  - `TestNewUnixSocketDialer_RespectsContextCancel` — ctx-cancel
    abort within 100 ms.
  - `TestIsHopByHop_Predicate` — the lookup table.
- `pkg/gateway/certsync/certsync_test.go`:
  - `TestLeader_LexMinElection` / `_Follower` / `_EmptyLister` /
    `_RecomputeError` / `_PeersExcludesLeader` — election
    semantics.
  - `TestLeader_Renew_FollowerRejected` — `ErrNotLeader` for
    followers; closure NOT called.
  - `TestLeader_Renew_LeaderDelegates` — leader passes closure
    through.
  - `TestEncodeDecodeWire_RoundTrip` — wire round-trip.
  - `TestDecodeWire_BadMagic` / `_BadVersion` / `_ShortBuffer` —
    rejection safety.
  - `TestWriteCertAndKeyToDisk` — file writer + 0600 perm check.
  - `TestLeader_ConcurrentReads` — race-clean.

## Verification

- `make test` — full unit suite, must pass with `-race`.
- `make test-metal` — exercise the legacy + split daemons on
  Lima / a reference control-plane node (the legacy daemon stays in-tree during the
  migration window).
- `make leakcheck` — zero leaked netns/TAPs/cgroups.
- `make lint` — `golangci-lint` + repo-wide `gofmt -l` gate.
- `make spec-check` — vacuum + AST parity + git clean.
- `make migrations-check` — embedded set stays contiguous 1..118.
- Manual smoke (Lima / reference control-plane node), revised 2026-08-04:
  1. `make bootstrap && make run` with one `gatewayd-public` +
     one `gatewayd-internal`.
  2. `curl -s http://127.0.0.1:9090/readyz` → 200 after PG ping
     succeeds.
  3. `curl -s http://127.0.0.1:8080/healthz` → 200 once both
     listeners are bound (revised: plain HTTP, not TLS).
  4. Confirm Caddy is upstream and reverse-proxying to
     `127.0.0.1:8080`; the actual customer-app smoke
     (`https://app.apps.gregale.dev/`) is Caddy's job, not
     `gatewayd-public`'s.
  5. Kill `gatewayd-internal`; the customer's HTTPS request gets
     502 Bad Gateway (`internal dial failed`); restart internal,
     request succeeds again.

## Migration slot

**116–118.** This PR cluster ships three migrations:

- 00116 `warm_hint` (table + pg_notify)
- 00117 `pg_ratelimit_counters`
- 00118 reserve slot (follow-on ADR-069/070/071 fence)

The pre-existing 116 reservation from the open issue #517
follow-up is **preserved as 116 here** — the reservation is
dropped on rebase per ADR-041 / PR #391 carve-out. The follow-on
PRs (cert-bundle audit, hint retention, LB-coordination tokens
per ADR-069/070/071) claim slots 119+; their renumber chain
follows the existing post-#533-merge renumber-reset pattern.

## Open follow-ups (deliberately deferred)

- Cross-box `gatewayd-public` ↔ `gatewayd-internal` mTLS hop
  (Gate-B; out of scope).
- Cross-region `gatewayd-public` replication (Gate-B; out of scope).
- Capacity-aware internal-replica selection so the public daemon
  doesn't pick a saturated replica even when the warm hint says
  otherwise (Tier A8 follow-on).
- The full file moves from `cmd/gatewayd/` to `cmd/gatewayd-internal/`
  (the handler, pgbackend, scheddrouter, nodecache, forwarder, etc.)
  land in a separate PR cluster to keep review surface small.
- HAProxy / cloud-LB story for operators who want to put
  `gatewayd-public` behind an external LB anyway (deferred to ops docs).
- A "soft" version of sticky-by-app that allows hot-app replicas
  to spread to multiple internal replicas if their load diverges
  (cap on per-replica saturation).

## Rejected alternatives

- **Multi-process on one box (`gatewayd-public` × N + `gatewayd-internal`
  × N behind an external LB).** Rejected — violates CLAUDE.md
  "single public listener per box" and breaks per-process state
  (rate limiter buckets split, warm hints split). The whole
  point of the split is to KEEP single-process state on the
  internal tier while presenting a single plain-HTTP surface
  to the upstream Caddy+Cloudflare. (TLS is now Caddy's job;
  the original 2026-08-03 ADR spoke of "single TLS surface to
  the public" — superseded 2026-08-04.)
- **`apps.last_warm_node_id` column.** Rejected — same writer-
  invariant problem (would force schedd to write a customer-intent
  table); also breaks schedd/apid writer roles.
- **Sticky-by-app routing.** Rejected for v1.0 — sticky-by-warm-
  node is the brief; sticky-by-app with a rebalanced app = new
  replica ≠ old bucket = broken invariant.
- **Centralized rate limit in Redis.** Rejected — new dependency,
  out of scope. The Postgres-backed counter has the right
  latency characteristics (P50 0.8 ms, P99 3.2 ms on the reference node per
  ADR-040 follow-up bench).
- **Single combined `gatewayd-public` + `gatewayd-internal`
  process that switches mode via env.** Rejected — the two
  daemons have different resource limits, different systemd
  units, different restart policies. A combined process can't
  restart one tier without restarting the other.
- **Don't split — just put the legacy daemon behind an external
  LB.** Rejected — per-process state breaks (rate limit buckets
  split, warm hints split). The brief asks for a clean
  state-isolation model; the split is the only path that
  delivers it.

## PR-E note (2026-08-09)

This ADR is the source of truth for the `gatewayd-public` /
`gatewayd-internal` split and is intentionally NOT carrying the
PR-E narration banner that the rest of the `docs/adr/` files picked
up. The body references `cmd/gatewayd/` only where it describes the
pre-split era or the legacy daemon's specific responsibilities (the
`pkg/gateway/certsync` consumer, the `cmd/gatewayd/main.go:878`
wiring fix) — those references are the historical substance of the
split and stay verbatim. Cite this ADR rather than the banner when
explaining the split.

## Amendment 1 — Legacy single-box fallback removed (multi-host safety cluster PR-7)

The legacy FAAS_SCHEDD_SOCKET shortcut in
`cmd/gatewayd-internal/scheddrouter.go::clientForNode` is
REMOVED. The router now always dials the per-node target from
`compute_nodes.schedd_target_url`; the env-var override no
longer exists.

The shortcut existed because the synthetic default-local row
(migration 00090) carries the canonical production socket, and
the e2e harness relocates the socket per test. In a multi-box
fleet the shortcut would silently swap a foreign-owned wake
onto the local box. The PR-5 owner gate catches the wake at
`Engine.EnsureWake`, but the dial still happened — wasted
work and an unnecessary FailedPrecondition surface. Removing
the shortcut closes the audit F5 gap.

The setter `PGBackend.WithLegacySingleBox` is retained as a
no-op for backwards compatibility with existing wiring. The
`resolveSched` transient-miss fallback (resolves through
`b.sched` on resolver ok=false / empty NodeID / clientForApp
miss) is removed — it would route a foreign-owned app through
the local schedd in a multi-box fleet and surface a 503 storm.

Operators who need a non-canonical default-local target must
update the `compute_nodes.schedd_target_url` row directly.
