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
