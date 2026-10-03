# Route policy plans

Turn [route requirement](route-requirements.md) violations into reviewable
throttle and execution-budget patches:

```sh
gregale routes plan my-api --requirements gregale-routes.yaml
gregale routes plan my-api --requirements gregale-routes.yaml --json
gregale routes plan my-api --requirements gregale-routes.yaml --out route-plan.json
```

The command works on a normal app or a preview app. The server reads app metadata,
app-owned edge rules, and account limits in one consistent snapshot. Planning
does not invoke application routes or write platform configuration.

## Repair from saved requirements

After a monitoring violation, plan directly from the app's saved intent:

```sh
gregale routes plan my-api --saved \
  --deployment 00000000-0000-4000-8000-000000000001 \
  --expected-revision 1 --throttle-burst 20 --out repair.json
gregale routes apply my-api --plan repair.json --confirm
gregale routes results my-api \
  --deployment 00000000-0000-4000-8000-000000000001 --wait --json
```

`--saved` and `--requirements PATH` are mutually exclusive. Saved planning needs
an app-owned captured deployment and saved version 2 intent. The server reads
saved intent, policy, account limits and capture in one consistent snapshot;
there is no requirements export or separate client-side read. Omit
`--expected-revision` to plan from the current revision, or supply the revision
from a notification or route result to reject intent changes before planning.
This pins intent; the plan always reads current configuration and capture.

The artifact and human review show `requirements_revision` and the existing
`requirements_sha256`. The plan fingerprint binds both. Apply reads saved intent
again under the same app lock used by requirements updates, requires the reviewed
revision and recomputes the entire plan. A revision mismatch returns HTTP 409
without changing policy or recording a receipt. Replacing intent and later
restoring identical content still requires a fresh review. A no-op save retains
its revision. Captured inventory and configuration retain their existing stale
plan protection; authentication and unknown evidence remain unresolved.

Identical retries of a committed repair return its original durable receipt even
if saved intent or capture later changes. The receipt records the historical
outcome; it does not claim that current requirements are satisfied. Read route
results for current recovery evidence. Existing file-based plans remain available
for proposed intent and retain their existing fingerprint format.

Direct API and SDK callers set `saved: true`, omit `requirements`, and provide
`deployment_id`. `expected_revision` is optional on plan and mandatory on saved
apply, together with `expected_plan_sha256`, `confirm: true` and the same retry
key. `--consolidate-budgets` and burst choices work in either planning mode.

## Route-group plans

Use version 2 requirements to remediate a complete captured route inventory:

```sh
gregale routes plan my-api --requirements gregale-routes.yaml \
  --deployment 00000000-0000-4000-8000-000000000001 \
  --throttle-burst 20 --out route-plan.json --fail-on-unresolved
gregale routes apply my-api --plan route-plan.json --confirm
```

Replace the example UUID with a deployment belonging to this app that has a
complete captured OpenAPI contract and current endpoint-discovery entitlement.
`--deployment` is required for version 2
requirements and is rejected for version 1. Planning reads the stored capture;
it does not fetch a live schema or probe application routes.

The version 3 server artifact includes the deployment ID, captured contract
SHA-256, group assignments, and before/after checks for every captured operation.
Each throttle or budget proposal lists the intersecting captured operations
whose selected policy changes and any intersecting public exceptions that retain
their policy. Apply includes the deployment binding and rechecks the entire
inventory under transaction locks. Changed, missing or truncated captures require
a fresh review, even when operation paths still look the same. Receipts preserve
the selected capture fingerprint and committed group coverage.

Groups describe policy intent; by default generated rules target individual
captured families. `/checkout/{id}` can produce `/checkout/*` for one host and method.
Whole-segment parameters become `*`; the gateway's trailing `/*` also matches
descendants. The report explicitly marks this scope beyond the capture: wildcard
rules can affect uncaptured and future paths. A newly declared operation still
needs another coverage report. Shared throttle bucket admission effects also
remain outside configured-selection impact.

Overlapping rate and budget ceilings combine using the tightest bound. Conflicting
key dimensions or missing-identity policies stay unresolved. Each accepted
proposal rechecks every captured operation and preserves every previously
satisfied overlapping check. Public exceptions must keep their selected policy;
a proposal that changes an exception or cannot prove its request context stays
unresolved. Group exemptions do not establish public runtime access.

Uncovered operations, empty groups, stale exact assignments, partial family
selection, ambiguous rules, unsupported families and incomplete captures prevent
apply. Authentication remains a manual review. Planning shares the coverage
work/byte/finding budgets across all rechecks and supports up to 32 changes. A
work-limit failure discards proposed changes and returns blocked/unknown coverage.
Reduce the inventory or resolve policy manually before planning again.

Public-exception rationale is input-only policy commentary. The CLI replaces it
with `declared_public_exception` before transmitting requirements; the server
also normalizes direct API requests. Plans, receipts and report findings never
contain the original rationale. This commentary does not affect normalized
policy fingerprints.

## Review a plan

### Consolidate group budgets

For version 2 requirements, opt into compatible budget proposals within the
declared group prefixes:

```sh
gregale routes plan my-api --requirements gregale-routes.yaml \
  --deployment 00000000-0000-4000-8000-000000000001 \
  --consolidate-budgets --out route-plan.json --fail-on-unresolved
gregale routes apply my-api --plan route-plan.json --confirm
```

For example, two `GET /reports/...` operations needing identical configured
budgets can receive one `GET /reports/*` rule for a declared `/reports/` group.
**This option permits scope beyond the capture:** the prefix also covers
uncaptured and future paths. Review the actual selector and captured impact
before apply. Consolidation never creates a prefix outside a declared budget
group or combines methods or throttle buckets.

The planner combines overlapping ceilings and preserves each existing baseline
before comparing proposed actions. It consolidates only identical proposals for
at least two violated operations, and only when one ordinary family selector
does not already cover the batch. Captured operations already satisfied,
unassigned or uncertain retain their selected policy, as do public exceptions.
Existing rules can still take precedence when they are provably preserved.
Incompatible budgets, conditional selection, ambiguous precedence or unsafe
exceptions fall back to individual family proposals; remaining uncertainty
still blocks apply.

Plans show the consolidated budget group, affected operations, and app rule
counts before/after against the current plan quota, including disabled rules.
Existing rules are retained; the count reports actual proposed usage, not a
claim that every captured route otherwise needed its own rule. Shared work and
32-change limits still apply. The option is fingerprinted and forwarded from
the artifact on apply, then recomputed under the existing transaction locks.

### Individual changes

Every proposed change includes:

- An affected host and method, with a concrete path or a captured route family.
- Expected, current, and proposed configuration summaries.
- A `create` body for `POST /v1/apps/{slug}/edge-rules`, or an `update` body
  for `PATCH /v1/edge-rules/{id}`.
- The existing rule ID for an update, or the broader rule displaced for the
  exact request shape by a create.
- A reason for the proposal.

The plan contains complete `before` and `after` requirement evaluations.
`before.policy_scope` is `current_app`; `after.policy_scope` is
`proposed_app`. The latter checks configuration in memory, not policy already
installed at the gateway. A new rule's `simulated_rule_id` is a local reference,
not an ID allocated by the API.

For example, a priority-10 shared throttle on `POST /api/*` can be overridden
by a priority-9 throttle on the app's exact platform host and
`POST /api/checkout`. The broader rule is retained. A selected rule already
restricted to that exact host, method, and path receives an action-only update.
The planner does not change selectors, disable rules, or move a broader rule's
priority.

These exact selectors bound changes to policy selection. A new throttle owns a
new bucket, and its traffic leaves the former shared bucket. That can change
admission for traffic remaining in that shared bucket; the planner does not
model live buckets or promise unchanged runtime behavior for other routes.

## Throttle proposals

The planner changes the selected key dimension to the required `key_by`,
tightens its rate only when needed for `max_rps`, and sets a requested
`missing_key_policy`. It preserves a known existing burst and dimensional
key capacity where applicable. Switching to `none` removes dimensional-only
settings.

With no selected throttle, specify `throttle.max_rps` in the requirements and
choose a burst:

```sh
gregale routes plan my-api --requirements gregale-routes.yaml \
  --throttle-burst 20 --out route-plan.json
```

The requested maximum is used as the proposed rate for that new rule. The burst
flag applies only to new throttles without a selected policy to copy. It does
not replace bursts copied from an existing selected rule. A maximum below the
gateway's 1-RPS minimum remains unresolved.

Rates, bursts, key capacities, app-rule counts, and throttle-rule counts are
checked using the server's current account plan and limits table. Disabled rules
still consume quota. Apply rechecks the same limits under transaction locks.

## Budget proposals

A budget over `max_ms` receives a tighter configured baseline. Where there is
no selected budget rule, the planner copies the current effective app/plan
baseline and lowers it if required. This can satisfy `explicit: true` without
inventing or increasing a timeout.

Budget rechecks retain the existing plan and platform clamps. They cover the
configured guest-execution baseline without request-header overrides, not a
deadline across all request contexts or an end-to-end response-time SLO.

## Unresolved requirements

Status is:

- `ready`: proposed changes leave every declared requirement satisfied.
- `partial`: some changes were proposed, but requirements remain unresolved.
- `blocked`: requirements remain unresolved and no change was proposed.
- `no_changes`: the inspected configuration already satisfies all requirements.

Each unresolved entry names the request shape, requirement, stable code,
reason, and next action. Independent budget changes may still be proposed when
a throttle needs additional input.

Header-dependent selection, equal-priority candidates, routing/rewriting
uncertainty, missing metadata, and invalid actions retain the evaluator's
uncertainty. A broader priority-0 winner cannot receive an earlier exact
override. Unknown action fields, custom JWT-claim throttles, and custom budget
override headers require manual review rather than an opaque action copy.

Authentication requirements are checked but do not receive patches in this
version. App-wide consumer authentication and JWT policy need separate review.
Custom domains and named environment policies remain outside the platform-host scope.
Version 2 group requirements support the bounded captured families described below.

## Export and CI

`--out` creates a complete JSON file with mode 0600. Existing files and symlinks
are not replaced, including destinations created while platform reads are
running. The requirements file retains the strict validation and bounds from
the requirements checker.

```sh
gregale routes plan pr-42-api --requirements gregale-routes.yaml \
  --throttle-burst 20 --out route-plan.json --fail-on-unresolved
```

By default a generated plan exits zero even if partial or blocked.
`--fail-on-unresolved` exits 1 when requirements remain unresolved after the
proposed changes. Output and the requested artifact are emitted before that
exit. A zero exit means planning completed, not that policy was installed.

The version 2 artifact includes normalized requirements and their fingerprint, a
configuration fingerprint, and a deterministic plan SHA-256. Rule ordering,
JSON object formatting, and non-policy timestamps do not change the latter
when configuration is otherwise equal. Raw selector values and unrelated rule
actions are excluded; the exported bodies contain only generated, allowlisted
throttle/budget fields.

## Apply a reviewed plan

```sh
gregale routes apply my-api --plan route-plan.json --confirm
gregale routes apply my-api --plan route-plan.json --confirm --json
```

Apply accepts a version 2 or 3 server artifact for this app with status
`ready` or `no_changes`. Legacy local plans must be regenerated. An edited
artifact fails its local fingerprint check before any network request.

The API rebuilds the proposed changes from the normalized requirements and
options; submitted patch bodies are never executed. It locks the account, app,
and existing rules (plus the selected deployment and capture row for group plans),
compares the complete reviewed fingerprint, then writes all
changes and a durable receipt in one transaction. A stale fingerprint, quota
failure, unresolved requirement, or failed configuration verification leaves
every rule unchanged. Plan requires read scope; apply requires deploy-write
scope. Both use the existing ownership and MFA gates.

The receipt contains actual rule IDs and a `committed_app` verification report.
It records the configuration at commit time. Recover it with
`GET /v1/apps/{slug}/route-policy/receipts/{receipt_id}`. Later edits do not
rewrite that historical evidence.

The CLI uses the plan fingerprint as its retry key. Retry the same command after
a lost response to recover the original receipt without duplicating rules.
`--idempotency-key KEY` chooses a different key; keep that key unchanged during
retries. Keys remain valid for the app's lifetime. Reusing a key with a different
request returns a conflict. Applying the same artifact and key again always
returns its original result, even after subsequent policy changes; a new
application attempt needs a fresh reviewed plan and, when its fingerprint is
identical, a new explicit key.

Gateway confirmation is separate from the transaction:

- `active`: every registered serving gateway acknowledged this generation.
- `converging`: policy committed, but gateway confirmation is incomplete.
- `unknown`: a committed retry receipt was recovered while preparation was unavailable.
- `unobserved`: the installation has no registered serving gateway acknowledgments.

A returned receipt means the transaction committed, including when gateway
confirmation is incomplete. These configuration checks do not replace runtime
tests or establish application authorization.

See [preview route reports](route-change-report.md) for release contract,
traffic, and supplied test evidence.
