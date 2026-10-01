# Shared traffic counters

Implementation: ADR-375. Complete traffic-platform release acceptance remains
pending; local evidence below does not establish deployed latency or HA.

## Selection and inspection

The internal gateway's `[ratelimit] mode` defaults to `central`. It requires
the daemon's Postgres pool and shares app, account and rule counters across
processes. Invalid modes refuse startup. Explicit `local` mode selects private
counters for development or an operator exception and logs that fleet caps
are unavailable. The existing Ansible template already defaults to central.

The retry budget also defaults to the same Postgres pool in central mode.
Existing mutually exclusive Redis URL / credential-file settings select Redis
instead. A configured Redis endpoint must connect at startup. One shared
retry budget object is passed to public and declared service-proxy retry loops.
No additional Redis installation is required for the Postgres default.

Inspect `gateway_rate_limit_shared` and `gateway_retry_budget_shared` (1 means
shared, 0 means local), plus `gateway_retry_budget_backend_info{backend_id}`
for a credential-free endpoint identity. Compare all gateways. The legacy
`gateway_ratelimit_degraded_total` now counts refused shared rate admission;
it does not mean a private allowance was granted. Retry backend operations
report observe/admit results, including errors. Endpoint identity and selected
mode are diagnostic evidence; runtime failure checks are still required.

## Accounting

| Traffic | App/account rate allowance | Aggregate retry allowance |
| --- | --- | --- |
| Authenticated ordinary public request | Account then app, once before cache lookup | One original only if the retry policy and replay safety checks permit retry |
| Cache hit | Charged once | No original or retry |
| Additional public attempt | No additional rate debit | One atomic retry spend |
| Failed authentication | No app/account debit | No retry observation |
| Earlier fixed edge response, health or CORS preflight | Existing path-specific controls | No retry observation |
| Declared internal service call | Binding authorization and existing service controls; public app/account limiter is not invoked | Eligible original and retry spends share the target app's public retry counter |
| Streaming / raw Upgrade | Initial public rate admission if reached | Public HTTP retry loop excludes streaming; raw Upgrade is not replayed |
| Background job / arbitrary guest socket | Outside public request counter | Outside these managed HTTP retry loops |

Rule and dimensional throttles retain their configured scope and charge at
their existing enforcement stage. Sequential account/app/rule checks are not
one transaction: a previous scope can be charged when a later scope rejects.
Ambiguous store-operation timeouts are not refunded. These are traffic safety
counters, not billing records. Retry windows count eligible logical originals
and never count additional attempts in the denominator.

## Failure and recovery

Rate exhaustion returns 429. An unavailable or timed-out central counter
returns 503 with `rate_limit_unavailable` and no unverified guest admission.
An already-expired request deadline takes precedence and returns its 504.
Restarting a gateway cannot reset a durable bucket. Fractional refill time is
preserved; statements reaching a row out of timestamp order cannot subtract
tokens through negative elapsed refill.

Retry store errors refuse replay while the original can run once. Failure to
observe that original also refuses its replay, even when an earlier app
window has unused allowance. The database owns the ten-second expiry. An
expired window needs a fresh original; process replacement cannot grant a
fresh minimum. Expired rows are pruned in batches and app deletion cascades.

## Local evidence

Separate processes against real Postgres admitted exactly two retries for
twenty originals at ten percent, and exactly four tokens from a shared burst.
A replacement process admitted no fresh retry or burst. Store unavailability,
recovery, expiry and row-lock timeouts were checked. Two real HTTP gateway
processes with a guest HTTP fixture admitted four of sixteen concurrent
requests in one shared app burst; measured local p50 was 5.9 ms and p95 was
8.0 ms. Both gateways refused store outages; replacement/recovery preserved
debt, and blocked admission timed out without forwarding.

The daemon fleet fixture now runs two independent OS processes through
`LoadConfig` and `runWithDeps`, leaving the counter mode unset to exercise the
production central default. App limits and cache/retry rules are read from
Postgres. Actual serving observations identify both gateways and their shared
backend. The production node client and forwarding RPC connect to a fixture
VM endpoint over a Unix socket and then one real HTTP origin. This checks
shared app admission, app/account charging on cache hits, bounded row-lock
waiting, counter outages, recovery and process replacement. Additional retry
attempts do not create additional app/account debits. Retry minimums remain
spent across replicas and replacement within the same ten-second database
window. Application 500 responses run once; retry-store failures refuse replay.

The fixture exposed a public retry dispatch bug: the picker chose a sibling,
but the forwarding RPC retained the first instance's transport header. Public
attempts now clone and restamp target identity in headers and correlation
metadata. The regression checks actual RPC instance IDs and guest executions,
plus removal of stale provenance and preservation of earlier attempt headers.

Logical completion now follows the last target whose forwarder ran. Successful
activity, instance request/egress records, debugger telemetry and completion
logs/spans name that target. Selecting a sibling that is refused before dispatch
does not change the completion owner. The original request's wake cause, cold
outcome and wake node remain tied to admission; a cached sibling wake ID is not
a new wake. Affinity cookies are set per dispatch, and buffered retries preserve
original platform cookies with the final guest's cookies. Discarded attempts
cannot contribute cookies. Rate and logical request charges remain once per
request; retry attempts retain their separate accounting.

`TestTrafficFleetDaemonRetryCompletionTelemetry` observes the production
publisher's five-second cycle from both daemon processes. It compares emitted
completion identities with RPC targets and actual guest request headers, and
checks two logical records for three forwarding attempts. The receiver captures
the emitted records as a fixture; this does not establish production telemetry
persistence or shutdown delivery.

Managed attempts also own a cloned request and header map, including bodyless
replays. Verified account and selected endpoint identity replace prior-hop
values in guest headers and correlation context. The logical managed-call span
names the last endpoint actually forwarded; a policy-refused sibling cannot
replace it. Empty provenance clears earlier values. HTTP and raw forwarding
RPCs publish that current context, replacing reserved correlation metadata
while retaining unrelated transport metadata. Repeated publication remains
bounded. This envelope is diagnostic data, not an authorization credential.

`TestTrafficFleetDaemonManagedRetryIdentityAndReplacement` enables the configured
private service listener in two actual daemon processes. A namespace source
address fixture feeds the production fresh Postgres caller lookup; stored
declared bindings and reliability policy drive the call. Four eligible originals
spend one shared retry across replicas and replacement in one live database
window. Five RPC attempts produce two guest executions, and RPC correlation
matches the served guest identity. A spoofed caller is refused before forwarding.
These internal calls leave public app/account rate counters untouched. Listener
binds and guest source addresses are local fixtures; this is not native network,
DNS, VM, node-admission or cross-host acceptance.

Run `go test ./cmd/gatewayd-internal -run '^TestTrafficFleetDaemon'` with an
unmigrated disposable `DATABASE_URL` and `FAAS_PGTEST_TEMPLATE_DATABASE=1`.
The test helper itself is guarded when invoked without its subprocess spec.
Placement, VM-side forwarding and telemetry receivers are local fixtures;
outer daemon discovery, cross-host mTLS, node admission and native VM execution
are separate acceptance work.

These are local fixture measurements. Native VM paths, complete policy and
traffic coverage, production load, staging recovery and rollout are separate
acceptance requirements tracked in `docs/traffic_platform_implementation.md`.
