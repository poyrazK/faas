# ADR: Concurrent Go acknowledgment recovery-health monitoring

- Status: Accepted
- Date: 2026-10-17

## Context

Node and Python expose retained callback recovery health through bounded caches
and a shared Prometheus/Grafana pack. Go needs the same evidence and missing-data
contract while supporting independent polling and concurrent net/http scrapes.
Scrapes must not perform storage or recovery work or retain protected inputs.

## Decision

Add a pointer-only owner-scoped Go cache with mutex-protected fixed state: one
selected metadata sample, database timestamp high-water mark, freshness budget
and poller ownership flag. Validate full health scope, coverage, bounded counts
and relationships before copying scalar metadata and a UTC timestamp. Each
snapshot owns its sample copy; formatting and HTTP writes occur after unlocking.
Construction starts no goroutine, query or listener. Zero/nil caches are invalid;
their zero snapshots fail exposition rather than manufacturing healthy counts.

Require complete, fresh, strictly advancing evidence at recording and freshness
at snapshot time. Fresh incomplete reads also advance the high-water mark, which
survives failed reads and poller shutdown. Malformed/failed reads clear samples.
Repeated/regressing/future/stale observations cannot become eligible through time
alone. Compare wall/database age and initial age plus local monotonic elapsed;
wall regression also invalidates eligibility. Saturate duration arithmetic for
eligibility while retaining calendar-range raw age via second arithmetic. Format
fractional Unix timestamps exactly without UnixNano overflow, including dates
before the epoch. Keep metadata ages finite and bounded counts within scan limits.

Reuse the thirteen Node/Python gauge suffixes, HELP strings, order, prefix and
text-0.0.4 content type. Generate no labels or explicit sample timestamps. Emit
three status/configuration gauges always and ten sample gauges only with a valid
cached sample. Incomplete/stale raw samples remain ineligible; missing data is
absent rather than zero. Validate all formatter inputs before returning any text.

Provide an explicit sequential Go poller using the existing bounded health read.
Require one matching owner/poller per cache and validated copied options before
SQL. Read synchronously under a cooperative context deadline; check expiry and
actual elapsed time even after nominal success. Never race or abandon an active
query. Read failures clear evidence and polling continues. Caller cancellation
joins the active database call, clears sample data and returns the context error.
The application still bounds driver connect/statement/cleanup calls and keeps
the database pool available until polling returns. Existing watch/SQL behavior
remains unchanged. Manual feeds are an alternative sole source, not another poller.

Add an unbound cache-only net/http handler with strict GET /metrics routing,
no-store headers and generic errors. Provide a buildable host with explicit
polling, an independently started loopback server on 9468 and HTTP timeouts.
The application supervises both loops and owns authorization/network protection,
bounded shutdown, joins and pool lifetime. Reuse the shared five recordings,
six alerts and dashboard queries/identities/thresholds. Add a distinct Go scrape
config and static CI lint entry; dashboard version 3 changes only language
metadata. Extend shared setup/runbook with Go control APIs and uncertainty fences.

## Consequences

Each target covers one retained owner scope. Duplicate observers cannot be
summed into a global backlog; authorized owner mapping stays in host configuration,
outside labels. Metrics do not translate protected jobs/checkpoints between SDKs
and perform no callback, recovery, business replay or budget mutation. No platform
schema/migration, dependency or deployment changes are required.

Prometheus backlog notifications can resolve when evidence disappears, which
does not prove recovery. Inspect companion unavailable/down/absence conditions
and authoritative rows; use the checkpointed SDK watch for incident identity
across unknown evidence/restarts. A budget grant or newer staging can lower
parked counts without confirming a callback. Driver cancellation is cooperative,
not a hard bound on an unresponsive driver; supervision belongs to the host.

## Validation

The standalone Go SDK and buildable examples compile; vet passes. Static AST
inspection matches all thirteen suffixes, HELP strings and emission order across
Go/Node/Python, verifies selected sample fields and checks that construction/
scraping starts no SQL or helper goroutine. Existing health SQL, watches,
Node/Python exporters, shared rules and existing scrape configs are unchanged.
Prometheus 2.54.1 accepts the new Go config and all eleven shared rules. Static
YAML/JSON comparison preserves timing, opt-in labels, existing targets and all
dashboard identities/queries/thresholds; CI only adds Go config syntax lint.
Alert metadata, ADR uniqueness, documentation routes, local links and whitespace
checks pass. No tests/assertions were added/changed and no local tests or SDK
helper smoke calls ran. Hosted behavioral qualification remains pending for
concurrent recording/scraping, clocks, stale/repeated evidence, joined database
cancellation/shutdown, live HTTP scrapes, alert transitions and Grafana rendering.
