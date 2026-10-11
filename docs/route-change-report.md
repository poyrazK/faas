# Preview route change reports

For local source changes, use [route source impact](route-source-impact.md)
to connect supported application handlers and shared modules to affected
endpoints before deployment.

`gregale preview report` assembles a read-only review of a preview's
HTTP surface. It compares captured deployment contracts and gateway policies,
revision-attributed traffic observations, and supplied test
evidence. It does not deploy, invoke routes, change policy, or post a PR comment.
The report covers the selected preview app. Use `gregale preview review` to
combine several member previews into one release-wide review.

```sh
gregale preview report pr-42-checkout
gregale preview report pr-42-checkout --json > route-report.json
gregale preview report pr-42-checkout --format markdown > route-report.md
```

For a release that spans multiple preview apps, review all services in one
command. Route paths remain scoped to their app, and release priorities put
blockers first, then use observed tenant/consumer reach and requests to order
routes at the same priority:

```sh
gregale preview review pr-42-checkout pr-42-catalog \
  --source-impact pr-42-checkout=checkout-impact.json \
  --source-impact pr-42-catalog=catalog-impact.json \
  --test-report pr-42-checkout=checkout-tests.json \
  --test-report pr-42-catalog=catalog-tests.json \
  --format markdown
```

The JSON result uses a version 1 release envelope containing each app's full
version 7 preview report and a release-wide `review_priorities` list. Repeat
`--baseline-deployment`, `--source-impact`, `--test-report`, and `--requirements`
with `PREVIEW=VALUE` to bind optional evidence to the correct app. The command
accepts up to 20 unique preview slugs. An unavailable preview is listed as
incomplete and causes a nonzero exit; the `--fail-on-*` flags apply across all
available app reports. `--fail-on-requirements` requires a requirements file for
every preview. Customer identity details remain omitted unless
`--customer-details` is supplied. Combined local evidence files are limited to
64 MiB per release review.

Markdown can be attached to a CI job summary. JSON uses a `version: 7` envelope,
with or without `--source-impact`. Update consumers that accepted only earlier
versions. Version 7 ties per-route `policy_drift` to the selected deployment
pair using immutable snapshots; version 6 added per-route drift for matching
app edge rules. Version 5 added [observed customer exposure](route-customer-impact.md)
for the selected baseline deployment, with counts by default and optional
identity details. Version 4 added separate declared-security evidence and per-route
`security_compatibility`, alongside request-comparison evidence and findings,
selected deployment IDs, document fingerprints, and per-route next actions.
No raw rule actions, schema values, defaults, examples, reference URLs,
request queries, or test error messages are included.

Policy drift reads the edge-rule snapshots captured in the same transaction
when the selected deployments first became live. It compares rules whose path
and method selectors cover each reported route, including additions, removals,
priority/enabled/mode changes, and action changes. Rule IDs and selectors are
provenance; match-header values and raw actions stay out of the report. The
top-level `policy_drift.scope` is `deployment_pair`. A missing or unreadable
snapshot leaves route-level drift `unknown`, never replaced with today's
mutable app rules.
This comparison describes captured configuration; it does not simulate every
host, header, or request context. The owner-scoped
`GET /v1/apps/{slug}/deployments/{deployment}/route-policy` endpoint exposes the
captured rule set and its SHA-256 fingerprint for audit tooling.

Customer exposure reads the parent app's selected baseline deployment in the
`--since` window, with its end fixed at report generation time. Text, Markdown,
and JSON show distinct observed consumers and request-time platform tenants,
identified/anonymous/unresolved request counts, and last observation timestamps.
Use `--customer-details` to include bounded consumer and tenant IDs for follow-up.
Names, external references and credentials are never included. Missing or
plan-gated telemetry stays unavailable; this advisory evidence does not change
the existing release gate exits or prove that any particular client will break.

### Build a customer migration roster

The preview report is organized by route. To prioritize customer follow-up across
one app or a multi-app release, first save a report with identity details, then
derive a roster locally:

```sh
gregale preview report pr-42-checkout --customer-details --json > route-report.json
gregale preview customers --report route-report.json --by consumer --format markdown

gregale preview review pr-42-checkout pr-42-catalog --customer-details --json > release-review.json
gregale preview customers --report release-review.json --by tenant --format csv --out customer-roster.json
```

The roster lists opaque consumer IDs (scoped to an app) or tenant IDs (scoped to
the account), changed routes, change reasons, breaking-route counts, observed
request volume and last-observed time. It ranks breaking exposure first, then
the number of affected routes, observed requests and recency. The command reads
only the saved JSON report; it makes no API calls and sends no customer
communications. `--out` writes the versioned JSON roster to a new file.

This is an outreach aid, not a complete customer inventory. Reports without
`--customer-details` cannot produce a roster. Missing route observations,
truncated identities, anonymous or unresolved traffic, and clamped windows are
marked incomplete. When the same app route appears in multiple preview reports,
the roster keeps the largest per-customer route count to avoid double-counting
shared baseline traffic; sums across different routes are only a prioritization
signal, not billing totals.

### Track customer adoption after a route change

After release, compare that saved cohort with identity-linked requests on the
new immutable deployments. Map every old route in the cohort explicitly to one
or more successor routes. This avoids guessing that a similar path, renamed
operation, or split endpoint is a replacement:

```json
{
  "version": 1,
  "mappings": [
    {
      "from": {"app": "checkout", "method": "GET", "path": "/v1/orders/{id}"},
      "successors": [
        {"app": "checkout", "method": "GET", "path": "/v2/orders/{id}"}
      ]
    }
  ]
}
```

Save that as `route-successors.json`, then select the current deployment for
each app referenced by the old and successor routes:

```sh
gregale preview customers track \
  --roster customer-roster.json \
  --mapping route-successors.json \
  --deployment checkout=00000000-0000-4000-8000-000000000001 \
  --since 14d --format markdown
```

The report classifies each customer-route link as `old_route_active`,
`successor_observed`, `both`, `no_current_evidence`, or `incomplete`. An empty
`successors` array records an intentional retirement with no replacement. A
same-path contract change is labeled `in_place_unmeasurable`, because route
telemetry cannot tell old client behavior from updated client behavior when
both call the same endpoint. For a multi-app release, repeat `--deployment`
once for each app. For every observed side, the report includes request counts
and the latest observation time to help separate occasional use from adoption.
Consumer IDs are scoped to one app, so cross-app successors require a tenant
roster (`gregale preview customers --by tenant`) to preserve a stable
account-level identity.

This command makes read-only telemetry requests for the selected deployments.
Missing traffic is reported as no current evidence, never as proof that a
customer migrated or stopped using the old route. Truncated identities or route
inventories, unavailable app telemetry, and clamped observation windows make
the affected assessment incomplete. `--out` saves the versioned report to a
new JSON file for later review.

Add a version-controlled [route requirements file](route-requirements.md) to
check authentication configuration, throttle scope, and configured execution
budgets in the same report:

Version 2 groups also check whole supported route families and require every
captured candidate operation to have a policy assignment or explicit public
exception. Newly captured operations inherit matching group requirements;
empty groups and unassigned operations fail the requirements gate. Inventory
can be checked independently of the baseline contract.

```sh
gregale preview report pr-42-checkout --requirements gregale-routes.yaml \
  --fail-on-requirements --format markdown
```

Use [route policy plans](route-policy-plans.md) to propose exact-selector
throttle and budget changes for version 1 requirement violations.

## Source changes and review priorities

A shared helper can change route behavior while its OpenAPI contract stays
unchanged. Add a committed [source impact report](route-source-impact.md) to
connect that change to the selected preview and parent deployments:

```sh
# Use the exact commits declared by the selected baseline and candidate.
gregale routes impact checkout --base "$BASE_COMMIT" --head "$CANDIDATE_COMMIT" \
  --path services/checkout --out impact.json
gregale preview report pr-42-checkout --source-impact impact.json \
  --test-report results.json --requirements gregale-routes.yaml --format markdown
```

The source analyzer records a credential-free GitHub repository identity from
`remote.origin.url`. The join requires repository identity, full commit ID,
build root, and deployment app ownership to agree for **both** selected
revisions. FastAPI source reports use schema version 2; Go `net/http` source
reports use schema version 3. Reports without repository
metadata, working-tree candidates, missing deployment annotations, unsupported
repository providers, and mismatches remain explicitly `unbound`. Regenerate
legacy artifacts with the current CLI and committed candidates.

Agreement is reported as `declared_match`. Git and deployment annotations are
customer declarations, not proof that the analyzed bytes were deployed. The
Python snapshot fingerprint and deployment archive digest cover different
bytes and are never compared to establish this agreement. The supplied report
file has its own SHA-256 for audit.

Only captured deployment routes receive source findings. A unique method and
path-template match can ignore whole-segment parameter names, so
`GET /users/{user_id}` can match `GET /users/{id}`. Multiple equivalent
templates, embedded parameters, converters, encoded paths, missing captured
routes, and conflicting route presence remain unmatched. Current declarations
and traffic observations cannot substitute for captured contracts. Contract
classification stays separate from source classification.

The optional `source_impact` joins share `review_priorities` with request,
declared-security, and route-policy drift findings. Changed edge rules receive
route-level review entries; unavailable or uncomparable policy evidence needs
follow-up before relying on the comparison.
Text and Markdown show
handler locations, static reference chains, mapping status, and next actions.
Source issue messages and the artifact's free-form scope are not copied into
the shareable report. Analyzed provenance and deployment declarations appear
separately so mismatches can be resolved. Unbound and unmatched source findings
stay separate from deployment tests and traffic.

| Priority | What requires attention |
|---|---|
| `blocker` | Known request or response-contract break, declared authentication reduction or client restriction, route removal, failed candidate checks, declared requirement violations, unassigned candidate operations, or empty policy groups. |
| `needs_evidence` | Unresolved request/security/policy comparison, unbound source, incomplete analysis, unresolved route mapping, unknown requirements or family coverage, missing candidate inventory, or affected routes without passing candidate samples. |
| `review` | Source changes with available passing samples and changed route rules that need behavior review. |

Within each priority, revision-attributed baseline request counts order
attention; unavailable traffic remains unavailable. This is an explainable
review order, not a weighted risk score. Known breaks and failed checks take
precedence over traffic volume. Removed routes need compatibility review
without requiring assertions against an absent candidate route.

Passing receipts describe only supplied request samples. Source-matched tests
from another deployment remain supplemental, and a requirement on one concrete
path is never promoted to an entire template. Check the separate requirements
section for concrete-path findings that do not match a captured route exactly.
Version 2 family checks join by the exact captured operation identity and retain
group provenance; passing samples cannot remove a coverage violation.
Incomplete source analysis can omit routes even when all reported routes match.

`--fail-on-incomplete` also gates unresolved source evidence and source changes
requiring review. `--fail-on-breaking` continues to gate known captured response
breaks and route removals; request restrictions have their own gate.

## Contract comparison

The default baseline is the latest parent deployment **when it is live**. If the
latest deployment failed or is pending, the report does not silently compare
that revision as production. Select the intended parent revision explicitly:

```sh
gregale preview report pr-42-checkout --baseline-deployment DEPLOYMENT_ID
```

The baseline must belong to the parent app. During a traffic split, the default
is one selected live revision, not a combined contract for all serving revisions.
The candidate is the preview's latest deployment. Its readiness is reported
separately from contract compatibility.

Both documents come from the deployment OpenAPI capture endpoints. Missing,
truncated, inaccessible, or invalid documents make comparison unavailable;
current imports and traffic-derived documents are never substituted for captured
revision evidence. Existing plan entitlements apply to these reads.
Current declared/observed routes can still appear with `change: unknown` and
their own provenance, policy summaries, and available traffic. They cannot
establish added/removed operations or compatibility between revisions.

The route/response classifier reports added/removed operations and the existing
supported response-schema breaks. A separate directional request comparator
checks supported declared inputs. Effective operation or inherited root security
requirements are evaluated by a separate declared-security comparator. Business
behavior and runtime authentication are outside contract comparison.

## Declared authentication changes

Compare the selected captured revisions' authentication declarations before
release:

```sh
gregale preview report pr-42-checkout --test-report results.json \
  --fail-on-security-regression --fail-on-request-breaking --format markdown
```

`security` reports comparison availability independently of request shapes and
current gateway policy. Each compared route has a `security_compatibility`
result with `complete`, `changed`, `regression`, `client_breaking`, controlled
findings, and baseline/candidate summaries. Summaries show declaration source
(`root`, `operation`, or `absent`), credential kinds, and whether credentials
are required, an anonymous alternative is declared, or no requirement is
declared. Missing declarations do not prove runtime anonymous access.

The comparator supports OpenAPI 3.0/3.1 root inheritance, operation overrides,
empty arrays removing inherited requirements, and `{}` anonymous alternatives.
Requirement objects combine credentials with AND; the outer list combines
alternatives with OR. Duplicate/reordered alternatives and scopes, redundant
stronger alternatives, descriptions, bearer-format hints, and header-name case
do not create findings. Scope requirements and OpenAPI 3.1 role requirements
compare as sets for the same credential identity.

Known findings include:

- `anonymous_access_added`: a previously required declaration now permits an
  anonymous alternative or removes all declared requirements.
- `security_requirements_weakened`: the candidate strictly broadens accepted
  credential combinations, including removing an AND credential/scope or adding
  an OR alternative. This is a declared requirement reduction, not a ranking of
  authentication mechanisms' cryptographic strength.
- `authentication_required` / `security_requirements_restricted`: previously
  supported anonymous or credential combinations no longer satisfy the
  candidate declaration. These are client compatibility findings.

API keys in headers/query/cookies, HTTP basic/bearer, OAuth2 flows, OpenID Connect,
and OpenAPI 3.1 mutual TLS have supported identity definitions. Local references
under `#/components/securitySchemes/` resolve within the supplied capture only.
OAuth scopes must appear in at least one declared flow. OpenID scope declarations
are compared without fetching discovery metadata. Relative flow/provider URLs
remain unknown because the captured document's original base URI is unavailable.
Role lists on non-OAuth
schemes are unsupported in OpenAPI 3.0. The comparator tracks the same component
name across revisions; renamed/replaced credentials can remain incomparable.

A referenced credential's transport/name, HTTP mechanism, OAuth flow endpoints,
or OpenID provider change produces `security_scheme_changed`, including when the
requirement list is unchanged. These changes and requirements that both broaden
and restrict accepted combinations remain explicit unknowns requiring client
and security review. Unresolved/external/cyclic references, unsupported schemes,
malformed requirements, and invalid metadata cannot become unrestricted rules.
A known new anonymous alternative can coexist with unsupported candidate
alternatives; it remains a regression with `complete: false`.

Known reductions and client restrictions become review blockers, even with
passing candidate samples. Unresolved findings require evidence. Baseline traffic
orders routes within priority; traffic and missing samples never suppress a
security finding. The report can have `security_regressions` or
`security_client_breaking_changes` outcomes. Existing response/request breaks,
test failures, and policy violations retain outcome precedence; the individual
security fields and gates remain available independently.

Added/removed operations are `not_compared` for security; the route classifier
owns their review. Referenced path items or unparsed HTTP operations make the
whole security comparison unavailable. Independent centralized bounds allow
2,000 routes, reference depth 64, 50,000 work nodes (including every implication
pair and credential/scope visit), 5,000 findings, 4,096 bytes per semantic string,
and 16 MiB aggregate semantic metadata. Exhausting aggregate bounds discards the
whole comparison instead of returning partial success.

The report emits no credential names, actual header names, scopes/roles, provider
URLs, raw references, or extensions. Inspect the original captured contracts for
those details. It performs the same read-only API calls and sends no application
requests. It does not verify token validity, application authorization, tenant
ownership, runtime enforcement, or historical gateway policy. Current JWT and
consumer-auth configuration remains separate requirements evidence.

## Request compatibility

A candidate can break callers by accepting fewer request inputs even when its
response schema stays identical. For example, making `currency` required on
`POST /checkout` is a request restriction. Removing that requirement widens
accepted inputs and does not create a request review task by itself.

```sh
gregale preview report pr-42-checkout --test-report results.json \
  --fail-on-request-breaking --format markdown
```

The comparator uses both captured OpenAPI 3.0 or 3.1 documents. It does not depend
on a source-impact artifact or framework-specific source analysis. Its supported
checks include:

- New required bodies, object fields, and query, header, or cookie parameters.
- Narrowed core types, scalar enums, and nullability (`nullable` in 3.0,
  type unions in 3.1). Integers remain accepted by a `number` schema.
- Numeric minimum/maximum bounds, including OpenAPI 3.0 boolean exclusive
  modifiers and OpenAPI 3.1 numeric exclusive bounds. Integer rules are
  normalized to accepted integer endpoints: `> 0` and `>= 1` are equivalent.
- String `minLength`/`maxLength` and array `minItems`/`maxItems`. String lengths
  count Unicode code points, not encoded bytes. Zero minimums match omission.
- OpenAPI 3.1 `anyOf` with exactly two branches: a supported value schema and
  a null-only schema. Annotations and local schema references are supported;
  branches can appear in either order. Nullable models and array items retain
  their supported structural and validation checks.
- Boolean `additionalProperties`, declared object properties, and array items.
  Removing a property from an open object can widen acceptance; removing it
  from a closed object restricts acceptance. Adding a typed property to a
  previously open object can restrict previously unconstrained values.
- Removed accepted media types, including wildcard coverage and more-specific
  schema overrides. Schemas are compared using the most specific media entry.
- Effective path-level and operation-level parameters, case-insensitive HTTP
  header identity, and local `components/schemas`, `components/parameters`, and
  `components/requestBodies` references, including JSON Pointer escapes.

Descriptions, examples, defaults, declaration order, and explicit default
serialization settings do not create a request change. Operation parameters
replace shared parameters with the same declared name and location. A newly
documented path parameter remains unknown: its path segment already existed.
Schema restrictions on an existing declared path parameter are compared.

Each captured route has `request_compatibility` with `method`, `path`, `status`,
`complete`, `changed`, and structured `findings`. A finding contains a controlled
severity/code and a schema or parameter location; uncertainty can name the
`base` or `candidate` revision. Enum values and original reference URLs are never
copied into findings.

| Status | Meaning |
|---|---|
| `breaking` | At least one supported declared input restriction was found. |
| `no_supported_breaks` | The supported comparison completed without a known restriction. |
| `unknown` | Unsupported or unresolved evidence prevents completion. |
| `not_compared` | The operation exists on only one side; route additions/removals are handled separately. |

Bounds are compared against inputs allowed by the baseline. A finite enum that
still fits a tightened range or length limit does not create a break. An enum
that covers every value in a bounded integer range, a numeric singleton, or an
empty-string-only schema is also compatible. Coverage counts candidate values
without expanding large ranges. Impossible
numeric/length/item domains are removed from the accepted types, while a null
alternative can remain accepted. Empty-array-only schemas do not create item
restriction findings for items that cannot occur. Bound values stay private;
findings report controlled codes and normalized schema locations.

For example, reducing an integer `quantity` maximum from 100 to 20 produces
`numeric_maximum_restricted`. Increasing it back widens acceptance. Tightened
minimums/maximums produce corresponding numeric, string-length, or array-size
findings through the existing review queue and request CI gate.

A known restriction can coexist with `complete: false`: for example, a newly
required body can be identified even if its schema reference is unresolved.
Passing HTTP samples never erase that finding. Request breaks are blockers in
the review queue; unresolved requests need evidence. Supported widening changes
need no request review, while independent source or policy findings still apply.

General composition (`allOf`, `oneOf`, other `anyOf` shapes or validation
siblings on a nullable union), formats, patterns, `multipleOf`, uniqueness,
object property counts, schema-valued `additionalProperties`, non-scalar enums,
read-only schemas, non-default schema dialects, external or cyclic references,
body encoding, parameter `content`, and serialization changes remain explicit
unknowns. Malformed bounds also remain unknown; their original values are not
included in output. Required read-only fields
in OpenAPI 3.0 apply only to responses. Removed body/parameter declarations do
not prove whether the application still accepts those inputs and remain unknown.
Unresolved baseline parameter identities cannot establish that candidate
parameters are newly required. No external reference is fetched.

Referenced path items, unparsed HTTP operations (including the legacy loader's
unsupported TRACE operation), and exhausted aggregate work/finding limits make
request evidence unavailable. No partial request findings are retained, and
existing response findings remain available. Invalid or oversized individual
property/reference metadata produces a controlled unknown finding. Bounds are centralized in
`pkg/api/limits.go`: 2,000 operations, depth 64, 50,000 work nodes, 5,000 findings,
1,000 enum entries per schema, 4,096 bytes per metadata item, and 16 MiB of
normalization work bytes across both documents.

Known request breaks produce `request_breaking_changes` unless an existing
response break, candidate test failure, or policy violation already has
precedence. Unknown requests contribute to an incomplete report. Availability
of captured evidence and comparison completeness are separate facts: an
available request comparison can still contain unknown routes. The command
uses eight account-scoped GETs in its default path and runs no tests or
application requests. Adding `--requirements` performs one candidate app
configuration read; its route-rule inventory reuses the report's candidate
edge-rule read.

## Policy and performance evidence

Policy kinds are summaries from the current preview app's OpenAPI policy preview.
They are not a historical deployment policy snapshot or proof of effective
authentication. A missing summary means coverage was not established. Use
`gregale edge-rules trace` to inspect a concrete request, including runtime
decisions that cannot be predicted offline.

Traffic defaults to the trailing 24 hours; use `--since 168h` for a longer
lookback. The report echoes the actual windows and notes retention/list clamps.
Route p95, errors, request count, and cold-request count are attached only when
all observations for that route belong to the selected deployment. Mixed or
unattributed revisions produce no latency delta. Differences are advisory:
production and preview traffic can have different request mix and cold/warm
proportions. They are not a controlled benchmark or a release gate.

## Supplied lifecycle test evidence

```sh
gregale test --profile all --report results.json
gregale preview report pr-42-checkout --test-report results.json
```

The CLI records `source_sha256` when the queued deployment provides it. Matching
source hashes let the report show warm/cold/restored test results from isolated
test deployments as **supplemental source evidence**. Those environments can
have different policies, secrets, fixtures, and dependencies.

Candidate coverage requires receipts naming the exact candidate deployment and
preview app. Older receipts without source identity, local/simulated results,
and unrelated revisions are not promoted to candidate coverage. The input is a
customer-owned JSON report, not a platform-signed attestation. Failed runs or
cleanup cannot be reported as passing checks. Only HTTP checks are attributed
to routes; arbitrary assertion commands do not establish endpoint coverage.

## CI behavior

By default, successfully generating a report exits zero, including incomplete
reports and reports with findings. Invalid inputs or a failed preview/parent
lookup exit nonzero.

- `--fail-on-breaking` exits 1 for a known route removal or supported
  response-contract break. Missing evidence alone does not satisfy this gate.
- `--fail-on-request-breaking` exits 1 for a known supported request restriction.
  This includes stricter declared authentication requirements. Unknown requests,
  unknown credential definitions, and one-sided operations alone do not satisfy
  this gate.
- `--fail-on-security-regression` exits 1 for a known reduction in declared
  authentication requirements. Missing/unknown evidence and stricter client
  requirements alone do not satisfy this gate. Combine with
  `--fail-on-incomplete` to reject unresolved security evidence as well.
- `--fail-on-policy-drift` exits 1 when any route rule configuration changed or
  the parent/preview comparison is incomplete. Intentional changes can be
  reviewed in the emitted report before updating the CI baseline.
- `--fail-on-incomplete` exits 1 when the outcome is not `no_findings`, including
  missing or unresolved evidence, source review, failed tests, and known breaks.
- `--fail-on-requirements` requires a requirements file and exits 1 for violated
  or unknown configuration requirements. Missing latency or test evidence does
  not trigger this particular gate.
- Combine the flags when CI should reject both known breaks and incomplete
  evidence. Output is emitted before the gate exit, so CI can retain the report.

`no_findings` is bounded by the stated classifier and evidence coverage; it is
not a claim that the API is universally compatible or safe to release.

## Next increments

1. Extend request comparison to additional composed schemas and validation
   keywords.
2. Run customer-defined assertions against an identified preview revision and
   persist coverage without requiring a local receipt attachment.
3. Turn a selected failure into an editable, redacted regression scenario with
   explicit fixtures and isolated dependencies.
4. Extend policy plans with the preview evaluator's scoped template coverage
   and confirmed application of reviewed changes.
5. Validate optimization suggestions through controlled comparisons, including
   separate warm, cold, and restored measurements.
