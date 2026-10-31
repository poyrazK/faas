# Configure service bindings

Use `gregale bindings service configure APP --file policy.json` to update a
standalone app's service targets, inbound caller permissions, reliability and
URL transport. The
file is a partial JSON patch: omitted fields retain their current settings.
The command uses the selected connection profile and updates app-level settings;
it does not select or configure a named project environment.

For example, allow `public-api` to call only its declared `billing` dependency:

```json
{
  "service_binding_targets": ["billing"],
  "service_binding_policy": "declared"
}
```

```sh
gregale bindings service configure public-api --file policy.json --dry-run
gregale bindings service configure public-api --file policy.json --json
```

Configure the target separately. To allow only `public-api` and limit its
requests to GET on `/invoices` and slash-delimited descendants, apply this file
to `billing`:

```json
{
  "allowed_service_callers": ["public-api"],
  "allowed_service_call_scopes": {
    "public-api": {
      "methods": ["GET"],
      "path_prefixes": ["/invoices"]
    }
  }
}
```

Both restrictions apply together. Adding a caller to one field does not grant
access through a restriction retained in the other. Caller-side `declared`
policy and target-side grants must both permit a request. Discovery bindings
alone do not restrict outbound calls while the caller's policy is `account`.

| Field | Omitted or `null` | Empty value |
| --- | --- | --- |
| `service_binding_targets` | Unchanged | `[]` removes all declared targets |
| `service_binding_policy` | Unchanged | Invalid; use `account` or `declared` |
| `allowed_service_callers` | Omitted: unchanged; `null`: remove caller-list restriction | `[]` denies every caller |
| `allowed_service_call_scopes` | Omitted: unchanged; `null`: remove method/path restriction | `{}` denies every caller |
| `service_reliability` | Omitted: preserve overrides for retained targets; `null`: clear all overrides | `{}` clears all overrides |
| `service_binding_transport` | Unchanged | Invalid; use `http` or `https` |

To restore same-account inbound access, explicitly set **both** caller fields
to `null`. To restore legacy caller-side reachability, set
`service_binding_policy` to `account`. These actions broaden access and appear
in the preview. Removing outbound targets can also remove their stored
reliability overrides under the existing API behavior.

Unknown fields, duplicate JSON keys, invalid names, self-targets, and malformed
method/path grants fail before any API request. Files are bounded to the existing
source-manifest size limit. Names and grants use the API's normalization rules.
Only the six fields above are accepted.

Dry-run reads the current app and displays current and proposed values for
submitted fields and implicit reliability pruning without saving changes. It is
a snapshot, not a reservation or a concurrency fence. The server checks account membership, target existence,
and project-managed restrictions when applying the single atomic app update.
The apply receipt includes the saved values returned by the server. Saving
settings does not confirm guest adoption or successful service requests.

After applying, inspect and verify the result:

```sh
gregale bindings public-api --json
gregale bindings verify public-api billing --json
```

## Reliability and URL transport

The same file can configure timeout and retry behavior for each declared target,
and select the canonical URL scheme injected into the caller:

```json
{
  "service_binding_targets": ["billing"],
  "service_binding_transport": "https",
  "service_reliability": {
    "billing": {
      "timeout_ms": 1000,
      "max_attempts": 2,
      "min_remaining_ms": 100,
      "retry_budget_percent": 10
    }
  }
}
```

`service_reliability` is a full replacement map. Omission preserves existing
overrides except those pruned when their target is removed; `null` or `{}` clears
all overrides. Each policy must name a target in the resulting binding list,
including any target added in the same file. Unknown fields, invalid budgets,
and a minimum remaining time greater than a positive timeout are rejected.
The CLI uses the API's existing bounds and normalization rules.

Non-idempotent retries remain disabled unless the individual target policy
explicitly sets `"allow_non_idempotent": true`. Use that option only when the
called operation can safely tolerate retries. This command configures the
existing gateway policy; it does not guarantee retry execution or exactly-once
effects.

`service_binding_transport` accepts `http` or `https`; omission or `null` leaves
it unchanged. It selects the canonical `GREGALE_SERVICE_<NAME>_URL` scheme;
it does not by itself enforce HTTPS-only access or confirm that a running guest
has adopted the changed URL. The explicit HTTPS alias remains available.

Previews and saved receipts include reliability overrides removed implicitly
by a target-list update. When reliability is omitted, the PATCH still omits it,
so pruning remains owned by the API rather than replacing a stale snapshot.

## Export and check policy drift

Capture current settings in a reusable configuration file:

```sh
gregale bindings service export public-api --out policy.json
gregale bindings service check public-api --file policy.json --json
```

Export includes all six service settings, including explicit `null` for
unrestricted caller policies and empty collections for deny-all policies.
Omitting `--out` prints the policy JSON directly to stdout. File exports are
private (0600) and never replace an existing destination. Treat exported files
as dependency and authorization configuration; they contain no API token.

Check is read-only. It compares only fields supplied in the policy and uses
the same validation and normalization as configure. Ordering of target names,
callers, methods, path prefixes, and map keys does not cause drift. A null
reliability policy and an empty reliability map both mean no overrides.
Null target, policy, and transport fields retain configure's no-op semantics.
Caller nulls remain distinct from empty deny-all policies.

Exit codes are **0** for a match, **1** for drift, and **2** for invalid input,
I/O, authentication, or API errors. JSON results contain `app`, `match`, and
field-level `changes` with `before` (live) and `after` (desired) values.
A check is a snapshot; use a reviewed saved plan for a guarded update.
It does not verify runtime adoption or connectivity. These commands read
app-level settings and do not select a named project environment.

## Explain service authorization

Explain the configured policy decision for an exact caller and target app:

```sh
gregale bindings service explain public-api billing --method GET --path /invoices --json
```

The command reads both apps through the selected account connection, without
creating tasks or sending application requests. It evaluates the caller's
outbound account, declared or project policy, the target's caller list, and the target's
caller method/path scopes. Both inbound restrictions must permit the caller.
Invalid per-target reliability policies also block calls at the gateway.

Methods are normalized to uppercase. Paths use the gateway's decoded path
prefix matching: `/invoices` permits itself and `/invoices/123`, but not
`/invoices-private`. Query parameters do not affect scope checks and are omitted
from the report. Invalid methods, ambiguous paths, hosts, and fragments are
rejected before API access.

JSON results include `result` (`allowed`, `denied`, or `indeterminate`),
`checks` with stable `rule` and `reason` values, the relevant `setting` and
review hint, the observed service fingerprints, and `canonical_transport`.
Exit codes are **0** for allowed, **1** for denied, and **2** for indeterminate
or command errors. Missing or unreadable apps produce indeterminate results;
they do not prove a service call would be denied. Previews and incomplete,
invalid, or unknown policy contracts also produce indeterminate results.
The command requires app responses with service-settings fingerprints.

This is an app-level configuration explanation from separate reads, assuming
canonical transport and authenticated same-account service identity. It does
not resolve named project environments, release routing, or preview identities.
It does not verify runtime adoption, caller credentials, connectivity, or
application-level authorization. Use binding verification and runtime smoke
checks to investigate those layers. Hints suggest settings to review and never
apply changes or automatically broaden access.

### Explain an exact deployment

Select the caller deployment to include environment and release routing:

```sh
gregale bindings service explain public-api billing \
  --deployment CALLER_DEPLOYMENT_UUID --scope staging \
  --method GET --path /invoices --json
```

The environment comes from the selected app-owned deployment. `--scope` is an
optional assertion: a mismatch produces a denied explanation. It requires
`--deployment`; neither selector switches the selected account. For managed
deployments, `billing` is the logical workload name in the reviewed graph and
may differ from the physical target app slug. Ordinary deployments use the
target app slug.

The read-only API uses the gateway's app authorization, managed environment
binding resolver and project release resolver. For managed routes it evaluates
frozen method/path policy and transport after app authorization, and returns
the exact target deployment and active reviewed release. Missing bindings never
fall back to global app names. Checks explain missing active releases, stale
caller membership, scope mismatches and resolver denials. An ordinary project
route can select a retained release under the existing gateway rules; an
unpinned service route reports `unpinned_service_route` without inventing a
target deployment.

JSON adds `caller_deployment_id`, `target_app_id`, optional
`target_deployment_id` and `release_id`, plus `policy_source` (`app` or
`environment_release`). `scope` is the caller environment. App fingerprints
describe observed app settings, not the frozen release policy. The same exit
codes apply: 0 allowed, 1 denied, 2 indeterminate/error. The CLI checks response
identities and never falls back to app-only evaluation if the server lacks this
API or returns an incomplete explanation. Query values are removed before the
CLI sends the path to the API and are excluded from reports.

The API is `GET /v1/apps/{slug}/bindings/services/{service}/explanation`, with
required `deployment_id` and optional `scope`, `method` and `path` query
parameters. It requires app read permission and completed MFA. Foreign caller
deployments return 404; foreign or unreadable targets produce indeterminate
results without target identities. Duplicate or unknown selectors are rejected.

These are separate metadata reads assuming an authenticated caller from the
selected deployment. They do not establish live caller credentials, guest
adoption, connectivity, endpoint selection or application authorization.
Previews, held qualification guests and development bridge contexts are not
evaluated. Use runtime verification for those observations.

## Inspect deployment service bindings

```sh
gregale bindings public-api --deployment v12 --json
gregale bindings check public-api --deployment v12 --json
```

The inventory command accepts an exact deployment UUID or app version. The
server must confirm that selector and publish the service namespace source;
older servers without this contract are rejected. The CLI never silently inspects another
deployment. Without it, the server selects the current live deployment. Ordinary
unmanaged deployments retain app-level service discovery. Managed deployments
use only the selected deployment's frozen service map, including its custom
environment keys and effective transport. An empty or unreadable frozen map
does not fall back to app settings.

`service_binding_source` identifies `app`, `environment_release` or
`unavailable` metadata. Each managed service entry has `service_context` with
the caller deployment, account-owned physical target app, active reviewed
release when resolved, routing status/reason and an opaque context revision.
The entry's name remains the logical workload; `service_target_deployment_id`
is the actual target selected by the managed resolver. Context revisions change
when the frozen graph, selected target or release changes, and participate in
durable review configuration digests. They are metadata, not probe receipts.
Scope mismatches, invalid frozen data and failed reads make inventory incomplete;
known denied routes remain visible. `service_targets` cannot override a managed
release route.

Active request/service and worker callers with resolved frozen HTTPS bindings
to request/service targets publish
`verification_supported: true` and `verification_contract: managed-https-v1`.
Verify the selected logical binding with:

```sh
gregale bindings verify public-api billing --deployment v12 --json
gregale bindings verify public-api --all --deployment v12 --require-complete --json
gregale bindings verify orders-worker billing --deployment v12 --json
```

The server pins the custom environment key, caller deployment, physical target,
exact target deployment, active release and an opaque context revision in the
task ledger. The guest requires the staged caller identity and frozen URL,
validates private TLS, and checks exact gateway deployment/release confirmations.
On named compute nodes, the guest service listener authenticates the probe's
network address through vmmd's live map and its current task lease. Older vmmd
nodes reject managed probes before boot; task VMs never become serving replicas.
The CLI refreshes inventory after completion. Changed graph inputs, targets,
releases or service policies make old evidence stale. Ordinary app receipts are
excluded even if their deployment/name matches. Inventory and checks recognize
only matching managed receipts.

Worker verification uses a fresh task guest with the frozen worker configuration.
It does not start the image's worker entrypoint or listener, restore a worker
snapshot, or consume queued work. A single-service probe never publishes a queue
message. If the worker also has eligible queues, strict `--all` verification
requires explicit `--queue-publish` consent for those separate bounded canaries.
The frozen workload class and effective execution mode must agree. This extension
adds HTTPS service evidence, not managed queue-canary admission for zero-traffic
worker deployments. Strict `--all` still fails when a queue lacks supported
canary evidence, even after its HTTPS service probe passes.

Held candidates, Job callers, worker/Job targets, HTTP edges, unresolved routes and unknown
contracts remain `unsupported` with `managed_service_verification_unsupported`.
Checks and strict batch coverage block those gaps; `--allow-unsupported` does
not waive them. Managed service smoke calls remain unavailable. Route probes
do not establish resident application adoption, application behavior or reviewed
environment qualification.

For managed callers, a gateway route probe can succeed only while the exact
target deployment selected by the active reviewed release has a healthy replica.
Conflicting deployment or release selectors are rejected. A successful response
confirms both selected identities without waking the target or invoking its
application. A raw gateway response alone is not a managed binding receipt;
the admitted managed guest report and current context checks are also required.

## Save and apply a reviewed plan

Save a plan to a new private file, then apply that exact patch:

```sh
gregale bindings service configure public-api --file policy.json --plan-out service-plan.json --json
gregale bindings service apply public-api --plan service-plan.json --json
```

`--plan-out` implies preview and makes no app mutations. The plan contains the
normalized patch, before/after review, app and account IDs, API endpoint, and an
opaque fingerprint of the reviewed service settings and source ownership.
Existing files are never replaced. Keep the plan private: it contains dependency
and authorization configuration. It does not contain an API token.

Apply requires the same endpoint, account, app identity and service settings.
The server checks the fingerprint while holding the app row lock, in the same
transaction as the update and any activity handoff. Conflicts return HTTP 409;
create and review a fresh plan. Apply never silently refreshes a stale plan or
falls back to an unguarded update on an older server. Unrelated app settings can
change without invalidating the plan, and their latest manifest values survive
the guarded update.

The fingerprint compares current content, not change history: changing settings
and restoring the same reviewed state may make the plan applicable again. Plans
have no time-based expiry and do not guarantee target availability or guest
adoption. A checksum detects modified plan files; it is not a signature or an
additional authorization grant. Normal API authorization and source-management
restrictions still apply. Direct `configure --file` without a saved plan retains
the existing unguarded partial-update behavior.
