# ADR: Observe actual Go callback recovery-worker activity

- Status: Accepted
- Date: 2026-10-17

## Context

Recovery-health metrics describe retained database backlog. A live health reader
and scrape endpoint can coexist with an unscheduled or hung callback recovery
worker. Operators need actual pass activity and bounded outcome metadata without
letting observation perform recovery or refresh liveness artificially.

## Decision

Add a separate concurrency-safe fixed-memory Go cache for one trusted owner scope
and one nonoverlapping pass stream. Construction begins the first-pass idle
expectation, default one minute; actual pass start and joined return are its only
subsequent activity events. A monitored wrapper validates scope/options/ownership
before recovery I/O, calls the existing finite recovery pass and preserves its
result/errors. Record completion only after admitted work joins. Pre-canceled
calls do not refresh activity. A panic leaves the pass tracked in flight.

Use an idle-gap expectation and a separate active-pass duration expectation,
default five minutes. Neither is a deadline, scheduling instruction or authority
to abandon/overlap work. Keep original driver/callback bounds and conditional
claim, budget and unknown-COMMIT protocols. No public manual heartbeat API allows
unrelated reads/scrapes to refresh activity. No SQL, pass, listener, timer or
background goroutine starts from construction or scraping.

Compare wall age with local monotonic elapsed and retain the greater age.
Detected clock regression invalidates evidence for the cache's lifetime; explicit
replacement follows clock review. Copy timestamps to UTC and return independently
owned metadata snapshots/results. Retain only latest start and latest joined
return/progress, including while a subsequent pass is active. Validate counts,
stop reasons and admission/outcome algebra, bounded to 10000 admissions. Invalid
returned progress omits outcome evidence while preserving known activity timing.
Never retain owner identity in snapshots, callback inputs, clients, contexts,
arbitrary error causes or unbounded history. Cache configuration retains only
the trusted scope and expectation bounds.

Expose 23 fixed Prometheus gauges under a separate worker prefix. Always expose
timing/expectation markers; omit unobserved start/return/outcomes. Latest-pass
counts are gauges rather than cumulative counters or retained database backlog.
Keep success/empty, unconfirmed, parking, lost claims, failure, interruption and
unknown COMMIT meanings distinct. A pass return does not confirm all callbacks
or business success. Formatters emit no labels or explicit sample timestamps.

Add a combined matching-scope cache-only HTTP handler using fixed default health
and worker prefixes. Preserve the existing thirteen health gauges and health-only
handler. A buildable combined host retains the existing 9468 endpoint but leaves
health polling, HTTP serving and monitored pass scheduling separately explicit.
Applications own supervision, trusted scope mapping, network protection, driver/
HTTP deadlines and joined shutdown. Inspect original progress/error before
scheduling further work; metrics are no recovery command or durable receipt.

Add a separate worker opt-in scrape config, three recordings and three warning
alerts in a new shared-rule group. Existing eleven health rules and existing
scrape configurations stay unchanged. Worker eligibility uses reachable opted-in
timing/clock/bounds independently of database-read evidence. Recorded age takes
the greater of host age and Prometheus time since actual activity, preventing
frozen successfully scraped text from extending liveness. Compare idle/active
bounds separately. Alert on overdue activity, missing/invalid worker evidence and
latest failed/unknown-COMMIT admissions, each for two minutes; interruption alone
does not trigger the pass-failure alert. Preserve existing down/absence alerts.

Dashboard version 4 preserves the original nine panels, selectors and identity,
and adds eight worker panels with explicit Unknown/gap handling. Document host
ownership, pass-gauge semantics, clock/overlap diagnosis and uncertainty handling.
CI only adds static syntax validation of the opt-in config; no gate is weakened.

## Consequences

Node/Python health-only exporters remain compatible; their worker-cache parity
can follow separately. One target describes one local pass stream/scope; do not
double-scrape or sum duplicate observers into global activity/backlog. Restart or
new results can clear prior local history. Alert resolution after changed/missing
evidence does not establish callback recovery; inspect companions and authoritative
retained work. Expected target subsets still require external inventory policy.

No platform migration, schema, generated API or dependency changes are needed.
No automatic pass, callback, retry-budget grant, business replay or fence takeover
is introduced. Live concurrency, clocks, cancellation, scraping and notification
transitions require hosted behavioral qualification before release.

## Validation

Go SDK/examples build and vet pass. Static AST inspection checks the fixed gauge
inventory, metadata-only cache/snapshot fields and wrapper-only recovery entry;
construction/scraping starts no SQL or helper goroutine. Existing recovery pass,
health exporter/watch/SQL and schemas remain unchanged. Prometheus 2.54.1 accepts
the opt-in config, seventeen shared rules and all thirty-two dashboard expressions.
Static YAML/JSON comparisons preserve original rules/panels/configs and isolate
worker opt-in from database evidence. Alert metadata, ADR uniqueness, docs routes,
local links, formatting and whitespace checks pass. No tests/assertions were
added or changed and no local tests or SDK helper smoke calls ran. Runtime
qualification remains pending; static validation does not supply that evidence.
