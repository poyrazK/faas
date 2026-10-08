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
request constraints, response schemas and security requirements. A changed
parameter position remains `unknown`. It reports `breaking` differences,
security changes that need review, and `unknown` when either captured contract
or a supported comparison is incomplete. When a mapping offers multiple
successors, the report shows the result for each option. It records each
contract's source, capture time and SHA-256. Deployment IDs are immutable, but
an owner can replace the contract capture attached to a deployment; retain the
contract SHA-256 in the report when you need to identify the exact bytes reviewed.

`no_supported_breaks` means the supported declaration checks found no breaking
difference; it does not prove that runtime behavior, data transformations or
undocumented client assumptions are equivalent. Use this as migration evidence
alongside the customer progress report and have the route owner review the
result. The command only reads deployment contracts and writes a local report.

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
