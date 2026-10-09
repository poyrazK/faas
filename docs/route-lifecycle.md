# Review route lifecycle and surface drift

`gregale routes lifecycle review` reconciles a deployment's captured OpenAPI
contract, retained route usage, saved route requirements and a source-impact
report. It surfaces route-surface discrepancies alongside endpoints that merit
a human retirement review, while keeping missing or mismatched evidence
inconclusive.

First create a source-impact report for the deployed commit and the app's source
root, then review it against the immutable deployment:

```sh
gregale routes impact api --base BASE_COMMIT --head DEPLOYED_COMMIT \
  --path services/api --out route-impact.json --json
gregale routes lifecycle review api --deployment DEPLOYMENT_UUID --since 14d \
  --source-impact route-impact.json --out lifecycle-review.json --json
```

The source report must name the same app and align with the deployment's
repository, source root and full commit SHA. Working-tree reports and reports
for another commit are unbound. If source analysis is incomplete or unavailable,
routes without traffic remain `unknown`. This is declared provenance alignment;
it does not prove the analyzed source bytes are identical to the deployment
archive.

The report compares each captured method/path with the selected deployment's
route-usage window and bound current source snapshot. It uses exact path matches
first, then parameter-shape matches such as `/users/{userId}` to `/users/{id}`
only when the match is unique across the relevant surfaces. Ambiguous or
unsupported matches remain unknown. Each route lists the surfaces where it was
found: `captured_contract`, `current_source` and/or `observed_traffic`. Source
matches include the handler and source/registration file locations when
available.

`source_only` means static analysis found a source registration absent from the
captured OpenAPI contract. It is a useful contract-drift lead, but static
discovery alone does not prove the route is reachable in the deployed runtime.
If traffic also matches that route, the row is
`source_and_observed_outside_contract`. `observed_only` means telemetry found
traffic with no confident contract or source match. Contract routes marked
`removed` or `not_found` are counted as missing from the bound source snapshot;
the row still records the captured contract surface.

Routes with saved individual or group requirements are marked `protected`, as
are routes found in the bound current source snapshot. A `review_candidate` has
no matching observation in the returned window, no current static-source or
saved-requirement reference, and complete untruncated evidence. It is a prompt
to inspect client usage, documentation and compatibility before deciding what
to do; it is never an instruction to delete an endpoint.

Coverage is `observed_only`. Sampling, missing instrumentation, dropped events,
disabled recording and plan retention can all hide requests. The route list is
capped at 200, and plans retain different telemetry windows; clamped or
truncated results make absent routes inconclusive. The captured OpenAPI document
is also a contract inventory rather than a complete runtime route registry. If
observed traffic contains a route missing from that document, the inventory is
marked incomplete and quiet routes are withheld as candidates. A source-only
finding is surfaced for investigation; it does not by itself establish a live
runtime route.

JSON statuses include `active`, `review_candidate`, `protected`, `unknown`,
`observed_only`, `source_only` and `source_and_observed_outside_contract`. The
summary counts source routes outside the contract, contract routes missing from
the source snapshot, and observed routes outside the contract. The top-level
outcome is `incomplete` when a required evidence surface is missing, a route
match is ambiguous or the evidence is bounded; otherwise it is
`review_required` when a candidate or route-surface discrepancy is present and
`clear` when no review finding remains. Use `--fail-on-incomplete` in automation
to return a nonzero exit when review coverage is inconclusive. Candidate
findings themselves do not cause a failing exit, so CI can archive the report
for a human to review.

Reports include requests, distinct tenant and consumer counts, and last-seen
time for observed routes. Tenant and consumer populations can overlap; do not
sum them. Customer IDs are omitted. The command is read-only and `--out` writes
the JSON review to a new local file.

## Measure customer migration before retiring a route

Use a saved customer-impact roster and an explicit old-to-successor mapping to
track the affected cohort against selected immutable deployments. Save distinct
observation windows, then compare them locally:

```sh
gregale preview customers track --roster customer-roster.json \
  --mapping route-successors.json --deployment api=DEPLOYMENT_UUID \
  --since 30d --out migration-window-1.json --json
# Repeat within 30 days using the same roster, mapping and deployment ID.
gregale preview customers progress \
  --snapshot migration-window-1.json --snapshot migration-window-2.json \
  --grace-period 30d --min-windows 2 --max-staleness 72h \
  --format markdown --fail-on-not-ready
```

Progress rejects snapshots whose customer-route cohort, explicit successor
mapping or immutable deployment IDs changed. It needs distinct complete windows
with continuous coverage; keep the 30-day windows overlapping (for example, run
the tracker weekly). Clamped, truncated, stale or missing evidence cannot
qualify. Readiness requires zero **aggregate** requests to the old route, so
traffic from anonymous callers or customers outside the saved cohort still
blocks review. Customer rows separately show successor observations and
`no_current_evidence`; silence is never labeled as a verified migration.

`owner_review_ready` is a checkpoint for the route owner to review compatibility,
support commitments and product behavior. It is not proof that removal is safe
and never deletes or changes a route. Older tracker files without aggregate
old-route counts remain readable but contribute no zero-traffic evidence. New
snapshots with aggregate counts must cover the full grace period before they can
establish readiness.

## Suggest successors for disappearing routes

Generate a draft mapping and a report from captured deployment contracts:

```sh
gregale routes migration suggest \
  --from-deployment checkout=OLD_DEPLOYMENT_UUID \
  --to-deployment checkout=NEW_DEPLOYMENT_UUID \
  --since 14d --out route-successors.json --report-out suggestions.json
```

By default, source routes are baseline operations missing from the same app's
successor contract. Repeat the deployment flags to compare multiple apps,
including moves between apps. Use `--sources sources.json` to select specific
source routes, including routes still present in the successor. This is the
version 1 mapping format with empty `successors` arrays for every source.

Suggestions require the same HTTP method and rank version-prefix and parameter
shape matches, shared literal path segments, matching `operationId` values,
and declared request, response and security compatibility. Identical generic
schemas alone do not identify a replacement. Each candidate includes its score,
reasons, contract findings and observed traffic. Scores are heuristic rankings,
not probabilities. The draft selects a strong best candidate only when its
score is at least 70, its lead exceeds 10, and the comparison finds no supported
breaks. Ambiguous, weak, incompatible or incomplete matches keep empty
`successors` arrays for owner review. Traffic breaks display-order ties but
cannot resolve ambiguity. `--limit` controls displayed candidates (default 3,
maximum 10); ambiguity is checked before this limit is applied.

The report records capture hashes, sources and timestamps, and usage windows
for each specified deployment. Source traffic refers to the baseline deployment;
it is not app-wide current traffic. Missing or truncated observations remain
unknown rather than proving inactivity. Customer UUIDs are omitted by default.
Use `--customer-details` to include the bounded observed source consumer/tenant
UUIDs, request counts and last observations; the report marks incomplete details
and omitted requests. Use `--without-traffic` for a contract-only run.

The command reads APIs and writes new local files. It supports up to 20 apps and
2,000 captured operations per side, with at most 10,000 source/candidate pairs;
narrow the source file or deployments when this bound is exceeded. Review the
draft with the route owner, then pass the edited mapping to the contract review
and customer tracking commands below. Existing files are never replaced.

## Assess migration readiness with refreshed contracts

After reviewing the suggested mapping with the route owner and saving at least
two customer tracker windows, generate one readiness report:

```sh
gregale routes migration readiness \
  --mapping route-successors.json \
  --from-deployment checkout=OLD_DEPLOYMENT_UUID \
  --to-deployment checkout=NEW_DEPLOYMENT_UUID \
  --snapshot migration-week-1.json --snapshot migration-week-2.json \
  --grace-period 30d --min-windows 2 --max-staleness 72h \
  --format markdown --out readiness.json --fail-on-not-ready
```

Repeat deployment flags for every app in the mapping. The command reads the
deployment identities and captured contracts again, compares each mapped
successor, and joins that fresh review with the saved customer windows. It uses
the same readiness rules as `preview customers migration review` below, without
requiring an intermediate contract review file. The tracker windows must match
the successor deployment IDs and the exact source/successor mapping; mismatched
evidence is rejected.

The report includes per-route compatibility findings, telemetry gaps, grace
period evidence, customer next steps, successor observations and old-route
request counts and last observations for each saved window. Missing or invalid
last-observed timestamps remain unknown. Counts in overlapping windows must
not be added. Customer UUIDs from the supplied snapshots appear in the report;
it contains no names, keys or request payloads.

`owner_review_ready` means complete aggregate zero-traffic evidence covers the
grace period and the declared contracts have no supported breaks. Missing,
truncated, stale or uncovered telemetry cannot establish readiness. The report
always sets `owner_review_required: true`: supplying a mapping does not record
or prove owner approval. Review behavioral equivalence and customer evidence
with the owner before removing a route.

Use `--format csv` for the customer action queue, `--json` for automation, or
`--fail-on-incomplete` to fail on evidence gaps. `--fail-on-not-ready` exits 1
unless all mapped routes reach the owner approval checkpoint. These flags are
local CI checks; this report does not enforce a deployment gate or remove routes.
Existing output files are never replaced. Readiness JSON can be compared over
time using `preview customers migration diff`.

## Gate route removal before traffic promotion

Use a staged migration: first deploy both the old route and its successor, then
observe customer cutover on that serving production revision. For the removal
gate, generate readiness with **both** `--from-deployment` and `--to-deployment`
pointing to that same serving revision, and collect all tracker windows against
it. This binds old-route silence to production traffic and the exact captured
contract that still exposes the old route. A quiet preview or removal candidate
cannot establish that production customers have stopped calling the route.

The next candidate removes the old route while retaining its reviewed successor.
Inspect the proposed removal in report-only mode:

```sh
gregale routes migration gate --app api \
  --baseline-deployment SERVING_DEPLOYMENT_UUID \
  --candidate-deployment REMOVAL_CANDIDATE_UUID \
  --readiness readiness.json --mapping route-successors.json \
  --out removal-gate.json
```

Every captured baseline operation absent from the candidate needs a mapping,
complete cutover evidence, and explicit owner approval. The gate compares each
mapped successor against the removal candidate again; an earlier compatible
review cannot hide a missing successor, a new break or an unsupported change.
Cross-app successors remain blocked in this preflight because it does not
refresh their destination contracts. Reports show route-specific and shared
blockers. Missing captures cannot establish that there are no removals.

After the route owner has reviewed behavioral equivalence and customer evidence,
record that owner's explicit local attestation:

```sh
gregale routes migration approve --app api \
  --baseline-deployment SERVING_DEPLOYMENT_UUID \
  --candidate-deployment REMOVAL_CANDIDATE_UUID \
  --readiness readiness.json --mapping route-successors.json \
  --approved-by owner@example.com --out owner-approval.json
```

This command requires the baseline to be live at 100% traffic, refreshes the
captured contracts and recent production route traffic, and refuses to write an
approval while any contract or cutover blocker remains. The attestation binds
the app, baseline and candidate deployment IDs, both contract hashes, and the
SHA-256 of the exact readiness and mapping files. Changing file bytes or either
contract capture requires a new approval. `--approved-by` is an asserted owner
identity in a local file, not a server-authenticated approval. Keep the files
under the same access controls as other trusted CI inputs.

Opt into enforcement when promoting the live candidate:

```sh
gregale traffic promote api --deployment REMOVAL_CANDIDATE_UUID \
  --if-serving SERVING_DEPLOYMENT_UUID --route-removal-mode enforce \
  --route-readiness readiness.json --route-mapping route-successors.json \
  --route-owner-approval owner-approval.json
```

The CLI checks evidence freshness, distinct continuous observation windows,
grace-period coverage, route/customer readiness, approval bindings, and fresh
production traffic before any traffic write. New old-route requests, missing,
truncated or clamped observations, or a returned window narrower or older than
requested block promotion. Reports include the returned production observation
window and route counts. Evidence and approvals must be no older than 72h;
`--route-evidence-max-age` can tighten this bound. The readiness report's own
stricter staleness limit still applies to its observation windows.

`--if-serving` retains the server's atomic serving-deployment guard, including
with `--require-bindings`. JSON promotion receipts include `route_removal_gate`;
blocked enforcement exits 1 with the gate report and sends no promotion write.
`--route-removal-mode report` is advisory and still performs the requested
promotion when the removal gate finds blockers. Without a removal mode,
`traffic promote` keeps its existing behavior. The read-only `migration gate`
command also supports `--mode enforce` for a CI exit check.

`preview report` includes an advisory removal gate by default. Supply
`--route-readiness`, `--route-mapping`, `--route-owner-approval`, and
`--route-removal-mode enforce` to make its exit status depend on that gate.
For an approval of a preview candidate, use `migration approve --candidate-app
<preview-slug>` with its actual deployment ID; a later production candidate
requires its own approval. Existing response/request/security checks still
apply independently.

These flags enforce a CLI preflight. Its evidence reads precede the API write;
a local attestation cannot satisfy the server policy below. Existing platform
contract gates retain their checks for unrelated breaks and incomplete comparisons.

## Enforce production removal on the server

The production guard covers `prod`, the legacy `default` scope (including an
empty stored scope), and `production`. Other environment scopes are excluded.

Configure a policy while the staged production deployment still exposes both
old and successor routes. The default mode is `report`; unconfigured apps retain
existing behavior. Policy writes require an account admin credential, completed
MFA and the current revision. An owner/admin organization session is also
accepted. A deploy token or customer tenant key cannot grant this approval.

```sh
gregale routes migration policy --app api
# Revision 0 means no saved policy yet.
gregale routes migration policy --app api --mode report \
  --expected-revision 0 --grace-period 720h --max-approval-age 1h
```

First configuration adopts the single production deployment serving 100% of
traffic. Split production traffic must first converge to one baseline. The
server observation clock starts at adoption. A full cutover adopts the new
baseline; changing or deleting its captured contract restarts the clock.
Identical capture retries and changing policy mode preserve this clock. Approval
requires the configured grace period to have elapsed since adoption and capture,
and that period must fit within the account's retained server telemetry.
Go durations are used here (`720h` for 30 days). The allowed grace period is
1h–2160h; approval age is 1m–72h, default 1h.

Prepare the removal candidate at **zero production traffic** so its capture is
available for review. Keep the existing production baseline serving while
collecting the local readiness windows described above. Both deployments must
belong to the same production app. Server approvals currently require exactly
one same-app successor for each removed operation.

After reviewing readiness, opt into server enforcement with the current policy
revision, then obtain a server approval using the returned new revision:

```sh
gregale routes migration policy --app api --mode enforce \
  --expected-revision 1 --grace-period 720h --max-approval-age 1h

gregale routes migration authorize --app api --expected-revision 2 \
  --baseline-deployment SERVING_DEPLOYMENT_UUID \
  --candidate-deployment REMOVAL_CANDIDATE_UUID \
  --readiness readiness.json --mapping route-successors.json \
  --acknowledge-observed-only --out server-approval.json

gregale routes migration server-check --app api \
  --candidate-deployment REMOVAL_CANDIDATE_UUID --fail-on-blocked

gregale traffic promote api --deployment REMOVAL_CANDIDATE_UUID \
  --if-serving SERVING_DEPLOYMENT_UUID
```

When the production API contract gate is enabled, an explicit zero-traffic
candidate may omit old operations so it can be captured and reviewed. Other
breaking changes and incomplete comparisons still block that activation.
This keeps the existing API contract feature flag and its `prod` scope targeting;
the removal policy itself also covers `default` and `production`.
Traffic increases waive only the operation removals covered by a valid server
approval under an **enforced** removal policy. Both captures must project to the
exact compared canonical snapshots. Report mode and local approval files grant
no exception. Historical rollback contract gates remain strict. The read-only contract diff
continues to show the raw changes, including approved operation removals.

The comparison uses the serving contract, or the removal policy's retained
baseline during a canary. A dark candidate's newer snapshot cannot replace that
baseline. The traffic transaction pins and rechecks the approval, policy revision,
capture digests, and snapshot digests; changing mode to report after preflight
also invalidates the exception.

`server-check` prints the policy revision, earliest approval deadline, matching
approval expiry, blockers, and recovery steps. Use global `--json` to obtain
`earliest_approval_at`, `approval_valid_until`, and `next_actions` as structured
fields. A passed check is advisory: every traffic transition checks current
evidence again after acquiring the application and policy locks. Expiry,
policy revisions, changed captures, or renewed old-route observations require
new evidence and approval. Rejected transitions roll back their traffic changes.

`authorize` checks the local readiness report before submitting an authenticated
approval request. The server independently loads its captures, checks every
mapped successor in both staged baseline and candidate, and counts old-route
observations across **all production deployments and identities**, without route
or customer output caps. Uploaded report statuses and local `--approved-by`
values cannot authorize traffic. An API admin can request approval directly
using the endpoints below; that request follows the server contract, grace and
telemetry checks and explicitly acknowledges the limits of observation.

The durable receipt records the authenticated account/key or session identity,
policy revision, baseline/candidate IDs, authoritative capture hashes, canonical
mapping and its digest, server timestamps, observation interval and expiry.
Capture `doc_sha256` metadata identifies the server-owned bytes; local reports
hash their normalized JSON separately. Policy revisions and changed captures
invalidate approvals. Policy history and approvals are stored durably, with
additional audit events for policy changes, approvals and blocked deployment
activation.

Production traffic increases run the shared server evaluator inside the write
transaction. A database guard covers direct traffic APIs, automatic activation,
canary advancement, recovery, rollback and project flows that update deployment
traffic. Zeroing or superseding the prior deployment cannot erase the durable
baseline. Partial canaries keep that baseline until full cutover. Missing or
truncated captures, stale approval, changed policy/hash or renewed old-route
observations return `route_removal_required`; inspect `server-check` for blockers.
First production activation has no prior routes to remove. A dark deployment
can become live at 0%; the gate applies before it receives weighted production
traffic. Explicit revision access is outside this traffic policy.

Quiet telemetry is **observed only**. The removal gate requires unsampled
delivery coverage, but ingestion delay and missing instrumentation still limit
what observations establish. It is not proof that every client has migrated or
that no new request can arrive after a query. Human review of behavioral equivalence and customer readiness remains
required. A removal approval waives only its covered operation deletions in the enabled
API contract gate; binding policies, health checks, unrelated contract breaks,
and historical rollback requirements still apply. MemStore has no telemetry backend,
so it refuses server approvals rather than treating missing telemetry as silence.

### Telemetry coverage before approval

Removal approvals require fresh, continuous, unsampled telemetry coverage from
**every active compute node**, including idle nodes. Gateway publishers send a
private coverage heartbeat at startup and each flush (normally five seconds).
The server records per-app delivery loss, ring overwrites, and backlog, alongside
node collection state, sampling, boot identity, sequence, and source/receipt times. A heartbeat older
than 30 seconds, an ingestion backlog, or a restarted gateway cannot establish
quiet traffic. Rate-limited or rejected telemetry rows count as gaps for their app. A mixed
batch retains other apps' acknowledgements and retries only unacknowledged rows.

The observation window ends at a UTC minute boundary at least two minutes
before approval. Configure the grace period with three minutes of margin inside
your plan's telemetry retention. After an app's dropped or backlogged events, collect a new full healthy grace
period for that app before requesting fresh approval. Gateway restarts, sampling,
disabled collection, and heartbeat outages require recovery for every app. Existing receipts cannot bypass those gaps. Removal
checks and traffic transitions use the same coverage evaluator; transactions
hold node membership, node coverage, and the target app's gap rows while making
the decision.

`server-check` reports these blockers and their recovery steps:

| Blocker | Recovery |
| --- | --- |
| `telemetry_coverage_missing` | Upgrade or restore coverage reporting on all active gateways. |
| `telemetry_coverage_stale` | Restore gateway-to-apid heartbeat delivery. |
| `telemetry_disabled` | Enable collection on gateways and apid. |
| `telemetry_sampled` | Restore unsampled collection. |
| `telemetry_ingestion_pending` | Resolve the app's backlog, unattributed pending rows, or source-time lag. |
| `telemetry_window_incomplete` | Collect an uninterrupted healthy grace period after recovery. |

Delivery gaps and backlog are now app-specific: another app's rate limiting,
overwrites, or rejected rows do not invalidate your approval. Availability still
requires fresh healthy heartbeats from every active node. Unknown losses and a
full gateway loss journal remain shared blockers. Lost heartbeat receipts retain
the loss journal and can conservatively extend recovery deadlines when retried.
Older heartbeat formats retain fleet-wide loss accounting; gateways without
coverage reporting block approval. Upgrade gateways and apid to obtain app
isolation. Retired nodes must be deactivated through normal node administration. Coverage still describes
recorded completed requests; it does not prove that every client has migrated
or that an in-flight request cannot finish after the check.

API endpoints:

| Endpoint | Purpose |
| --- | --- |
| `GET /v1/apps/{slug}/route-removal/policy` | Read policy and baseline |
| `PUT /v1/apps/{slug}/route-removal/policy` | Save with expected revision |
| `POST /v1/apps/{slug}/route-removal/approvals` | Record authenticated approval |
| `GET /v1/apps/{slug}/route-removal/check?deployment_id=UUID` | Explain current blockers and capture hashes |

The server deployment must include migration
`20261009061628464_route_removal_policy.sql` before enabling these endpoints.

## Check a successor contract before customer cutover

Before asking customers to move, compare each explicit old-to-successor mapping
against the deployment-bound OpenAPI contracts for the selected baseline and
successor deployments:

```sh
gregale routes migration review --mapping route-successors.json \
  --from-deployment checkout=OLD_DEPLOYMENT_UUID \
  --to-deployment checkout=NEW_DEPLOYMENT_UUID \
  --format markdown --out migration-review.json
```

The review compares HTTP method, path parameters (paired by position), declared
request constraints, response schemas and security requirements. A response
field that was required in the baseline and becomes optional in the successor
is reported as breaking. Changed `oneOf` / `anyOf` response schemas and
unsupported facets such as `enum`, `format`, constraints, and composition are
reported as `unknown`, since their compatibility is not classified. A changed
parameter position also remains `unknown`. The review reports `breaking`
differences, security changes that need review, and `unknown` when either
captured contract or a supported comparison is incomplete. When a mapping offers multiple
successors, the report shows the result for each option. It records each
contract's source, capture time and SHA-256. Deployment IDs are immutable, but
an owner can replace the contract capture attached to a deployment; retain the
contract SHA-256 in the report when you need to identify the exact bytes reviewed.
An older captured union without an opaque schema fingerprint is reported as
`union_baseline_incomplete`. A legacy non-union response schema without its
fingerprint is reported as `schema_baseline_incomplete`, since the old
snapshot cannot prove unsupported facets were absent. Recapture the contract
with the gate disabled before relying on later comparisons.

`no_supported_breaks` means the supported declaration checks found no
confirmed break or changed unsupported response-schema facet. It does not prove that runtime
behavior, data transformations or undocumented client assumptions are
equivalent. Use this as migration evidence alongside the customer progress
report and have the route owner review the result. The command only reads
deployment contracts and writes a local report.

## Join contract review with customer cutover evidence

After saving the contract review and at least two distinct customer tracker
snapshots, combine them into one route-by-route and customer-by-customer report:

```sh
gregale preview customers migration review \
  --contract-review migration-review.json \
  --snapshot migration-week-1.json --snapshot migration-week-2.json \
  --grace-period 30d --min-windows 2 --max-staleness 72h \
  --format markdown --fail-on-not-ready
```

The command verifies that tracked routes have the same explicit successor
mapping as the contract review and that telemetry uses the reviewed successor
deployment IDs. A route is `owner_review_ready` only when its mapping has
`no_supported_breaks` and aggregate old-route traffic has remained at zero for
the complete grace period. Breaking, unknown, and review-required contract
results block that status. Customer rows show observed old-route and successor
windows plus an advisory next step. Each customer's `observed_successors` lists
the exact successor endpoint seen, its contract result, and the request count
and last-seen time for each saved snapshot. Snapshot windows may overlap, so
counts are kept per snapshot and must not be added together. Summary categories
for historical successor observations can overlap when a customer used multiple
successor endpoints. The separate latest-successor fields use in-window
last-observed timestamps across snapshots, and their contract counts can overlap
when multiple endpoints tie. Missing timestamps in a window that could extend
past the latest known event make the latest endpoint ambiguous and cause the
next step to request verification. Successor
traffic or silence alone never proves that an individual customer has completed
migration. The report is read-only and does not authorize or perform route
removal.

For support or engineering handoffs, use `--format csv` to export one action
row per customer-route link. The queue ranks breaking successor usage first,
then observed old-route reuse and contract review, followed by remaining
migration and evidence follow-up. Each row includes the reason for its priority,
the next step, route identity, latest successor details, and event time where
available. Priorities are triage guidance derived from the supplied evidence;
they do not establish customer intent or authorize route removal. CSV is written
to stdout, while `--out` continues to save the full JSON review.

Compare two saved reviews to see which customer-route links regressed, improved,
or lost evidence:

```sh
gregale preview customers migration diff \
  --before migration-last-week.json --after migration-today.json \
  --format markdown --fail-on-regression
```

The diff requires the same customer grouping and cohort, route-to-successor
mapping, and baseline and successor deployment IDs. It also requires the `after`
review to be generated no earlier than the `before` review. Cohort or mapping
drift makes the comparison invalid; missing customer rows are never counted as
resolved. Breaking successor use and renewed old-route activity are regressions.
Evidence becoming ambiguous, incomplete or unavailable is reported separately
as `evidence_degraded`; changes in the customer's latest successor endpoint are
listed as `evidence_updated`, and improvements are listed independently. The
`--fail-on-regression` gate fails only for confirmed regressions, not for evidence
degradation.

The cutover review also flags `old_route_reobserved_after_successor` when the
latest snapshot with available old-route evidence positively observes a
customer's old-route request after their latest observed successor request. Both
last-observed times must fall inside their telemetry windows; overlapping
windows are ordered by those timestamps, not by snapshot creation time. Missing
or ambiguous timestamps produce no signal. Review the exact endpoints and
contract status with the customer and route owner; the signal does not infer
intent or authorize removal.

## Publish deprecation and sunset guidance

Include operation metadata in the application OpenAPI document captured for
each deployment (or the owner-uploaded deployment contract):

```yaml
paths:
  /v1/items/{id}:
    get:
      deprecated: true
      x-gregale-deprecated-at: "2026-10-01T00:00:00Z"
      x-gregale-sunset-at: "2026-12-01T00:00:00Z"
      x-gregale-successor: "https://api.example.com/v2/items"
      responses:
        "200":
          description: OK
```

The app hostname publishes `Deprecation: @1790812800`,
`Sunset: Tue, 01 Dec 2026 00:00:00 GMT`, and a successor-version `Link`
for matching live requests when their selected deployment capture contains
these fields. App imports retain the same metadata for explicit import review,
but do not replace a deployment capture used for live headers.
Dates require whole seconds; sunset cannot precede deprecation and requires an
HTTPS successor URL. A plain OpenAPI `deprecated: true` remains valid without
additional metadata but does not generate a dated header.

Matching uses the original public path and method before rewrites, preferring
static paths over parameter templates. HEAD inherits GET metadata only when
HEAD is not explicitly declared. Explicit route allowlists govern admission;
lifecycle metadata comes from the selected deployment capture. Live responses on app hostnames and pinned URLs use the final selected
deployment capture, including canaries and fallback targets. CORS preflight and earlier edge answers omit lifecycle headers. Cached bodies
with recorded serving-deployment identity resolve current lifecycle guidance. Unavailable metadata does not prevent otherwise permitted requests.

Sunset dates are advisory. Use migration review and server removal approval to
verify compatibility and remaining callers before removing an operation.

## Review upcoming sunsets and remaining callers

```sh
gregale routes sunsets api --deployment BASELINE_UUID --since 14d --within 720h
```

This read-only report defaults to the selected deployment's captured lifecycle
annotations and retained caller telemetry. Use `--source manual_import` to
review the mutable application import instead. It lists
invalid metadata, overdue sunsets, upcoming sunsets, undated deprecations,
and later scheduled sunsets, in that order. Each operation includes observed
requests, latest activity, consumer/tenant observations, anonymous requests,
and unresolved identities. Missing dates and successor links remain visible.

Supply an explicit mapping and successor deployments to include fresh contract
comparison and observed successor activity:

```sh
gregale routes sunsets api --deployment BASELINE_UUID \
  --mapping route-successors.json --to-deployment api=SUCCESSOR_UUID \
  --since 14d --within 720h --out sunset-report.json --json
```

All source mappings must refer to the selected app. Successor telemetry uses
the same half-open window as baseline telemetry. For same-app successors,
`old_route_callers_also_observed` counts matching consumer/tenant pairs observed
on both routes. Cross-app identities are not compared; the report marks
`caller_identity_comparable: false`. A successor URL is published guidance;
it does not substitute for an explicit operation mapping or compatibility check.

`--fail-on-overdue` exits 1 when a sunset date has elapsed.
`--fail-on-incomplete` exits 1 for missing lifecycle fields, missing or
inconclusive successor review, breaking or review-required contracts, clamped
or truncated telemetry, or anonymous/unresolved caller activity. Reports are
still emitted before these exits. `--out` never replaces an existing file.

Coverage remains observed-only: zero requests cannot prove that every caller
has migrated. The report records `metadata_source`, `capture_source`, `capture_sha256` and
`captured_at` for deployment metadata, or `imported_sha256` for app imports.
Import metadata is mutable and distinct from deployment capture metadata.
Successor observations do not establish migration order. Use migration readiness
and server removal approval for production cutover decisions.

## Compare sunset reports over time

Save successive reports for the same app and baseline deployment, then compare
locally without account access:

```sh
gregale routes sunsets diff --before sunset-before.json --after sunset-after.json \
  --max-staleness 24h --out sunset-diff.json --fail-on-regression --json
```

The diff reports newly observed old-route callers, later requests from existing
callers, resumed old-route activity, and traffic observed after sunset. Continued
activity from an existing caller is an evidence update; it does not prove that
they migrated and returned. New old-route callers, resumed activity, and
post-sunset requests are observed regression signals.

Equal-length, non-overlapping windows with unchanged import, contract captures,
mappings and usable telemetry can show reduced old-route activity or increased
compatible-successor usage as advisory progress. Overlapping or different-length
windows suppress aggregate progress claims. Successor activity does not prove
migration order or completed adoption. The full before/after rows retain counts,
caller identities, latest activity and successor overlap for owner review.

Changed lifecycle dates, successor URLs or mappings, contract captures/status,
route additions and operations missing from the current report require review.
An absent operation or caller is never treated as successfully migrated. Missing
metadata, anonymous/unresolved identities and clamped/truncated telemetry remain
incomplete evidence, even when a positive regression observation is present.

Both generation time and window end must advance. Reports from different apps
or baseline deployments, duplicate operations/callers, invalid dates, negative
counts and observations outside their window are rejected. The old report may
be historical, but its window must have been fresh when generated. The new
report and window end must be within `--max-staleness` of the comparison time.

`--fail-on-regression` exits 1 for observed regressions.
`--fail-on-incomplete` also exits 1 for changed, stale or incomplete evidence and
windows that cannot support aggregate comparisons. JSON and `--out` reports are
emitted before these exits. All results remain advisory; use server removal
checks and approval for production changes.

## Lifecycle headers during rollouts

Live forwarded responses use the final deployment selected after capacity
admission, including pinned URLs, production splits, canaries and fallback
routing. Path/method matching still uses the original public request before
rewrites. Captures are checked against the account, app and deployment; missing,
truncated or invalid captures omit advisory headers without blocking requests.
There is no fallback to an app-wide import for a missing deployment capture.

Deployment captures are cached separately by account/app/deployment. Insert,
update and deletion notifications invalidate the app's deployment entries; a
five-minute TTL handles missed notifications. An invalidation during a read
prevents that read from repopulating the cache. Guest headers cannot overwrite
platform-authored dated Deprecation or Sunset values.

Response-cache entries now retain their actual origin deployment ID separately
from the key's rollout selection. Fresh, stale-while-revalidate, stale-while-waking
and stale-on-error hits resolve current lifecycle metadata from that deployment's
capture, using the original public request before rewrites. Capture replacement
changes guidance without discarding the body or resetting its expiry.

The optional origin ID survives Redis/L2 storage and cross-gateway hydration.
Older entries without it remain readable and omit lifecycle headers; the gateway
never guesses from the current traffic split or key. A normal origin refresh
adds the identity. Missing/invalid captures also omit advisory headers while
serving the cached body. Stored lifecycle dates and successor-version links are
filtered; unrelated headers and links retain normal replay behavior.

Pinned deployment URLs retain their existing cache bypass. Rollout-selected keys
must match the body's recorded deployment, and detached refreshes select that
exact deployment instead of a sibling. A stale fallback inside an origin cache
writer cannot recapture the old body as a new response, change its deployment
identity, or renew its expiry.

## Check lifecycle declarations before rollout

```sh
gregale routes lifecycle declarations api \
  --from-deployment BASELINE_UUID --to-deployment CANDIDATE_UUID \
  --out lifecycle-declarations.json --fail-on-findings --json
```

The advisory review compares exact deployment captures, records their evidence
and document hashes, and flags removal of deprecation or lifecycle fields,
earlier sunset dates, successor changes requiring compatibility review, and
operation removal before its announced sunset. Missing/truncated/unsupported
captures or invalid lifecycle metadata produce an incomplete review. A deprecated
operation removed without an announced sunset requires review. Removing an
operation at or after sunset passes this declaration check but still requires
normal contract and route-removal approval checks.

Use `routes migration review` with an explicit mapping to investigate a changed
successor. A URL is guidance, not proof that an operation is compatible. The
declaration gate accepts a matching server-owned compatibility receipt for
successor-review findings. Saving this advisory report never creates an approval
or bypasses server checks. Other lifecycle findings must be resolved first.

Lifecycle checks run transactionally on **production traffic increases**, using
the existing route gate configuration:

```sh
gregale routes gate get api
gregale routes gate set api --mode enforce --expected-revision CURRENT_REVISION
```

The default `report` mode records lifecycle reasons while allowing traffic.
Production review history is retained in `production_lifecycle_reviews`; canary
advances also include it in their gate decision and traffic audit. `enforce` blocks
lifecycle findings. Canary advances additionally require current satisfied route
requirements evidence. Existing plan entitlement, saved
requirements, ownership and revision checks still apply. The gate compares all
positive serving sibling deployments in the same environment and the retained
removal-policy baseline. Missing baseline or candidate captures therefore block enforced traffic
increases; capture contracts before promoting an older app.

Baseline and candidate captures are locked through the traffic transaction.
Lifecycle results are recomputed, so an earlier passing route-requirements
check cannot hide a changed baseline declaration. Findings are exposed as
`lifecycle_*` reason codes without route paths or payloads in gate audit metadata.

Initial activation, initial canary/split stages, ordinary and service promotions,
manual traffic redistribution and checked rollback require lifecycle review.
Every production traffic increase also passes a database guard, which rejects
missing or stale internal transaction authorization in enforce mode. Dark
activation at 0% stays available for capture and approval. Decreasing traffic
requires review for any sibling that gains traffic.

Canary/service abort and automatic incident rollback can restore their validated
predecessor without lifecycle approval; existing recovery and other policy checks
still apply. Ordinary rollback remains reviewed. A successful traffic review
records whether this internal recovery exception was used.

Successor receipts now include a database configuration binding for workers that
cannot run the API's configured fingerprint callback. Existing receipts without
that binding are invalidated by the migration and need fresh approval. App limits,
manifest, account eligibility and edge-rule changes invalidate that binding.
Changing the operator's canonical host configuration requires a fresh review.


## Approve captured successor changes

Select the intended canary gate mode before preparing the request. Create an
explicit mapping JSON array for every changed successor in the compared pair:

```json
[
  {
    "method": "GET",
    "path": "/old",
    "successor_url": "https://api.gregale.dev/new",
    "successor_method": "GET",
    "successor_path": "/new"
  }
]
```

The URL must exactly match the candidate operation's `x-gregale-successor` and
use the destination app's canonical HTTPS host or an exact verified application-wide
custom domain, with no port, query or fragment. The checker supports production
routing destinations and inline rooted OpenAPI
operations, and declared source and successor responses. External/custom hosts
and other environment successors require future target-binding support.

```sh
gregale routes lifecycle prepare-approval api \
  --from-deployment BASELINE_UUID --to-deployment CANDIDATE_UUID \
  --mappings successor-mappings.json --out lifecycle-approval-request.json
# Review the saved mappings and pinned server evidence before submitting.
gregale routes lifecycle approve api --request lifecycle-approval-request.json --json
gregale routes lifecycle receipt api --id APPROVAL_UUID --json
```

Preparation uses authoritative `doc_sha256` capture metadata, current gate and
removal policy revisions, and the saved route-check configuration hash and intent
revision. It writes a new file and does not approve anything. Approval requires
an account admin credential or owner/organization owner-admin session with
completed MFA. The server computes compatibility from captured contracts;
local compatibility claims and approver fields cannot grant authority.

Receipts expire after one hour. Gate/intent/removal-policy revision or configured
policy changes make a receipt unusable. Any replacement or deletion of either
capture permanently invalidates it, even if identical bytes are later restored.
An expired or stale receipt remains readable; inspect the current gate decision
to see accepted `lifecycle_approval_ids`. Each serving or retained baseline needs
its own matching receipt. A new sibling cannot borrow another baseline's review.

Only changed-successor review findings can be cleared. Earlier sunsets, removed
metadata and premature removals remain blocked, as do unresolved route intent,
structural contract checks and route-removal authorization. A passing declared
comparison is not proof of behavioral equivalence or client migration.


### Cross-app successor approvals

An explicit mapping can identify an operation in another application's capture:

```json
{
  "method": "GET",
  "path": "/old",
  "successor_url": "https://api.example.com/new",
  "successor_method": "GET",
  "successor_path": "/new",
  "successor_app_id": "11111111-1111-4111-8111-111111111111",
  "successor_deployment_id": "22222222-2222-4222-8222-222222222222",
  "successor_contract_sha256": "<authoritative destination doc_sha256>"
}
```

Supply all three destination pins together. The existing `prepare-approval`
command preserves these fields from the mappings file, and the server checks
ownership, the exact capture, routing and compatibility before issuing a receipt.
The destination must belong to the same account and owning organization and be
public. A legacy destination app must have one production deployment serving 100 percent;
a project destination must resolve to one live member of its active production
release graph and have a valid frozen production workload spec;
source-app destinations must use the candidate. Omitting destination pins keeps
the existing candidate workflow and also supports its verified exact custom domains.

Destination captures, hostname bindings, rules, ownership/visibility and traffic
changes invalidate the approval. The production write checks its binding again.
Unverified, environment, wildcard-only, tenant-surface and ambiguous
weighted destinations are unavailable for approval. Potentially applicable routing,
redirect and rewrite rules require further review, even when header conditional.
See [ADR-822](adr/822-verified-lifecycle-successors.md) for scope and bindings.

Project successor approvals bind the active release graph generation and members,
the selected deployment's frozen workload settings, and environment identity.
Publication and rollback invalidate the receipt even if they restore a previous
member. The active graph may retain its live member at zero weighted traffic;
newer weighted deployments cannot replace that default graph destination.
Missing graphs, members or frozen specs fail closed. Mutable desired settings do
not replace captured settings. This review covers default ingress, excluding
explicit retained-release request headers. See [ADR-823](adr/823-release-graph-lifecycle-successors.md).

### Production review history

Read production traffic decisions with:

```sh
gregale routes lifecycle history api --json
gregale routes lifecycle history api --limit 10 --before REVIEW_ID --json
```

The corresponding API is `GET /v1/apps/{slug}/route-lifecycle/history`, with
`limit` and `before` query parameters. Use the response's `next_cursor` for the
next page. Read authorization, application ownership and MFA requirements match
approval receipt reads.

Each review reports `applied` or `blocked`, the deployment and production scope,
review time, recovery flag and gate decision reasons. Capture hashes and graph IDs
reflect the recorded review. Approval summaries show whether the review used a
receipt and its current `valid`, `expired`, `invalidated` or `unavailable` status,
with a reason. Read the approval receipt for complete successor mappings. Current
status does not replace a fresh rollout check.

`evidence_available: false` identifies older reviews whose binding evidence was
not recorded. `truncated: true` identifies omitted metadata beyond the bounded
summary limits. Successful traffic writes and application-level blocked reviews
are retained. Direct SQL rejections and successful evaluations rolled back by
another policy are not retained. See [ADR-824](adr/824-production-lifecycle-review-history.md).
