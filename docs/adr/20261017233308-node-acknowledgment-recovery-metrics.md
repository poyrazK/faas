# ADR: Node cached recovery-health Prometheus monitoring

- Status: Accepted
- Date: 2026-10-17

## Context

All SDKs can watch parked/due/stalled callback intents, including retained
checkpoint notifications. The shared Prometheus/Grafana pack observes preparation
lifecycle events, not retained callback backlog. Operators need a separate
read-only view of exhausted claims and unavailable recovery evidence, with no SQL
on the scrape path and no protected or high-cardinality identities in metrics.

## Decision

Add a Node collector bound to one normalized account/app and one cached metadata
sample. Feed completed health reads explicitly. Copy selected own primitive
properties, reject malformed counts/scope/accessors and clear cached sample data
on failure. Retain a database timestamp high-water mark across failures. Fresh
incomplete observations also advance it; repeated/regressing/future observations
never establish eligibility. Retain no callback, token, payload, invocation or raw
error. Snapshot memory is constant and detached/frozen.

Recompute sample age at each snapshot from wall-clock database age and age at
recording plus monotonic elapsed time, using the more conservative result.
Wall-clock regression invalidates eligibility too. Eligibility requires complete,
fresh, advancing evidence at recording and continued freshness at snapshot time.
No sample ripens into eligibility simply through elapsed time. Before a read or
after failure, omit count/coverage/age gauges rather than inventing zeroes.

Expose a pure strict text-0.0.4 formatter with thirteen fixed gauge families and
no SDK-generated labels or explicit sample timestamps. Allow bounded ASCII
prefix customization. The application example explicitly creates an unbound
scrape server and starts one sequential owner-scoped poller. Query deadlines are
cooperative and checked after actual driver cleanup; no active query is abandoned
or overlapped. Stopping polling clears evidence. Applications own listener/network
protection, driver budgets and joined shutdown. No import starts a listener,
query, timer, callback, recovery pass or notification.

Add separate recovery assets beside the existing preparation pack: five recording
rules, six warning alerts, a nine-panel dashboard and operator runbook. Require a
live scrape, successful complete advancing recording and host/Prometheus clock
freshness before backlog recordings exist. Check database observation time and
exported conservative age against the configured bound, so stale cached text
cannot remain healthy through repeated scrapes. Missing/incomplete/stale evidence
gets its own alert, alongside scrape-down and expected-target absence alerts.
Dashboard counts remain missing when unknown; do not fill/interpolate them to zero.

## Consequences

Each target describes one bounded owner scope; duplicate observers cannot be
summed into a global backlog. Keep authorized scope mapping in trusted host
configuration. Prefix, target label and recording aliases form an explicit
monitoring contract; Python exporter parity follows separately. CI adds rule/
config/JSON/metadata static coverage without changing existing gates or fixtures.
No platform metric, schema, migration, deployment or notification route changes.

Prometheus notification resolution can follow evidence loss and does not prove
recovery. Use companion evidence/down/absence alerts and authoritative rows;
use the SDK checkpoint watch for retained incident identity across unknown
observations/restarts. Guarded budget resume requires explicit authorization,
exact timestamp/count/grant fences and durable original command identity. A
grant or supersession can reduce parked count without confirming the callback.
No alert automatically mutates work or authorizes business replay.

## Validation

Node generation produced no API drift; TypeScript build and example syntax passed.
Five archive helper/root JS/declaration/README files match build/source bytes and
all seven public exports are present. AST inspection confirmed thirteen fixed
gauge families and metadata-only snapshot fields. Existing health SQL, recovery/
checkpoint protocols, schemas and preparation code/assets remain byte-identical.
Prometheus 2.54.1 accepted all eleven recovery rules, the scrape configuration and
sixteen embedded dashboard/variable expressions without time-series evaluation.
JSON/YAML structure, missing-value/selector guards, CI wiring, shell syntax,
alert/runbook metadata, ADR uniqueness, documentation routes, twenty-three added
local links/anchors and whitespace checks passed. Existing hosted fixture execution
is unchanged. No tests/assertions were added or changed and no local tests or SDK
helper smoke calls ran. Hosted qualification remains pending
for clock changes, stale/repeated/incomplete evidence, driver cancellation/live
scraping, alert transitions, notification routing and Grafana rendering.
