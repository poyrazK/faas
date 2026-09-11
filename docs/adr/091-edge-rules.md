# ADR-091 · Edge rules (issue #561 / routes#rev r=2)

- **Status:** accepted (PR-cluster landing PR-1 through PR-5)
- **Date:** 2026-08-10
- **Issue:** #561 (customer-facing per-request routing/decisions: kind=route / rewrite / redirect / headers / cors / jwt / ip)
- **Related:** spec §4.1.2 (gateway hot-path ordering), ADR-070 (Tier A7 edge split — the architecture that places `gatewayd-public` and `gatewayd-internal` on opposite sides of a unix socket), ADR-009 (the identical inner network world that lets PR 3's `kind=route` substitute freely), ADR-001–010 (inlined in spec §3), §11 (egress / host-age / cgroups posture that bounds the JWKS URL validator), ADR-041 (slot-reservation pattern used by the matching migration).
- **Supersedes:** the per-deployment "routing rules" feature deferred from issue #561 / Q3'26.

## Context

Customers want per-host request shaping beyond what deployments expose today.
A typical request is "host `foo.example.com` → rewrite `/v1/*` → `/api/*`, allow
CORS from `https://app.example.com`, require a Bearer JWT signed by our IdP, and
block any client outside `192.0.2.0/24` from `/admin/*`". The deployments row
today only carries run-time concerns (image, env, RAM, concurrency) — a separate
resource is needed for the **per-host decision set**. Issue #561 frames this as
"edge rules", borrowing Cloudflare's Ruleset Engine vocabulary: a ruleset has 7
kinds of rules, each rule attaches to (host, path, methods), and ordering
between rules of different kinds matches the engine.

The seven kinds are intentionally narrow:

| kind        | Effect                                                            | Free?  |
|-------------|-------------------------------------------------------------------|--------|
| `route`     | Substitute the inbound host with another deployment's app         | yes    |
| `rewrite`   | Mutate `r.URL.Path` (single-shot prefix rewrite)                  | yes    |
| `redirect`  | 3xx the inbound request to a different URL                         | yes    |
| `headers`   | Stamp request/response headers (allowlist; bans Host/Content-* / `x-faas-*`) | yes |
| `cors`      | CORS preflight short-circuit + response-header stamper             | yes    |
| `jwt`       | Inbound Bearer JWT verifier (per-rule JWKS, iss/aud/alg/claims)   | paid   |
| `ip`        | Allow/deny CIDR list evaluation against trusted XFF                | paid   |

PRs 1-4 of the rollout (#799 schema, #815 CLI, #820 kind=route matcher, #824
kind=rewrite/redirect/headers matcher) all cite "ADR-089 / issue #561" in
code comments. `grep -i 'edge rule' docs/adr/` returns nothing — ADR-089 is
occupied by build-status-endpoint / secret-rotation / standby-write-redirect /
pr-cluster-outline, none of which are about edge rules. **This ADR closes the
cite-bug in one shot** by replacing every "ADR-089 / edge rules" reference with
"ADR-091 / edge rules". (The other ADR-089 siblings — including secret-rotation
PR-A/B/C — keep their cite numbers untouched.)

## §3 Sub-decisions

1. **Seven kinds, three plan tiers.** CORS is Free. JWT and IP are Hobby+.
   `kind=route|rewrite|redirect|headers` are Free and unconstrained. Plan gating
   fires at create-time in `cmd/apid/handlers_edge_rules.go:184-201` via
   `state.EdgeRuleKind.IsPaidOnly()`; the 402 problem includes a stable `code`
   (`plan_edge_rule_kind_not_allowed`) and a docs URL.

2. **Each rule carries its own (host, path, methods) match filter, priority, and
   same-account binding.** Path-globs validate via `path.Match` at apid
   create-time AND at gateway compile time (PR 3 R3 fix — never trust the SQL
   hotfix path). Priorities sort ASC; per-kind matchers return the
   lowest-numbered match. Cross-account rules at the gateway log `edge_rule
   .{kind}_blocked` audit + `outcome=blocked` metric and fall through silently
   (PR 4 pattern).

3. **`pg_notify` invalidation is wholesale** (`pkg/gateway/EdgeRuleCache.Reset()`).
   No new pg_notify channel for PR 5. Per-host LRU mirrors `RouteCache`. The
   cache is `Reset()`-on-`db.NotifyEdgeRuleChanged` (single ops surface — every
   update funnels through one notify; gatewayd-internal subscribes; PR 6 will
   add the property test that pins the wholesale invariant).

4. **Hot-path ordering in `ServeHTTP`** (spec §4.1.2 + Cloudflare Ruleset
   Engine):

   ```
   matchAndSubstituteRoute          # PR 3: kind=route (only kind before Backend.Lookup)
     → Backend.Lookup               # host→app cache+PG (skipped on kind=route hit; goto haveApp)
     → matchAndApplyRedirect        # PR 4: kind=redirect (3xx short-circuit)
     → matchAndApplyRewrite         # PR 4: kind=rewrite (mutates r.URL.Path)
     → applyEdgeRuleHeaders         # PR 4: kind=headers (response header ops)
     → applyEdgeRuleCORS            # PR 5: kind=cors (preflight 204 short-circuit)
     → applyEdgeRuleJWT             # PR 5: kind=jwt  (post-headers, pre-wake-gate)
     → applyEdgeRuleIP              # PR 5: kind=ip   (cheap deny before auth)
     → enforceRequireAuthn
     → enforcePublicAuth
     → Backend.Pick
     → proxy leg
   ```

   JWT and IP precede per-deployment authority gates so a JWT-failed request
   never reaches `require_authn=true`. CORS, JWT, and IP all precede
   `Backend.Pick` so a rejected request never pays a cold-wake cost. The
   `kind=route` exception (only kind that runs before `Backend.Lookup`) is
   the substitution semantic — `matchAndSubstituteRoute` overwrites `app`
   and `goto haveApp` so downstream auth/wake/proxy sees the *target* app.
   Spec §4.1.2 documents this ordering authoritatively; this ADR and §4.1.2
   must stay aligned. **Note on the original D4 wording (PR 6 review fix):**
   the initial PR-cluster draft of this D4 said "CORS precedes redirect" but
   the actual production ordering (handler.go:2284-2296) is redirect first,
   CORS second — redirect is cheaper (3xx) than evaluating a preflight that
   won't apply anyway when a 3xx rule matches the host/path. The D4 prose
   above is the corrected version, aligned with handler.go:2284.

5. **Same-account enforcement is the only security boundary at the gateway.**
   Cross-account rules fall through silently. Apid create-time rejects
   same-account violations up front (existing `App.AccountID` check);
   `cmd/gatewayd-internal/edge_rules.go`'s `Match*` methods add a runtime
   same-account assertion as defense-in-depth even if the create-time check
   were bypassed.

6. **Metrics** — single counter widens: `gateway_edge_rule_match_total
   {kind, outcome}`. The pre-instantiation loop (`pkg/gateway/metrics.go
   :528-535`) initialises the cross-product `{kind ∈ route|rewrite|redirect|
   headers|cors|ip|jwt} × {outcome ∈ match|miss|blocked|failed|missing}` from
   the first scrape so dashboards see all labels without a "first-scrape gap".
   PR 5 widens the outcome vocab to 5 (added `failed`, `missing`) but the
   `{match,miss,blocked}` triple continues to work for the 4 prior kinds.
   No new `CounterVec`.

7. **Audit emissions** — `edge_rule.<kind>_matched` / `edge_rule.<kind>_blocked`
   for the 4 prior kinds. PR 5 widens: `edge_rule.jwt_matched|blocked|failed
   |missing` (4 kinds, since JWT has more failure modes than the others);
   `edge_rule.cors_matched|blocked`; `edge_rule.ip_matched|blocked`. Audits
   are emitted through the existing `gatewaydEdgeRulesAud.Emit` thin wrapper
   (`cmd/gatewayd-internal/edge_rules.go:440-460`).

8. **CORS preflight short-circuit** in `applyEdgeRuleCORS`: OPTIONS + a
   matching `kind=cors` rule → 204 with `Access-Control-Allow-*` headers,
   no `Backend.Lookup`. Mirrors `http.Redirect`'s pattern (write header,
   never `Write()`). Non-preflight stamps `Access-Control-Allow-Origin`
   via the `statusRecorder` hook (`pkg/gateway/handler.go`, response-side
   header-mutation pattern). PR 5 ships `applyEdgeRuleCORS` as the canonical
   reference shape.

9. **JWT verification** — `pkg/edgejwks` (NEW) wraps `go-jose/v4/jwk.NewCache`
   for the JWKS fetch+rotation work and exposes a narrow `Cache`/`Verifier`
   interface so cmd-side code never imports `go-jose` directly. Clock
   skew is `60s` (matches Auth0/Okta/Cognito defaults). Verifier returns
   typed errors (`ErrJWTMissingToken`, `ErrJWTExpired`, `ErrJWTBadSignature`,
   `ErrJWTWrongIssuer`, `ErrJWTWrongAudience`, `ErrJWTMissingClaim`,
   `ErrJWKSNotRegistered`) so `applyEdgeRuleJWT` can pick the right audit
   kind (`jwt_missing` vs `jwt_failed`).

10. **JWKS URL defense-in-depth at apid-Validate** — `EdgeRuleJWTAction
    .Validate()` (PR 5) requires `JWKSURL` start with `https://` AND rejects
    the RFC1918 / loopback / link-local prefix list (`https://localhost`,
    `https://127.*`, `https://10.*`, `https://192.168.*`, `https://169.254.*`,
    `https://[::1]`, `https://[fc*::*`, `https://[fd*::*`). Cheap string-prefix
    check (defense-in-depth; a future ADR can upgrade to `net.ParseIP` +
    `IsPrivate`/`IsLoopback`/`IsLinkLocalUnicast` for IPv6-multicast edges).
    §11 egress posture already forbids RFC1918 / link-local / metadata ranges
    at the host firewall — this is the application-layer equivalent.

11. **HS\* dropped from JWT algorithm vocabulary** — PR 1's `pkg/api/dto.go
    ::edgeRuleJWTAllowedAlgs` originally listed `RS256/384/512 + ES256/384/512
    + HS256/384/512`. PR 5 shrinks the vocabulary to the 6 asymmetric
    algorithms only. Rationale: HS\* over JWKS means a symmetric key served
    from a public endpoint — anyone with the URL can forge tokens. The action
    shape has no `secret_ref` field (only `jwks_url`), so HS\* was structurally
    broken. If customers later need HS\* with a shared secret, that's a new
    `secret_ref` action shape and a new ADR. CLI vocab (`cmd/gregale/commands
    _edge_rules.go::edgeRuleJWTAlgVocab`) shrinks in lockstep. Validator 422s
    `HS*` immediately rather than failing at runtime.

12. **CORS `*`+credentials footgun guarded at apid-Validate** — `EdgeRule
    CORSAction.Validate()` (PR 5) returns 422 if `AllowOrigins: ["*"]`
    combined with `AllowCredentials: true`. Browsers reject this combination
    (`Access-Control-Allow-Origin: *` cannot pair with `Allow-Credentials:
    true`). Cheaper to reject at create-time than to ship a rule that
    silently fails in production. The gateway stamper trusts validated input.

13. **Client-IP provenance for `kind=ip`: X-Forwarded-For single trusted hop.**
    The Tier A7 architecture puts `gatewayd-public` and `gatewayd-internal`
    on opposite sides of a unix socket (`pkg/gateway/internal_proxy.go:84-91`,
    `cmd/gatewayd-public/main.go:1-43`). When a request crosses the unix
    socket, `r.RemoteAddr` in `gatewayd-internal` is the unix-socket peer,
    **not** the customer's IP. The customer IP is forwarded via the single
    trusted `X-Forwarded-For` entry that `pkg/gateway/internal_proxy.go
    :286-289` writes (`internal_proxy_test.go:149` pins `len == 1`).
    `applyEdgeRuleIP` reads that one entry via the helper `clientIPFrom
    TrustedXFF(r *http.Request) (net.IP, bool)`. Defense-in-depth:
    `len(r.Header.Values("X-Forwarded-For")) != 1` falls through with
    `edge_rule.caller_ip_forged` audit (deny-by-default posture). The
    gateway-side assertion is the load-bearing one — even if a misbehaving
    caller appends to inbound XFF, `internal_proxy.go:286` strips the chain
    before re-adding the public daemon's `RemoteAddr`, so the gateway
    only ever sees the trusted 1-hop value.

14. **JWKS cache TTL** — `jwk.NewCache` with `WithMinRefreshInterval(5 *
    time.Minute)` and the background refresh every **15 minutes** (matching
    Cloudflare Access). Memory cap = **1024 keys per URL**; on overflow the
    cache evicts and re-fetches. The `pkg/edgejwks.Cache` interface is
    narrow (`Get | Register | Reset`) so cmd-side code never imports
    `go-jose` directly.

15. **Per-rule JWKS URL, not account-level** — each `kind=jwt` rule stores
    its own `jwks_url`. Account-level JWKS deferred to a future ADR if
    customers ask. Per-rule keeps tenancy boundaries obvious and avoids
    creating a separate "jwks_endpoints" table at PR-time.

16. **No new migrations.** PR 1 already added the `edge_rules` table
    (`migrations/00192_edge_rules.sql`) with all 7 kinds in the schema
    CHECK constraint. PRs 2-5 are non-schema. PR 6 ships D17 (LRU
    property test), D18 (per-kind e2e), D19 (spec §4.1.2 backfill),
    and D20 (deferred items).

17. **Property-test invariant for wholesale `Reset()` (PR 6, ship-blocking).**
    PR 6 ships a property-style test (`FuzzEdgeRuleReset_WholesaleInvalidatesAllKinds`)
    alongside a deterministic `TestEdgeRuleReset_WholesaleAcrossAllSevenKinds`
    in `pkg/gateway/edge_rules_test.go`. Both pin the wholesale
    `pg_notify`-driven `Reset()` invariant across all 7 kinds — a
    regression against any single kind fails the test, surfacing as a
    cache-consistency violation for ALL 7 kinds simultaneously. A
    companion test in `cmd/gatewayd-internal/edge_rules_test.go`
    (`TestGatewaydEdgeRules_ResetForwardsToCache`) pins the production
    `gatewaydEdgeRules.Reset()` as a one-line `cache.Reset()` forward,
    with `fakeEdgeRuleStore.calls` proving `Reset()` does NOT re-hit
    the store. D3's promise that PR 6 would add the wholesale-RESET
    property test is fulfilled here.

18. **e2e cross-process coverage of all 7 kinds (PR 6, ship-blocking).**
    PR 6 ships per-kind e2e tests under `cmd/e2e/edge_rules_<kind>_e2e_test.go`
    covering the apid → gatewayd-internal path. Bitmask: `e2etest.APID
    | e2etest.Gatewayd` (no schedd/vmmd/imaged — those wake microVMs
    and PR 6's bitmask budget excludes them). The six non-route kinds
    (rewrite / redirect / headers / cors / jwt / ip) all run at
    `haveApp` (spec §4.1.2) AFTER `Backend.Lookup`, so each per-kind
    test seeds a `kind=route` substitute (synthetic host → real test
    app slug) as a precondition — mirroring the production pipeline
    shape. Kind=jwt seeds the rule via direct `h.Pool.Exec` insert
    (bypassing apid-Validate, which rejects `https://127.*` JWKS URLs
    — D10's defence-in-depth stays in production); the gateway-side
    wire is the surface under test. Per-kind minimum coverage: one
    status-only happy path + one negative path. PR 6 is the
    rollout-closer; kind=route e2e is the most thoroughly unit-tested
    kind already (PR 3 + `cmd/gatewayd-internal/edge_rules_test.go`)
    and is deferred to D20.

19. **Spec §4.1.2 backfill (PR 6, ship-blocking).** Spec §4.1.2 is
    backfilled with the canonical hot-path ordering table from D4.
    ADR-091 D4 and spec §4.1.2 cross-reference each other
    bi-directionally. The CORS `*`+credentials guard (D12), HS\* drop
    (D11), and JWKS URL network-position guard (D10) are now
    spec-level decisions, not just ADR-level — they live in
    `docs/faas_implementation_spec.md` §4.1.2 "Defense-in-depth guards"
    so a reader hitting the spec doesn't have to dig into ADR-091 to
    learn that HS\* is dropped. Future contributions that try to add
    an HS\* action shape MUST propose a new ADR (D11 footnote); the
    spec guards make this clear. **Section-number correction (PR 6
    review fix):** the original D4 and this ADR's forward-cite at the
    top of the file pointed to "spec §13.4". That section number was
    incorrect — §13 is RAM budget ledger. The correct home is §4.1.2
    under `gatewayd-public` ⏵ `gatewayd-internal` — edge proxy (spec §4.1).
    PR 6 corrects all six forward-cites in `pkg/gateway/handler.go`
    (lines 954, 2277, 3126), `pkg/gateway/edge_rules.go:101`, and the
    two ADR-091 cites (lines 6, 64).

20. **Deferred items (follow-on PRs, non-blocking for PR 6).** The
    following follow-on items surfaced during PR 6 review and are
    explicitly deferred — they DO NOT block PR 6 merge:
    - **D20.1 — kind=route e2e cross-process coverage.** Adds
      `cmd/e2e/edge_rules_route_e2e_test.go` requiring
      `e2etest.DeployWake` (schedd + vmmd + imaged) and creating two
      stub apps on the same account, then asserting `kind=route`
      substitutes across hosts with a real `Backend.Lookup`. Defer
      until §14 M6 acceptance gets a stable stub-app fixture;
      `deploy_wake_metal_test.go` is the right shape.
    - **D20.2 — Account-level JWKS.** Per-rule JWKS URLs ship in PR 5
      (D15); an account-level `jwks_endpoints` table is deferred
      until a customer asks.
    - **D20.3 — Audit retention SLO.** Audit rows from
      `edge_rule.*_matched|blocked|failed|missing` are best-effort
      today. A retention SLO (e.g., 30 days) needs an
      operator-obs/audit-search ticket (#817 in flight).
      **Amendment 2 (PR-B residual, 2026-08-12):** retention is
      NOT shipped by this PR — ADR-075 already ships the 90-day
      SOC 2 CC6.2 floor (`pkg/eventretention.Cleanup`). D20.3 is
      re-scoped to **SLO + observability** on top of the prune
      loop: three new wire metrics
      (`apid_audit_events_deleted_total`,
      `apid_audit_events_retention_lag_seconds`,
      `apid_audit_events_volume_total{kind_prefix}`) plus a new
      runbook (`docs/runbooks/FaasAuditRetentionExhaustion.md`).
      ADR-075 + this PR close the runtime gap; the operator-obs
      ticket (#817 in flight) closes the dashboard chip in D20.4.
    - **D20.4 — Grafana observability chip.** PR-B (PR #857) shipped
      the three prerequisite metrics in `pkg/wire/metrics.go:1389-1425`:
      `apid_audit_events_deleted_total`,
      `apid_audit_events_retention_lag_seconds`,
      `apid_audit_events_volume_total{kind_prefix}`. PR-D20.4 closes
      the operator surface with three Prometheus alert rules
      (`FaasAuditRetentionLoopStalled` page, staleness-based — the
      gauge itself reads ~90d on every healthy pass and does NOT grow
      between passes, so the alert uses `time() - timestamp(gauge) >
      93600`; `FaasAuditRetentionLoopStretched` warn at 50h;
      `FaasAuditRetentionTableGrowingFasterThanPruned` warn with a
      `volume_rate > 0` precondition to avoid firing on idle days when
      the deleted counter is flat) and the Grafana dashboard
      `faas-audit-retention-d20-4` (3 panels: prune rate, retention
      lag with green/yellow/red thresholds at 89-91d healthy, and
      `topk(8, ...)` volume by kind_prefix). All alerts/panels
      reference `docs/runbooks/FaasAuditRetentionExhaustion.md`.
      Alertmanager routing keys on `severity` (page / warn / info),
      so the new `family: audit_retention` label requires no routing
      change. **Shipped.**
    - **D20.5 — Per-rule rate limit.** Token-bucket per rule (e.g.,
      "this JWKS-protected endpoint gets 100 RPS per IP"). Out of
      ADR-091 — new ADR needed.
      **Amendment 3 (issue #881, PR-A + PR-C + PR-D + PR-E, 2026-08-13):**
      D20.5 lands as an *amendment* to ADR-091 (matching the
      D20.6 / D24 precedent), not a new ADR slot. The shape:
      - **kind=throttle** as an 11th edge-rule kind
        (migration 00244 — extends the union from 11 to 12
        values; the kind CHECK widens in the same DDL).
      - **Sub-plan ceiling only.** A throttle rule is STRICTLY a
        tightening primitive: `requests_per_second ≤ plan.RateLimitRPS`
        and `burst ≤ plan.RateLimitBurst`. The apid validator
        (pkg/api/dto.go::EdgeRuleThrottleAction.Validate) enforces
        the ceiling; the gateway compiler enforces it again at
        load time and clamps + warns. A customer cannot raise
        their plan limit by registering a throttle rule.
      - **Bucket key: `appID + "\x00" + ruleID`.** Rule-scoped,
        so cardinality is bounded by *configured rules*, not by
        traffic. Per-IP sub-keying is deliberately out of v1
        (memory-bounded limiter + attacker-controlled IP
        cardinality = unbounded bucket growth).
      - **Prerequisite: LRU eviction.** The existing Limiter has
        no eviction; the throttle bucket-multiplies the
        cardinality. New `NewLimiterWithLRU(cap)` constructor
        + **full-bucket-only invariant** — only buckets with
        `tokens >= burst` are evictable; partially-drained buckets
        are pinned to prevent a forced-eviction bypass. Cap =
        `pkg/gateway.EdgeRuleCacheCap` (10,000) for consistency.
      - **Phase 1 (PR-A, ships alone):** read-only recommender.
        `GET /v1/apps/{slug}/throttle-suggestions?range=` returns
        `suggested_rps = clamp(ceil(observed_rps * 2), 1, plan.RateLimitRPS)`
        per route, with `__route_other__` excluded from the
        suggestions. New `pkg/promql.QueryGrouped` seam +
        `pkg/throttlerec` package.
      - **Phase 2 (PR-C + PR-D, land together):** enforcement.
        `applyEdgeRuleThrottle` runs between `applyEdgeRuleLimit`
        and the global MaxBytesReader on the hot path. Rejected
        requests (jwt/ip/geo) don't consume a route token. Throttle
        runs BEFORE the wake gate so throttled traffic never wakes
        a VM. 429 carries `x-faas-rate-limit-scope: route` +
        `X-RouteRateLimit-{Limit,Remaining,Reset}`.
      - **Quota (Free-allowed, per-kind cap):**
        `Limits.EdgeRulesThrottlePerApp` Free=1 / Hobby=5 / Pro=25
        / Scale=100. Same precedent as `kind=geo` (Free-allowed
        with a per-app cap; not gated by `IsPaidOnly`).
      - **Per-consumer/API-key keying remains OUT of scope**
        (Phase 3 — needs the ADR-040 policy question settled:
        opaque X-API-Key header vs JWT claim). D20.5 closed.
      **Amendment 4 (issue #881 Phase 3, ADR-104, 2026-08-14):**
        Per-consumer keying lands as a *further* amendment to D20.5,
        not a new ADR. ADR-104 picks the policy: per-rule opt-in
        (`key_by ∈ {"none","api_key","consumer_id","jwt_subject","jwt_claim"}`),
        bounded by `max_keys_per_rule` (Free 100 / Hobby 1000 /
        Pro 5000 / Scale 10000) with a `__other__` collapse when
        the cap is exceeded. The `__other__` bucket is pinned
        non-evictable to prevent an attacker from weaponising
        key-space growth to bypass the throttle. Phase 3 is the
        *edge-rule path* — a separate code path from ADR-040's
        wake-path policy, which is preserved verbatim. Mega-PR
        cluster outlined in
        `/Users/poyrazk/.claude/plans/serialized-greeting-fairy.md`;
        slot fence at `migrations/00267_reserve_slot.sql`
        (renumbered from 00266 after cross-PR precheck caught
        the ADR-101 OIDC cluster owning 00265/00266).
      **Amendment 5 (issue #881 Phase 4, 2026-08-18):** Three
        rate-limit improvements amend ADR-104 (not 091 — these are
        rate-limit improvements, not edge-rule kind additions).
        Mega-PR cluster; see ADR-104 amendment 5 for full detail:

        1. **Central mode for `pg_ratelimit_counters`** (the table
           that migration 00126 created with zero Go readers).
           Originally a "Rejected alternative" in ADR-104;
           amendment 5 FLIPS that bullet. The table is now read
           by the gateway hot path under `[ratelimit] mode =
           "central"` (TOML knob, default `"local"` for back-compat).
           Fast-path-cache pattern: in-process bucket serves
           hot-path Peek; central counter is consulted only when
           the in-process bucket would reject (ADR-070 bench
           P50 0.8ms / P99 3.2ms applies to the boundary case only).
           Cross-replica invalidation via `pg_notify
           'rate_limit_changed'`. The 00126 CHECK is widened
           to `scope IN ('app','account','rule')` via migration
           00281. Phase 3 scope is per-app + per-account + per-rule
           only — per-consumer central mode is Phase 4 (the PK
           does NOT include `consumer_id`; the `__other__`
           collapse bucket stays in-process until then).
        2. **`X-RouteRateLimit-Policy` per-consumer scope hint
           on the 429 path (wire-only).** New sibling header of
           `X-RouteRateLimit-{Limit,Remaining,Reset}`. Values:
           `route` (back-compat default for `KeyBy ∈ {"", "none"}`)
           or `per-consumer` (when the consumer collapsed into the
           `__other__` bucket). The existing
           `x-faas-rate-limit-scope` enum is untouched.
        3. **Dry-run preview on
           `GET /v1/apps/{slug}/throttle-suggestions`** (parameter
           sweep, not auto-apply). New query params:
           `dry_run=true&candidate_rps=N&candidate_burst=N`. Returns
           the existing `suggestions` slice unchanged plus a new
           `would_have_rejected` array (one row per route) with the
           over-cap count + a per-consumer limitation note. The
           recommender origin (D20.5 amendment 3, Phase 1) is
           extended with this guard-rail; the recommender remains
           INTENTIONALLY ADVICE-ONLY. Per-consumer limitation:
           `gateway_requests_by_route_total` has no per-consumer
           labels today, so the preview can only answer "would ANY
           consumer on the rule's `__other__` collapse bucket have
           been rejected?" — surfaced on the wire, in the CLI
           (`gregale throttle-suggestions <slug> --dry-run …`),
           and in the ADR-104 amendment.

        Sequencing: lands as a single mega-PR cluster (4 atomic
        + 2 review-fix commits; pattern after PR #924 Mega-PR-A).
        Branch `feat-881-phase-3-per-consumer` carries the
        work; PR opens as DRAFT per mega-PR house style.
    - **D20.6 — CORS non-preflight e2e path.** PR 6 covers CORS
      preflight e2e; non-preflight stamp-the-Origin flow is
      unit-tested at `pkg/gateway/handler.go:1175-1220` and the e2e
      can add a `GET` happy-path assertion in a follow-up.
      **Amendment 2 (PR-B residual, 2026-08-12):** the e2e ship
      lands in the CORS-residual PR — `TestEdgeRulesCORS_NonPreflight_HappyPath`
      in `cmd/e2e/edge_rules_cors_e2e_test.go` with bitmask
      `APID|Gatewayd`, plus a new unit test
      `TestApplyEdgeRuleCORS_NonPreflight_EmitsApplySuccess` that
      pins the apply/match counter contract. D20.6 closed.
    - **D20.7 — kind=maintenance + apps.maintenance_mode.** The
      maintenance primitives (PR-A #??? / PR-B / PR-C cluster).
      Adds a 10th edge-rule kind (`kind=maintenance`) that emits
      503 + Retry-After before the wake gate, plus a coarse per-app
      boolean (`apps.maintenance_mode`) that 503s the whole app.
      Plan-gated Free-and-above (no IsPaidOnly change). The hot-
      path placement is the cost-control slot (D4): runs BEFORE
      auth, BEFORE wake, AFTER `Backend.Lookup`. Cache invalidation
      uses a new `app_changed` pg_notify channel so the gatewayd
      apps LRU (no TTL) flushes on flip. TTL on maintenance rules
      is deferred (D20.8); dashboard chip deferred (D20.9, ADR-092).
    - **D20.8 — Maintenance TTL.** Auto-disable maintenance rules
      at a customer-set timestamp (`edge_rules.expires_at
      timestamptz` + a pg_cron sweeper + a new audit event). Out
      of scope for v1; customers who want timed maintenance today
      can flip the rule on/off via cron + an apid PATCH. New ADR
      needed.
    - **D20.9 — Dashboard chip for maintenance_mode.** Counter
      `gateway_app_maintenance_total{plan}` lands in PR-B; no
      Grafana chip ships. Owner: ADR-092 (observability catalog)
      follow-on, mirroring D20.4.
    - **D21 — Audit payload widening (PR-B residual, 2026-08-12).**
      Three round-trips folded into the CORS-residual PR (PR-B
      pre-maintenance):
      1. **`result` field** — every emit site that knows an outcome
         can stamp `result: "success"|"error[:code=...]"` via
         `pkg/auditutil.WithResult` (new package, single 5-line
         helper). `pkg/audit.Auditor.Emit` stays unchanged; the
         twin method `EmitResult` is the entry point that takes
         the result. `cmd/gatewayd-internal/audit.go::gatewaydAuditor`
         mirrors the contract. The 25 inline emit sites stay
         unchanged today — follow-on PRs migrate them call-site by
         call-site.
      2. **`client_ip` field (XFF audit widening)** —
         `applyEdgeRuleIP` audit rows at sites 1522 (deny CIDR),
         1549 (implicit deny), and 1564 (allow match) now include
         `client_ip`. Sites 1477 (cross-account, before the XFF
         read) and 1502 (forged-XFF, returns nil) keep their
         current payload. The IP flows through
         `clientIPFromTrustedXFF`'s defense-in-depth guard, so the
         audit row carries a trustworthy IP only.
      3. **`client_ip` PII note** — IP is RFC 7239 §6.1 PII;
         operators are responsible for access control on the
         audit-events table. Masking (last-octet truncation for
         v4, prefix truncation for v6) is a separate ADR and is
         out of scope for this PR. Runbook
         `docs/runbooks/FaasAuditRetentionExhaustion.md`
         documents the access-control expectation.

## Implementation surface

| Layer | File | Change |
|-------|------|--------|
| ADR | `docs/adr/091-edge-rules.md` | This file. NEW. |
| Cite-bug cleanup | `*.go` + `*.md` (all `*, "ADR-089"` / `, ADR-089` referring to edge rules) | REPLACE → "ADR-091". (Other ADR-089 siblings — secret-rotation etc. — keep their numbers untouched.) |
| Migration | — | None. (`00192_edge_rules.sql` already exists with all 7 kinds in the CHECK.) |
| DTO | `pkg/api/dto.go::EdgeRuleJWTAction.Validate` | ADD: reject `HS*` algs; reject RFC1918/loopback/link-local JWKS URL prefixes. |
| DTO | `pkg/api/dto.go::EdgeRuleCORSAction.Validate` | ADD: reject `AllowOrigins:["*"]` + `AllowCredentials:true`. |
| Limits | `pkg/api/limits.go` | None (CORS Free; JWT/IP already in `LimitsFor(...).EdgeRules{JWT,IP}Allowed`). |
| State types | `pkg/state/types.go` | None (PR 1 shipped all 7 kinds + actions). |
| State pgstore | `pkg/state/pgstore.go` | None (same SQL covers all 7 kinds). |
| New package | `pkg/edgejwks/cache.go` | NEW. LRU-wrapped `jwk.NewCache`. `Get | Register | Reset`. |
| New package | `pkg/edgejwks/verifier.go` | NEW. `Verify(ctx, rawToken, rule) (*Claims, error)` with 60s skew. |
| Gateway | `pkg/gateway/edge_rules.go` | ADD: `EdgeRule{CORS,JWT,IP}Resolved` types; `HostEntry` widens to 7 slices; `EdgeRuleMatcher` widens with `Match{CORS,JWT,IP}`; 3 `PickFirst*Match` helpers; `EdgeRuleCache.Get{CORS,JWT,IP}`; `noOpEdgeRuleMatcher` widens. |
| Gateway | `pkg/gateway/handler.go::applyEdgeRule{CORS,JWT,IP}` | NEW. ≤50 lines each (extracted helpers, matching PR 4 discipline). |
| Gateway | `pkg/gateway/handler.go::ServeHTTP` injection sites | CORS between route-substitute and redirect; JWT/IP after headers before `enforceRequireAuthn`. |
| Gateway | `pkg/gateway/handler.go::clientIPFromTrustedXFF` | NEW. Helper for `kind=ip`. |
| Gateway | `pkg/gateway/metrics.go::ObserveEdgeRuleMatch` | Pre-instantiation widens to 7 kinds × 5 outcomes. |
| Gateway | `pkg/gateway/internal_proxy.go` | None — already writes the trusted single-hop XFF (PR #749). |
| Config | `cmd/gatewayd-public/main.go` | None (RemoteAddr not rewritten; XFF is the source). |
| Compile | `cmd/gatewayd-internal/edge_rules.go::compile{CORS,JWT,IP}Rules` | NEW. Mirror of `compile{Route,Rewrite,Redirect,Headers}Rules`. |
| Compile | `cmd/gatewayd-internal/edge_rules.go::loadHost` | WIDEN to recompile all 7 kinds in one SQL pass. |
| Match | `cmd/gatewayd-internal/edge_rules.go::Match{CORS,JWT,IP}` | NEW. Cache.Get → store miss → compile → Put → PickFirst. |
| Wrapper | `cmd/gatewayd-internal/edge_rules_jwks.go` | NEW. Constructs the real `pkg/edgejwks.Cache` from `jwk.NewCache`. |
| Wire | `cmd/gatewayd-internal/run.go::run` | WIDEN to pass `edgejwks.Cache` into `gatewaydEdgeRules`. |
| Compile-time guard | `cmd/gatewayd-internal/edge_rules.go` | `var _ gateway.EdgeRuleMatcher = (*gatewaydEdgeRules)(nil)` (already exists at `:471`). |
| CLI | `cmd/gregale/commands_edge_rules.go::edgeRuleJWTAlgVocab` | SHRINK to RS/ES 256/384/512 (lockstep with `pkg/api/dto.go::edgeRuleJWTAllowedAlgs`). |
| Tests | `pkg/gateway/edge_rules_test.go` | +200 LOC. PickFirst tests, HostEntry round-trip, interface widening. |
| Tests | `pkg/gateway/handler_test.go` | +350 LOC. CORS preflight/non-preflight, JWT passthrough/failure, IP allow/deny/wins, ordering, malformed-CIDR. |
| Tests | `pkg/gateway/metrics_test.go` | +20 LOC. Pre-instantiation label coverage. |
| Tests | `pkg/edgejwks/cache_test.go` | +100 LOC. Get/Put/LRU-evict/Reset/concurrent. |
| Tests | `pkg/edgejwks/verifier_test.go` | +150 LOC. Valid/expired/wrong-iss/wrong-aud/wrong-alg/required-claims/bad-sig/clock-skew/rotation. |
| Tests | `cmd/gatewayd-internal/edge_rules_test.go` | +250 LOC. compile* + Match* (cache-miss-hits-store) + shared-cache. |
| Tests | `cmd/apid/handlers_edge_rules_test.go` | +60 LOC. HS\* reject, CORS `*`+cred reject, JWKS localhost/private reject, RS256 still works. |
| Tests | `cmd/sdk-coverage/main.go` | Verify auto-derivation still covers 7 kinds (no change required). |
| `go.mod` | — | PROMOTE `github.com/go-jose/go-jose/v4` from indirect to direct. |

## Verification

### Local

```bash
go test -race ./pkg/gateway/... ./pkg/edgejwks/... ./cmd/gatewayd-internal/... ./cmd/apid/... -count=1 -timeout 5m

# Lint
make lint
go fmt -l . | wc -l    # 0

# Full repo
go test ./... -count=1 -timeout 10m

# Cite-bug sweep gate
grep -rEn 'ADR-089' --include='*.go' --include='*.md' . \
  | grep -v 'docs/adr/089-' \
  | grep -vE 'commands_(builds|build_status|secrets_rotate|2)\.(go)' \
  | grep -vE 'server\.go|cmd/gregale/commands2' \
  | grep -vE 'docs/runbooks/standby-write-redirect\.md' \
  | grep -vE 'tests/property/write_redirect_test\.go' \
  | grep -vE 'package rekey|pkg/rekey|pkg/secretbox/kid\.go|pkg/api/secrets\.go' \
  | grep -vE 'cmd/apid/handlers_secrets_rotate(_test)?\.go' \
  | grep -vE 'migrations/00191_app_secrets_kid_test\.go' \
  | grep -vE 'pkg/api/(errors|client_method_sweep_test|client|limits(_test)?)\.go' \
  | grep -vE 'cmd/sdk-coverage/main\.go' \
  | grep -vE 'cmd/e2e/standby_write_redirect_e2e_test\.go' \
  | grep -vE 'pkg/state/(memstore|pgstore_edge_rules_test|pgstore|store)\.go' \
  | wc -l    # expect: ≤18 (the non-edge-rules siblings that legitimately cite ADR-089 secret-rotation etc.)
```

The cite-bug sweep is intentionally surgical — a blanket `s/ADR-089/ADR-091/g`
would mis-cite the secret-rotation / standby-write-redirect / build-status-endpoint
ADRs. Only edge-rules references flip.

### Manual smoke against a real `gatewayd-internal`

```bash
# 1. kind=cors preflight
FAAS_API=http://localhost:8080 FAAS_TOKEN=fp_live_xxx gregale edge-rules create \
  --app demo --kind cors \
  --match-host foo.example.com \
  --cors-allow-origin https://app.example.com \
  --cors-allow-method GET --cors-allow-method POST \
  --cors-allow-header Authorization \
  --cors-allow-header Content-Type
curl -i -X OPTIONS \
  -H "Origin: https://app.example.com" \
  -H "Access-Control-Request-Method: POST" \
  http://localhost:8080/api/foo
# → 204 + Access-Control-Allow-* headers (preflight short-circuit)

# 2. kind=ip deny
FAAS_API=http://localhost:8080 FAAS_TOKEN=fp_live_xxx gregale edge-rules create \
  --app demo --kind ip \
  --match-host foo.example.com \
  --ip-deny 192.0.2.0/24
curl -H "X-Forwarded-For: 192.0.2.42" \
  http://localhost:8080/api/foo
# → 403 (server-side IP deny fires via the trusted single-hop XFF)

# 3. kind=jwt
FAAS_API=http://localhost:8080 FAAS_TOKEN=fp_live_xxx gregale edge-rules create \
  --app demo --kind jwt \
  --match-host foo.example.com \
  --jwt-issuer https://idp.example.com/ \
  --jwt-jwks-url https://idp.example.com/.well-known/jwks.json \
  --jwt-algorithm RS256 \
  --jwt-audience https://api.example.com
curl -i -H "Authorization: Bearer $TOKEN" \
  http://localhost:8080/api/foo
# → 200 (if token verifies)
curl -i http://localhost:8080/api/foo
# → 401 (no token → edge_rule.jwt_missing)

# Metric surfaces from boot (no first-scrape gap)
curl localhost:9090/metrics | grep gateway_edge_rule_match_total
# expect labels:
#   kind=route|rewrite|redirect|headers|cors|ip|jwt
#   × outcome=match|miss|blocked (+ failed,missing for jwt)

# Live invalidation (wholesale — same as PR 3/4)
psql -c "select pg_notify('edge_rule_changed', '{\"op\":\"updated\"}')"
```

### CI

```bash
# 24 checks (parallel) — all green
gh pr checks <PR-NUMBER> --watch
```

### Pre-merge checklist

- [ ] `gatewayd-public` populates `r.RemoteAddr` with the **client's** IP
  (Caddy → public → internal), and `pkg/gateway/internal_proxy.go:286-288`
  re-adds that as the single trusted XFF entry. (`internal_proxy_test.go:149`
  pins `len == 1`.)
- [ ] JWKS URL network-position validation at apid-Validate (D10).
- [ ] HS\* algorithms dropped from BOTH `pkg/api/dto.go::edgeRuleJWTAllowedAlgs`
  AND `cmd/gregale/commands_edge_rules.go::edgeRuleJWTAlgVocab` (D11). Both
  vocabs must match exactly.
- [ ] CORS `*`+credentials footgun guarded at apid-Validate (D12).
- [ ] Wholesale-RESET property test pins all 7 kinds in
  `pkg/gateway/edge_rules_test.go::TestEdgeRuleReset_WholesaleAcrossAllSevenKinds`
  + the cmd-side forward test in `cmd/gatewayd-internal/edge_rules_test.go`
  (D17).
- [ ] Per-kind e2e files under `cmd/e2e/edge_rules_<kind>_e2e_test.go` cover
  rewrite/redirect/headers/cors/jwt/ip with status-only happy + negative paths
  on bitmask `e2etest.APID | e2etest.Gatewayd` (D18).
- [ ] Spec §4.1.2 backfilled with the canonical hot-path ordering table; all
  six forward-cites (pkg/gateway/handler.go:954,:2277,:3126,
  pkg/gateway/edge_rules.go:101, ADR-091 lines 6 and 64) point to §4.1.2,
  not §13.4 (D19).
- [ ] `grep -nE 'ADR-089' --include='*.go' --include='*.md'` returns ≤18 hits
  post-cite-bug sweep (legitimate secret-rotation siblings stay).
- [ ] PR 4 review-fix lessons applied — every `h.observe(...)` passes
  `string(app.Plan)`; every `r.URL.Path` concat handles the leading-`/` edge.
- [ ] `go-jose/v4` promoted from indirect to direct in `go.mod`.
- [ ] `pkg/edgejwks` does NOT leak `go-jose` types outside the package
  boundary (cmd-side sees `edgejwks.Cache` interface, not `*jwk.Cache`).
- [ ] `HostEntry.Put` no-op guard widened to 7 slices.
- [ ] Compile-time interface check at `cmd/gatewayd-internal/edge_rules.go`
  still passes.
- [ ] No new migrations (schema unchanged from PR 1).
- [ ] `make lint` introduces zero new issues.
- [ ] `codeql go` clean.

## Rollback

Revert the 6 commits in reverse. `pkg/edgejwks` package is purely additive (no
schema, no shared type changes outside the new package). `pkg/api/dto.go` keeps
the `https://` prefix check from PR 1; reverting PR 5 only drops the network-
position guard and HS\* restriction (a slight loss, but the seed `https://`
check still applies). `cmd/gatewayd-internal/edge_rules.go` widens back to
4-kind compile + match — PRs 3/4 keep their `kind∈{route,rewrite,redirect,
headers}` surface intact.

The cite-bug sweep is a **docs change only** — a partial revert would leave
some edge-rules code comments citing "ADR-089". A clean revert restores the
broken citations; the next ADR to land in 092-099 should re-do the sweep.

## Cross-cutting notes

- **HS\* migration ticket (out of PR 5 scope):** a one-time SQL audit
  `select id, app_id, action->>'algorithms' from edge_rules where action->>'algorithms' like '%HS%'`
  should run on production before PR 5 lands. If any rows return, they need a
  manual data-migration to a different algorithm (no automatic conversion is
  possible — HS\* without a shared secret is structurally broken). The PR
  description flags this as a deployment-prep ticket.
- **CORS `*`+credentials migration ticket:** the same audit logic applies
  (`action->>'allow_origins' = '["*"]' and action->>'allow_credentials' = 'true'`).
  No rows are expected in the seed environment; production audit closes the
  loop before PR 5 ships.
- **CORS `*`+credentials footgun history:** the legacy gateway monolith had a
  similar stamper that emitted `*` indiscriminately and ship-blocked
  credentials-rich apps. The current Tier A7 split puts the stamper behind
  `pkg/gateway/handler.go::applyEdgeRuleCORS`, where the validated-input
  posture mirrors the §17 G3 lesson.
- **No resource-scope change:** edge rules remain **per-app**, not per-org.
  The org-scope resolution (ADR-190 PR-4) doesn't extend them.

---

## D24 — kind=limit (ADR-091 D24, 2026-08-11)

### Decision

Add a **ninth** edge-rule kind, **`limit`**, whose action is a single
`max_body_bytes` integer (with an optional `max_body_bytes_streaming`
companion). The primitive is the standalone body-size gate: a customer
who only wants per-route body-size protection ("POST /upload ≤ 5 MB,
POST /users ≤ 1 MB, POST /webhooks ≤ 2 MB") declares this kind without
shipping a JSON Schema. Hot-path placement is §4.1.2.8c — after
`applyEdgeRuleIP` (so rejected-on-IP traffic never costs a body read)
and before the global `MaxBytesReader` at `handler.go:2789` (so the
per-rule cap is the OUTER reader on the in-limit path; the global
reader layers inside as the backstop for requests that don't match
any limit rule).

### Field-by-field

| Field | Type | Required | Bounds | Behaviour |
|---|---|---|---|---|
| `max_body_bytes` | int | yes | `(0, MaxRequestBodyBytes]` (1…26214400) | Buffered-path cap. `0` rejected at create-time as 422 (a standalone limit rule with no cap is a silent no-op, worst shape for a security feature — use `kind=validate` if you need a cap alongside a JSON Schema). |
| `max_body_bytes_streaming` | int | no | `[0, MaxEdgeRuleLimitBodyBytesStreaming]` (0…104857600); when >0 must be ≥ `max_body_bytes` | Streaming opt-in cap. `0` = no streaming carve-out (the buffered `max_body_bytes` is the cap on both paths). The wire contract bans streaming-tighter-than-buffered: it would 413 every streaming request for a body already accepted as buffered. |

### Why §4.1.2.8c (between IP and the global reader)

The placement is load-bearing for the **Content-Length fast path**.
A `Content-Length: 30 MiB` header against a 5 MiB rule, at this
slot, denies with 413 + RFC 7807 + `edge_rule.limit_rejected` audit
+ `match_total{kind="limit",outcome="blocked"}` metric, AND the
fake backend's `Pick` is never called — the test
`TestApplyEdgeRuleLimit_ContentLengthFastPath_DenyBeforeBackendPick`
is the load-bearing regression pin (`pickCalls.Load() == 0`).

If the applier ran AFTER the global `MaxBytesReader`, a bare
`MaxBytesReader` would still trip on the proxy leg's first read,
but at the cost of having the 30 MB body in memory first — a
regression from "never buffer an oversize body" to "buffer and
recover". The §4.1.2.8c placement guarantees the fast path fires
BEFORE the global reader wraps `r.Body`.

### Cross-account posture (ADR-091 D5)

Mirrors `applyEdgeRuleValidate`'s posture at `handler.go:1688–1706`:
a rule owned by a different account is a defense-in-depth no-op —
the handler writes no response, returns `false`, emits
`edge_rule.limit_blocked` audit, observes `match="blocked"` +
`apply="success"` (so the §12 dashboard chip doesn't falsely flag
the cross-account rule as a wire error). A regression that let
cross-account rules enforce would let any customer 413 another
customer's traffic; the test
`TestApplyEdgeRuleLimit_CrossAccount_DefenceInDepthNoOp` pins
this exactly (`pickCalls.Load() == 1` confirms the proxy leg
was reached).

### Relationship to kind=validate's `max_body_bytes`

Kept, NOT deprecated. The semantic split is:

- `kind=validate.max_body_bytes` = "cap the body I'm about to
  schema-check". The applier at `handler.go:1746–1749` installs
  the cap as an INNER reader around the buffered body, with the
  global reader at `handler.go:2789` as the OUTER backstop. A
  customer who wants both validation and a per-route cap uses
  this knob.
- `kind=limit.max_body_bytes` = standalone gate. The applier
  installs the cap as an INNER reader around the body, with the
  global reader at `handler.go:2789` as the OUTER backstop. A
  customer who wants only the cap uses this kind.

The two paths share the same underlying primitive (per-rule
`MaxBytesReader`) but surface different control surfaces:
validate couples the cap with schema checking, limit couples
the cap with nothing else. A customer who declared a
`kind=validate.max_body_bytes` rule today can migrate to
`kind=limit` in a follow-up PR (no schema is required for the
limit path, but they'd need to drop the schema from the validate
path or keep both rules side-by-side).

### Streaming carve-out — runtime

The rule carries `max_body_bytes_streaming` (≤ 100 MiB per
`pkg/api.RawStreamMaxRequestBytes`, ADR-080 raw-bridge parity).
The DTO + apid-Validate + cmd-side compile clamps are all in
place. **Runtime enforcement ships via `streamingFor(h, r, app)`
at `pkg/gateway/handler.go`**, a 4-conjunct helper lifted from
the proxy-leg inline computation at `handler.go:3193–3195`:

```go
return h.streamingEnabled && app.StreamingEnabled &&
    !isAcceptJSON(r.Header.Get("Accept")) &&
    !isUpgradeRequest(r)
```

The §4.1.2.13 call site (`applyEdgeRuleLimit` at handler.go)
computes the bool once and passes it as a parameter. The
applier picks the cap:

- streaming && `rule.MaxBodyBytesStreaming > 0` → streaming cap
- else → buffered cap

Per-cap-kind clamp: buffered cap clamps to
`api.MaxRequestBodyBytes` (25 MiB); streaming cap clamps to
`api.RawStreamMaxRequestBytes` (100 MiB). A single clamp to
`api.MaxRequestBodyBytes` would silently regress streaming
allowances, so the clamp is split. The DTO's
`max_body_bytes_streaming ≥ max_body_bytes` invariant
(`pkg/api/dto.go`) is trusted at runtime — a direct-DB row that
violates it passes the compile and falls back to the buffered
cap at runtime via the `MaxBodyBytesStreaming == 0` branch
(safe degradation, never widens).

The 413 detail message suffixes the cap kind — `"rule … caps
body at … bytes (buffered cap)"` or `"… (streaming cap)"` — so
a customer reading the problem+json body can bisect which cap
fired without consulting logs. The audit payload (`edge_rule.
limit_rejected` + `edge_rule.limit_matched`) carries an
additive `cap_kind` field with the same value, threaded into
the structured-log aggregator's existing sink. The cap kind is
additive — consumers ignore unknown fields by default — and
removing it is non-breaking.

Why the DTO invariant is trusted: apid-Validate enforces it at
rule-create time, and the apid-compile gate (the migration
test) pins the rule of `s ≥ b` on every apply. Direct-DB
writes are a known escape hatch (the e2e helper `seedEdgeRule
Direct` exercises this) and the runtime's safe-degradation
fallback is the documented contract for those paths.

### Test coverage

| Test | Pins |
|---|---|
| `TestPickFirstLimitMatch_PriorityOrdering` | First-match-wins; priority ASC, methods filter, path-glob filter — same contract as every other kind. |
| `TestPickFirstLimitMatch_PathGlob` | `/api/*` glob excludes `/healthz`; matches `/api/users`. |
| `TestPickFirstLimitMatch_MethodsFilter` | POST-only rule excludes GET at priority 0. |
| `TestEdgeRuleReset_WholesaleAcrossAllNineKinds` | Reset() drops all 9 kinds (route/rewrite/redirect/headers/cors/jwt/ip/validate/limit) — a regression that bound only 8 kinds trips this test. |
| `TestEdgeRuleLimitAction_Validate_HappyPath` | In-range action passes. |
| `TestEdgeRuleLimitAction_Validate_Rejects` | 0 / negative / over-cap / negative-streaming / over-streaming-cap / streaming-tighter-than-buffered → 422 with substring-pinned Detail. |
| `TestEdgeRuleLimitAction_Validate_Accepts` | Boundary-at-cap, streaming-equal-to-buffered, streaming-looser-than-buffered all pass. |
| `TestEdgeRuleLimitAction_Validate_NilReceiver` | Nil pointer → 422 (no panic on Go's reflect-dispatch). |
| `TestApplyEdgeRuleLimit_ContentLengthFastPath_DenyBeforeBackendPick` | The load-bearing assertion: 30 MiB CL + 5 MiB rule → 413 + `pickCalls == 0`. |
| `TestApplyEdgeRuleLimit_InLimit_InstallsMaxBytesReader` | In-limit body → `pickCalls == 1` + match/match + apply/success. |
| `TestApplyEdgeRuleLimit_CrossAccount_DefenceInDepthNoOp` | Cross-tenant rule → `pickCalls == 1` + match/blocked + apply/success (defense-in-depth). |
| `TestApplyEdgeRuleLimit_NilMatcher_PassThrough` | `h.edgeRules == nil` → no panic, falls through. |
| `TestApplyEdgeRuleLimit_CapClamp_DefenceInDepth` | Direct-DB row with `MaxBodyBytes = 1 GiB` (bypassed apid-Validate) → 30 MiB CL still trips 413. |
| `TestApplyEdgeRuleLimit_StreamingCap_StreamingRequest_OverCap_413` | Streaming posture (Accept=event-stream, app + h streaming on) + 110 MiB CL on 5 MiB buffered / 100 MiB streaming cap → 413 via STREAMING cap, detail suffixes `(streaming cap)`, `pickCalls == 0`. |
| `TestApplyEdgeRuleLimit_StreamingCap_StreamingCapZero_FallsBackToBuffered` | Streaming posture + streaming cap == 0 (customer didn't set it) → falls back to buffered 5 MiB, 30 MiB CL trips 413 with `(buffered cap)` suffix. |
| `TestApplyEdgeRuleLimit_StreamingCap_BufferedRequest_StreamingFieldIgnored` | Buffered posture (Accept=application/json) with a rule carrying both caps → buffered cap fires; detail MUST NOT mention `streaming cap`. |
| `TestApplyEdgeRuleLimit_StreamingCap_StreamingRequest_ContentLengthOverStreamingCap_413` | Streaming posture + 12 MiB CL on 1 MiB buffered / 10 MiB streaming → 413 via the streaming cap (the buffered cap is irrelevant — the streaming cap is the binding one). |
| `TestApplyEdgeRuleLimit_StreamingCap_AuditEventCarriesCapKind` | Two sub-cases (streaming + buffered) pin the audit payload's new `cap_kind` field: `edge_rule.limit_rejected` carries `cap_kind: "streaming"` or `"buffered"` per the request's path. |
| `TestApplyEdgeRuleLimit_StreamingFor_FourConjuncts` | Six-row truth table pinning the 4-conjunct detection formula (any future conjunct addition requires a new row). |
| `TestApplyEdgeRuleLimit_StreamingCapClamp_DefenceInDepth` | Streaming posture + rule with `MaxBodyBytes = 1 GiB` / `MaxBodyBytesStreaming = 2 GiB` → streaming cap clamps to `api.RawStreamMaxRequestBytes` (100 MiB); 120 MiB CL trips 413. |
| `TestMigrations_00219_EdgeRulesKindLimit` | 00219 migration applies cleanly; CHECK constraint carries 9-value vocab; `kind='limit'` round-trips; `kind='limit_typo'` → 23514; replay safety. |

### Rejected alternatives

- **Reuse `kind=validate`'s `max_body_bytes` knob instead of
  shipping a separate kind.** Rejected — a customer who wants
  ONLY a cap shouldn't be forced to ship a JSON Schema
  (`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}`
  to satisfy the validate kind's required `schema` field).
  Coupling a security primitive to a schema declaration is
  user-hostile.
- **Per-deployment `max_body_bytes` column on `apps` /
  `deployments`.** Rejected — CLAUDE.md pins apid as the only
  writer to customer-intent tables; adding a column there
  would touch customer-intent migrations and conflict with the
  edge-rule feature surface (per-app, per-deployment scope is
  already covered by edge rules).
- **Plan-level matrix entry for body size.** Rejected — no
  precedent in `pkg/api.Limits`; the four plan rows
  (Free/Hobby/Pro/Scale) already share the same
  `MaxRequestBodyBytes = 25 MiB` baseline.
- **Replace the global 25 MiB reader with a per-rule lookup.**
  Rejected — the global reader is the BACKSTOP for requests
  that don't match any rule. Removing it would create a
  security hole (no cap when no rule matches). The §4.1.2.8c
  layering keeps the global reader as the inner backstop and
  the per-rule reader as the outer tightening.

### Migration slot 00219

The 9-value vocab migration (DROP+ADD CHECK, replay-safe) lands
at `migrations/00219_edge_rules_kind_limit.sql`. Two fences at
`migrations/00217_reserve_slot.sql` (PR #849 ADR-092
app_secrets_scope) and `migrations/00218_reserve_slot.sql`
(preview-environments). The migration test
`migrations/00219_edge_rules_kind_limit_test.go` pins all 9
values present, all 8 pre-existing kinds still accept, and the
`pg_get_constraintdef` substring shape per
`pg-get-constraintdef-shapes.md`.

When PR #845 (kind=geo) lands, its migration must widen the
CHECK to 10 values including both `'geo'` and `'limit'`. The
CHECK-rewrite race is documented in the migration's header
comment; a regression that widens the CHECK without including
both new values trips the migration test.

## Amendment (issue #561 / PR #838, 2026-08-11): observability cluster

PR-A (#837, 2026-08-11) registered and pre-instantiated two Prometheus
counters but intentionally deferred the production emit sites to this
amendment. PR-B wires them and ships the dashboard + runbooks + spec §12
rows that close the observability gap.

1. **Adopted** — wired the production emit sites for the two counters
   PR-A registered at boot:
   - `gateway_edge_rule_apply_total{kind, result}` — sibling emit at
     every existing `ObserveEdgeRuleMatch` site in
     `pkg/gateway/handler.go` (the seven apply helpers: route, rewrite,
     redirect, headers, cors, jwt, ip). `result="success"` on fall-through
     and cross-account blocked (defense-in-depth no-op); `result="error"`
     on a non-2xx wire write. Miss paths (no rule fired) emit nothing —
     no apply path ran, and emitting would inflate the success denominator
     with no-op traffic.
   - `gateway_edge_rule_compile_error_total{kind}` — once per dropped
     rule inside `cmd/gatewayd-internal/edge_rules.go::loadHost`,
     ranging over the seven per-kind err slices. Counter equals the
     number of broken rules (not the number of hosts that had any broken
     rules). Pre-fix redox proof in the PR description.
   - The `rule==nil` JWT miss path emits nothing (PR-A regression test
     `TestApplyEdgeRuleJWT_MissPath_NoNilDeref` pins this). This is the
     load-bearing exception to the "match path always emits" rule.
2. **Dashboard** — `deploy/grafana/edge-rules.json` (UID
   `faas-edge-rules-pr-b`) and a byte-identical mirror at
   `deploy/ansible/roles/grafana/files/edge-rules.json`. Four panels:
   match rate by kind, apply rate by kind + result (green=success,
   red=error), JWT failure rate, compile-error stat (any non-zero
   paints red). Byte-identity enforced by `make grafana-mirror-check`,
   wired into `make test`.
3. **Runbooks** — three new in `docs/runbooks/`:
   - `FaasEdgeRuleApplyHigh.md` (warn) — per-kind `apply_total{result="error"}`
     rate > 1/min sustained 5m; cites `api.EdgeRuleJWTVerifyTimeoutDefault = 5s`
     and `pkg/edgejwks.DefaultFetchTimeout`.
   - `FaasEdgeRuleCompileError.md` (page) — any non-zero compile error;
     operator recovers by fixing the glob in apid or invalidating the
     cache via `db.NotifyEdgeRuleChanged` pg_notify.
   - `FaasEdgeRuleJWTFailures.md` (warn) — single failed-rate timeseries
     plus an audit-grep step that distinguishes timeout (`context
     deadline exceeded` substring in `data.err`) from verifier error
     (other substrings). Calls out the CORS-preflight short-circuit:
     a JWT-failure spike dominated by `OPTIONS` requests is a CORS
     preflight storm, not a JWT problem — filter `method != "OPTIONS"`
     before counting.
4. **Spec §12** — three rows added to the metric table:
   `gateway_edge_rule_apply_total{kind,result}`,
   `gateway_edge_rule_compile_error_total{kind}`,
   `gateway_edge_rule_match_total{kind,outcome}` (the match row was
   previously only in ADR-091 D20.4; PR-B promotes it to §12).
5. **Rejected alternative — JWT outcome label widening.** Considered
   adding `{failed_timeout, failed_signature, failed_aud, failed_iss,
   failed_expired, failed_missing_claim}` to the JWT outcome label set.
   Rejected because: (a) the JWT verifier's internal error taxonomy
   lives in `pkg/edgejwks` and is not a closed set — every verifier
   change would bloat the §12 metric surface and the Prometheus label
   cardinality; (b) the failure-reason breakdown is recoverable from
   the existing `data.err` field in the `edge_rule.jwt_failed` audit
   row, and the runbook documents the audit-grep. Deferred to a
   follow-on ADR if/when the operator workload shows the grep is
   insufficient.
6. **CORS preflight short-circuits IP+JWT gates — pinned.** Per
   `pkg/gateway/handler.go::applyEdgeRuleCORS` ordering comments:
   preflight applies AFTER rewrite (so a rewritten path is matched
   against CORS rules) and AFTER headers (so request-side header ops
   don't shadow the preflight's `Allow-*` headers). CORS preflight
   short-circuits with 204 + `Access-Control-Allow-*` headers; the
   caller MUST return to skip the auth gates. The `FaasEdgeRuleJWTFailures`
   runbook calls this out as the canonical reason to filter
   `method != "OPTIONS"` before counting.
7. **Pre-fix redox proof** — captured in the PR description (counter
   call-site walkthrough for a JWT-failed request; compile-error
   walkthrough for a malformed `match_path` rule).

## CORS improvements (one-PR follow-up)

A single follow-up PR lands six changes that build on the §D1–D19
decisions above. None of them are spec deviations; each is an
ergonomic widening of the surfaces introduced here. The cluster is
intentionally scoped as one PR (per the implementation plan's
`One big PR` decision) because every change references the same
allowlist grammar; splitting risks landing inconsistent validators.

### D20 — Subdomain / port wildcard grammar

`EdgeRuleCORSAction.AllowOrigins` now accepts three wildcard shapes
in addition to literal origins and the bare `*`:

- `https://*.example.com` — subdomain wildcard (`*` is a single
  left-most host label).
- `https://localhost:*` and `https://api.example.com:*` — port
  wildcard (`*` is the complete port).
- A regex constant `api.CorsOriginPattern` enforces the grammar at
  create-time; the gateway hot path runs the same predicates in
  `pkg/gateway/handler.go::matchOrigin` so a rule that bypasses
  apid-Validate still matches what the customer expects (defence in
  depth).

The bare `*`+credentials footgun guard (D12) is unchanged; only the
bare `*` entry trips it. A subdomain-wildcard entry expands to a
concrete origin at request time, so browsers permit credentials for
it.

### D21 — Per-app default CORS

`apps` gains two columns:

- `cors_default_enabled boolean NOT NULL DEFAULT false`
- `cors_default_origins text[]` (nullable; coalesce to `'{}'` on read)

When `cors_default_enabled` is true and no explicit `kind=cors`
edge rule matches a request, the gateway stamps a soft CORS header
set derived from `cors_default_origins`. The default is opt-in (no
silent stamp on legacy rows), uses the same `matchOrigin` matcher
the rule path uses (no parallel implementation), and **skips** the
OPTIONS short-circuit (the customer's backend remains authoritative
for the preflight answer; the gateway only stamps response headers).

The validator on PATCH `/v1/apps/{slug}` requires a non-empty
`cors_default_origins` when `cors_default_enabled` is true —
silently accepting an empty allowlist would leave the customer with
an opt-in flag that stamps nothing. Migration slot: 00223 (with
00224 reserved as a fence per the cross-PR slot pattern).

### D22 — Default-fallback placement in applyEdgeRuleCORS

The fallback runs **inside** `applyEdgeRuleCORS`, immediately after
the existing `MatchCORS` miss path. Pipeline order preserved:
`kind=cors` rule → per-app default fallback → JWT → IP. No audit
emit on the default path (it's a miss with a stamp, not a rule fire).

### D23 — Typed SDK helper

A new `pkg/api.CreateCORSEdgeRule(ctx, slug, opts)` packs the
`EdgeRuleCORSAction` JSON, pins `kind="cors"`, and applies
priority / max-age defaults the dashboard uses. Customers who want
the full edge-rule power (priority, enable/disable, multi-host)
still go through `CreateEdgeRule` directly; the helper is a thin
ergonomic shim, not a parallel wire surface. Node + Python SDKs
pick up the same shape via `make sdk-gen` (they read the kebab
POST from OpenAPI directly).

### D24 — CLI subcommand `gregale cors`

`cmd/gregale/commands_cors.go` adds `cors allow|ls|rm|show` as a
thin shim over the SDK helper. Same dispatch shape as
`gregale edge-rules` (parent / subcommand / suggest-on-typo).
Origins are validated against `api.CorsOriginPattern` locally so a
common typo fails fast without a round-trip.

### D25 — Hygiene bundle

- `MaxAgeSeconds` cap at 86400 (24 h). Browsers ignore larger
  values; the gateway was happily stamping
  `Access-Control-Max-Age: 2147483647` before the cap.
- Case-insensitive comparison on scheme + host in `matchOrigin`
  (RFC 6454 §3). The request `Origin` is lowercased before
  comparison; the echoed allowlist entry carries the lowercased
  form.
- `pkg/api/errors.go::ErrCORSOriginNotAllowed` gains a doc comment
  noting it's exported for apid test fixtures and future per-app
  audit emit; it's still not consumed on the gateway hot path
  (origin rejection stays silent).
- `cmd/e2e/edge_rules_cors_e2e_test.go` gains a `*+credentials`
  reject case so the footgun guard is e2e-covered (it was
  unit-tested in `cmd/apid/handlers_edge_rules_test.go` only).
