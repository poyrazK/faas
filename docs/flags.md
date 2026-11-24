# Gregale Flags

Gregale Flags releases already deployed application behavior to selected platform
customers without a new deployment. The initial operator qualification supports boolean flags,
named string variants, explicit customer lists, owner-managed customer groups,
authenticated application subject targeting, sticky weighted allocation and
customer- or subject-level percentage rollout, versioned configuration,
rollback, a decision inspector, Node, Python and Go runtime SDKs, and request
cohort evidence. Business
logic must explicitly check a flag. Flags do not grant
permissions or paid entitlements and are independent from deployment traffic splits.

## Change one flag from the CLI

The short commands read the current configuration, change one thing and
publish it with the version they read. If someone else published in between,
they apply the same change to the new configuration, at most three times.

```bash
gregale flag create new-export --project exports              # enabled, default false
gregale flag rollout set new-export 10 --project exports      # 10% of customers get true
gregale flag rule create new-export --id pilot --group pilot-customers --project exports
gregale flag disable new-export --project exports             # also sets default false
gregale flag list --project exports
gregale flag create checkout --variants legacy,new --project exports
gregale flag delete new-export --project exports
```

### Kill a flag

```bash
gregale flag kill set new-export --project exports     # every check serves the fallback in your code
gregale flag kill unset new-export --project exports
```

A killed flag (`"killed": true`) evaluates to the **fallback your code passes**
at the call site, with reason `killed`, whatever its default, rules or a
decision inherited from another service say. The fallback is the behavior your
code had before the flag existed, so a kill never turns something on.

Gregale's gateway lists each environment's killed flags on every request
(`X-Faas-Flag-Kills`), and the SDKs honor that list before their cached
configuration, so a kill applies from the next request rather than the next
refresh. Work that does not arrive through the gateway (queued messages, cron)
picks the kill up on the SDK's next refresh. SDKs advertise capability
`kill-v1`; an older SDK receives configurations without killed flags, so it
also serves its fallback (reason `flag_missing`) after its next refresh. A kill
is a published version like any other change, with history and rollback.

`rollout set` manages a rule named `everyone-percent` that always stays last;
`rule create` inserts new rules before it, so targeted rules win. `disable` on a
boolean flag also sets its default to `false`, because a disabled flag serves
its default. Use `flags apply --file` for whole-configuration changes. The earlier spellings
(`flags on`, `off`, `percent`, `rule-add`, `rule-rm`, `kill`, `unkill`, `stale`)
keep working.

### Owners, expiry and stale flags

Give each flag an owner and a removal date when you create it:

```bash
gregale flag create new-export --owner payments --expires 2027-01-31 --project exports
gregale flag set new-export --expires never --project exports
gregale flag stale list --project exports          # add --all for every flag
```

`flag stale list` (`GET .../flags/lifecycle`) reports, for each flag, its owner,
expiry, the newest version that changed it (looking back 200 versions), whether
it serves one value to everybody, and how many requests decided it in the last
7 days when your plan keeps request telemetry. A flag needs attention when it
is `expired`, `expires_soon` (within 7 days), `stale` (serves one value and
unchanged for 14 days), `never_evaluated` (enabled, unchanged for a week and no
decisions) or has `no_owner`. Gregale also checks every environment hourly and
records one `flags.lifecycle_attention` audit event per flag, reason and
configuration version. Owner and expiry never change evaluation.

## Publish a customer release

Flags are scoped to a project and named environment. A project app's `default`
deployment scope reads `production`; other scopes must match the environment
registry. Standalone apps and PR preview apps do not currently read runtime flags.
Use existing platform tenant UUIDs, not external customer references.

A protected environment other than production takes flag changes, rollbacks and
rollout promotions from an approved project plan or promotion, like its other
configuration; a direct change answers `409`. Production keeps direct edits.

```bash
gregale flags get --project exports --environment production
```

Save an update using the version returned by that read:

```json
{
  "expected_version": 0,
  "config": {
    "groups": { "internal": ["11111111-1111-4111-8111-111111111111"] },
    "flags": [{
      "key": "new-export",
      "description": "Enable the new export implementation",
      "enabled": true,
      "default": false,
      "rules": [{
        "id": "selected-customers",
        "customers": [
          "11111111-1111-4111-8111-111111111111",
          "22222222-2222-4222-8222-222222222222",
          "33333333-3333-4333-8333-333333333333"
        ],
        "value": true
      }]
    }]
  }
}
```

```bash
gregale flags apply --project exports --file flags.json
gregale flags inspect --project exports --key new-export --customer-id 11111111-1111-4111-8111-111111111111
```

The update replaces the full configuration atomically. Omitted flags are removed.
The server supplies immutable allocation seeds. An outdated `expected_version`
returns `409 flags_version_conflict`; read again before editing. Management uses
account-scoped read/deploy-write credentials and the existing MFA policy.

Rules run in order and the first match wins. Customer lists, group membership and
percentage eligibility combine with AND. A rollout of `1000` means 10% of eligible
customers; `10000` means all. Allocation hashes the stable environment flag seed,
flag key and verified customer ID. Increasing the percentage retains the original
cohort. Variant assignment uses a separate stable hash, so rollout eligibility does
not skew the configured variant weights. Everyone within the same customer receives
the same allocation unless a rule opts into subject allocation. Anonymous requests
never match customer rules. Flags with no matching rule use `default`.

## Target authenticated application users

Use `subjects` for a bounded list of opaque, stable user IDs supplied by the
application after it authenticates the user. Every subject rule must also have a
`customers` or `group` constraint, so a user ID is always scoped to a verified
Gregale tenant. Do not pass an unverified request parameter, email address, or
display name as the subject. Subject IDs are used to evaluate the rule, but the raw
ID is not stored in request evidence or decision propagation.

```json
{
  "key": "new-export",
  "enabled": true,
  "default": false,
  "rules": [{
    "id": "named-pilot-users",
    "customers": ["11111111-1111-4111-8111-111111111111"],
    "subjects": ["user-17", "user-42"],
    "value": true
  }]
}
```

Set `rollout_unit` to `subject` when percentage eligibility should be stable per
authenticated user rather than shared by all users in a customer. The rule still
needs a tenant constraint. The rollout and variant buckets then include both the
verified customer and subject ID; the same user keeps its cohort across requests,
while another customer with the same local user ID receives an independent cohort.

```json
{
  "key": "export-pipeline",
  "type": "variant",
  "enabled": true,
  "default": "legacy",
  "variants": [
    { "key": "legacy", "weight": 5000 },
    { "key": "new", "weight": 5000 }
  ],
  "rules": [{
    "id": "customer-user-rollout",
    "group": "pilot-customers",
    "rollout": 1000,
    "rollout_unit": "subject"
  }]
}
```

Subject-targeted configurations require SDK capability `subject-targeting-v1`.
The runtime endpoint returns `426 flags_sdk_capability_required` to older SDKs
instead of allowing them to evaluate a user-specific rule as a customer-wide rule.

Variant flags use a string `default`, a `variants` list whose integer weights total
10000 basis points, and targeting rules. A matching rule without `value` assigns a
weighted variant; a rule with a string `value` selects that named variant directly.
For example, target named customers to a 10% experiment while selecting their
variant consistently:

```json
{
  "key": "export-pipeline",
  "type": "variant",
  "enabled": true,
  "default": "legacy",
  "variants": [
    { "key": "legacy", "weight": 9000 },
    { "key": "new", "weight": 1000 }
  ],
  "rules": [{
    "id": "customer-rollout",
    "customers": ["11111111-1111-4111-8111-111111111111"],
    "rollout": 1000
  }]
}
```

## Target request attributes

A rule's `attributes` lists conditions that must all hold, alongside its other
constraints:

```json
{"id": "eu-pro", "attributes": [
  {"attribute": "gregale.country", "op": "in", "values": ["DE", "FR"]},
  {"attribute": "app.plan", "op": "in", "values": ["pro", "scale"]}
], "value": true}
```

- `gregale.country` (ISO 3166-1 alpha-2) and `gregale.ua_family` (`chrome`,
  `firefox`, `safari`, `bot`, ...) come from Gregale's gateway, which stamps
  them on each request. Clients cannot set them; an unknown value is absent.
- `app.<name>` attributes come from your application after it authenticates
  the user: `flags.withAttributes({plan: 'pro'}, fn)` in Node,
  `async with flags.for_attributes({"plan": "pro"})` in Python and
  `flags.WithAttributes(ctx, map[string]string{"plan": "pro"})` in Go. Names
  are lowercase identifiers, at most 16 per request; values are printable ASCII
  up to 128 bytes, and an invalid value is dropped.
- Operators are `in`, `not_in` and, for `app.*` only, `ends_with`. A condition
  on a missing attribute never matches, including `not_in`.
- A verified customer is still required: anonymous requests use the default.
- Attribute values are not stored in request evidence or propagated to other
  services.
- Runtime SDKs need capability `attribute-targeting-v1`; older SDKs receive
  `426 flags_sdk_capability_required` while an enabled flag uses attributes.

From the CLI, `--attr country=DE,FR` means `in`, `--attr 'plan!=free'` means
`not_in` and `--attr 'email$=@example.com'` means `ends_with`. `country` and
`ua_family` and `cohort` are the gateway's attributes; any other bare name is `app.<name>`:

```bash
gregale flag rule create new-export --id eu-pro --attr country=DE,FR --attr plan=pro --project exports
gregale flags inspect --project exports --key new-export --customer-id 11111111-1111-4111-8111-111111111111 --attr country=DE --attr plan=pro
```

## Check behavior in a Node application

Use `@gregale/sdk-node` in a managed Gregale VM. The workload needs the existing
loopback identity endpoint and operator-configured signing/trust described below.
Only accept these context headers on the app's Gregale gateway listener: the SDK
relies on the gateway replacing reserved headers. It does not authenticate an
arbitrary public request by itself.

```ts
import { createGregaleFetch, GregaleFlags, GREGALE_FLAG_EVIDENCE_HEADER } from '@gregale/sdk-node/preview';

const flags = new GregaleFlags({ apiURL: 'https://api.gregale.dev' });
await flags.start();
const serviceFetch = createGregaleFetch(fetch, { flags });

async function handle(request: Request): Promise<Response> {
  return flags.runRequest(request.headers, async () => {
    const decision = flags.boolean('new-export', false);
    flags.used('new-export'); // mark when the selected behavior is entered
    const response = decision.value
      ? await exportV2(request)
      : await exportV1(request);
    response.headers.set(GREGALE_FLAG_EVIDENCE_HEADER, flags.responseEvidence());
    return response;
  });
}
```

After authenticating the application user, scope its opaque ID around the checks
that depend on that user:

```ts
return flags.runRequest(request.headers, async () => {
  const actor = await authenticate(request);
  return flags.withSubject(actor.stableId, async () => {
    const decision = flags.boolean('new-export', false);
    return decision.value ? exportV2(request) : exportV1(request);
  });
});
```

For a multivariate behavior, call `variant()` and branch on the returned string:

```ts
const decision = flags.variant('export-pipeline', 'legacy');
flags.used('export-pipeline');
const response = decision.value === 'new'
  ? await exportWithNewPipeline(request)
  : await exportWithLegacyPipeline(request);
```

Set the evidence header before headers are committed, including error responses
when possible. For Node `http`, pass `req.headers` to `runRequest` and call
`res.setHeader(GREGALE_FLAG_EVIDENCE_HEADER, flags.responseEvidence())`. The SDK
records checks separately from explicit exposure (`used`). At most 32 flag keys
per request are recorded. Recording may be absent on an application crash, cached
response, edge response, overflow or disabled debugger; absence is not false.
`flags.close()` stops refresh on shutdown.

Refresh defaults to 15 seconds. Configuration freshness is bounded to at most
60 seconds, configurable downward through `maxStaleMs`. Each request pins one
version. After inactivity beyond that window, the SDK attempts a refresh with a
2-second default timeout before running the handler. Failure uses the explicit
fallback, with reason `configuration_stale`. Missing flags also use the fallback.
Startup during a configuration outage also permits explicit fallback behavior.
Disabling a defined flag uses its configured default; use `default: false` for an
off switch. Changes do not interrupt requests already using an earlier version.
Do not use this mechanism for an instantaneous security revocation; use `gregale flag kill set` for an emergency stop of application behavior.

### Use flags in a Python ASGI application

The Python runtime SDK evaluates the same immutable bundles and stable
allocations as the Node SDK. Start the client with Gregale's public API URL,
then wrap an ASGI app with the middleware. It reads customer and inherited
decision headers from Gregale's gateway, pins one bundle for the request, and
replaces the response evidence header before response headers are sent:

~~~python
from faas_sdk.preview import GregaleFlags, GregaleFlagsMiddleware

flags = GregaleFlags(api_url="https://api.gregale.dev")
async def on_startup():
    await flags.start()

async def on_shutdown():
    await flags.close()

# Wrap your existing ASGI application.
app = GregaleFlagsMiddleware(existing_app, flags)

async def handle_request():
    decision = flags.boolean("new-export", False)
    flags.used("new-export")
    if decision.value:
        ...
~~~

After authenticating the application user, use its opaque stable ID for
user-specific checks:

~~~python
async def handle_request(request):
    actor = await authenticate(request)
    async with flags.for_subject(actor.stable_id):
        decision = flags.boolean("new-export", False)
        return await export_v2(request) if decision.value else await export_v1(request)
~~~

Use the asynchronous HTTPX transport to forward only explicitly used decisions
to managed services. For queued work, put flags.propagation_header() in the
message's flag_context field, following the Node example below. Close the flags
client during ASGI shutdown with await flags.close(). By default, the client
uses the workload identity endpoint in FAAS_WORKLOAD_IDENTITY_ENDPOINT and the
gregale:flags audience.

These headers are trusted only when Gregale's gateway supplies them. Do not
use caller-provided customer or flag-context headers from arbitrary public
requests. Evidence is application-reported exposure; it is not authorization
or proof of a business side effect.

~~~python
import httpx
from faas_sdk.preview import AsyncGregaleFlagsTransport

service_client = httpx.AsyncClient(transport=AsyncGregaleFlagsTransport(flags))
~~~

### Use flags in a Go HTTP application

The Go runtime SDK evaluates the same immutable bundles and stable allocations
as the Node and Python clients. Start it with Gregale's HTTPS API URL and apply
its middleware to handlers served behind the Gregale gateway:

```go
flags, err := preview.NewGregaleFlags(preview.GregaleFlagsOptions{
    APIURL: "https://api.gregale.dev",
})
if err != nil {
    return err
}
if err := flags.Start(ctx); err != nil {
    return err
}
defer flags.Close()

app := flags.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    actor, err := authenticate(r)
    if err != nil {
        http.Error(w, "unauthorized", http.StatusUnauthorized)
        return
    }
    subjectContext, err := flags.WithSubject(r.Context(), actor.StableID)
    if err != nil {
        http.Error(w, "flag subject unavailable", http.StatusInternalServerError)
        return
    }
    r = r.WithContext(subjectContext)
    decision, err := flags.Boolean(r.Context(), "new-export", false)
    if err != nil {
        http.Error(w, "flag context unavailable", http.StatusInternalServerError)
        return
    }
    if err := flags.Used(r.Context(), "new-export"); err != nil {
        http.Error(w, "flag evidence unavailable", http.StatusInternalServerError)
        return
    }
    if decision.Value.(bool) {
        // Run the new implementation.
    } else {
        // Run the existing implementation.
    }
}))
```

Use `flags.Variant(ctx, key, fallback)` for named variants. The middleware pins
one configuration version per request and writes bounded decision evidence
before response headers are committed. `Start` performs a best-effort initial
refresh and refreshes every 15 seconds by default. Configuration is considered
fresh for at most 60 seconds; after that the middleware attempts a bounded
refresh and flag checks use the application's explicit fallback if it fails.
The loopback identity endpoint defaults to `FAAS_WORKLOAD_IDENTITY_ENDPOINT`.

Pass the request context through the standard `net/http` client to carry only
decisions marked used into managed service calls:

```go
serviceHTTP := &http.Client{Transport: preview.GregaleFlagsTransport{Flags: flags}}
request, err := http.NewRequestWithContext(r.Context(), http.MethodPost,
    "http://billing.svc.gregale/exports", body)
```

For queued work, set `flags.PropagationHeader(r.Context())` as the message's
`flag_context`. Caller-provided customer and flag-context headers are trusted
only when Gregale's gateway replaces them; do not use the middleware on arbitrary
public ingress. Decision evidence is application-reported behavior, not
authorization or proof of a business side effect.

### Carry decisions across managed service calls

Pass the `GregaleFlags` instance to `createGregaleFetch(fetch, { flags })` to
opt in. While `runRequest` is active, the helper attaches only decisions marked
with `used()` to calls targeting `*.svc.gregale`. The downstream SDK reuses a
matching inherited decision before checking its local configuration, so a
config refresh between service hops does not change the selected behavior.
The service proxy accepts a bounded envelope only after its existing caller
identity and binding checks, then stamps the customer context for the target.
Public ingress clears caller-supplied copies, malformed or duplicate envelopes
are dropped, and the helper removes the context header from external requests.

For example, call a managed service inside the request scope after marking the
selected behavior:

```ts
await flags.runRequest(request.headers, async () => {
  const decision = flags.variant('export-pipeline', 'legacy');
  flags.used('export-pipeline');
  return serviceFetch('http://billing.svc.gregale/exports', {
    method: 'POST',
    body: JSON.stringify({ implementation: decision.value }),
  });
});
```

Downstream evidence reports `source: "inherited"` and the originating app and
environment under `inherited_from`; its configuration version and rule ID refer
to the original decision. The envelope carries behavior context, not permission
or entitlement.

## Carry decisions into queued work

For work created while handling a customer request, include the SDK's bounded
context in `flag_context` when sending a queue message or app-inbox message:

```ts
await flags.runRequest(request.headers, async () => {
  const decision = flags.boolean('new-export', false);
  flags.used('new-export');
  const flagContext = flags.propagationHeader();
  await QueuesService.queueSend({
    slug: 'export-worker',
    requestBody: {
      payload: { exportId },
      ...(flagContext ? { flag_context: flagContext } : {}),
    },
  });
});
```

Gregale validates the bounded envelope, binds its customer to an active
platform tenant in the app's account, and stores it with the durable invocation.
Each attempt, including retries, reaches the app as a synthetic request carrying
the original `X-Faas-Platform-Tenant-Id` and `X-Faas-Flag-Context`. The worker
can use the regular `flags.runRequest(request.headers, handler)` wrapper. A
marked decision keeps its original value, rule, configuration version, app,
and environment; decisions that were not marked used are not carried forward.
Subsequent managed service calls can propagate the same context using
`createGregaleFetch`.

This applies to `queues/send` and the app inbox. Cron, delayed-task, and external
broker deliveries continue to evaluate flags using their existing request
identity unless the producer explicitly creates a new customer-bound invocation.

## Inspect decisions and request outcomes

```bash
gregale flags requests --project exports --key new-export --value true --used true --since 24h
gregale flags requests --project exports --key export-pipeline --variant new --used true --since 24h
gregale flags requests --project exports --key new-export --customer-id 11111111-1111-4111-8111-111111111111
gregale flags outcomes --project exports --key export-pipeline --since 24h
gregale flags outcomes --project exports --key new-export --customer-id 11111111-1111-4111-8111-111111111111
gregale flags outcomes --project exports --key new-export --rule-id selected-customers --config-version 4 --since 6h
gregale flags history --project exports
gregale flags inspect --project exports --key new-export --customer-id 11111111-1111-4111-8111-111111111111 --version 1
gregale flags inspect --project exports --key new-export --customer-id 11111111-1111-4111-8111-111111111111 --subject-id user-17
gregale flags inspect --project exports --key future-export --fallback-variant legacy
gregale flags rollback --project exports --expected-version 2 --version 1
```

An explanation contains value, matched rule ID, configuration version, source,
and the allocation bucket when a percentage or weighted variant rule matched.
Variant decisions identify their type and show a separate rollout eligibility
bucket when both percentage eligibility and weighted allocation apply. Inspector
subject simulations require a verified `--customer-id` and use the same
tenant-scoped allocation as the SDK. Simulations do not record exposure.
Historical versions remain available; rollback publishes a new version. History returns up to 100 versions; continue using
`--before-version` with the oldest version returned.

Request evidence reports gateway-attributed customer identity alongside status,
latency, deployment and trace identity. The flag decisions and `used` marker are
application-reported. Different decisions remain separate during aggregation;
`count` weights represented requests and latency may be conservatively quantized.
Request pages contain up to 100 rows and a `next_cursor`; pass it with the same
filters using `--cursor`. The debugger's plan entitlement, rate caps and retention
apply. Inspecting cohorts provides operational evidence, not a causal experiment.
Customer-level IDs and flag combinations are not added as Prometheus labels.

`flags outcomes` summarizes the selected flag's retained requests by boolean
value or named variant. Each cohort reports its request count, application-reported
`used` count, HTTP 5xx count and rate, and request-weighted p50/p95 latency over
the requested window. Latency percentiles use the conservative upper bounds of
the stored telemetry buckets. At most 100 groups are returned; if historical
configuration changes produce more groups, the response keeps the highest-volume
ones and sets `truncated: true`. Filter to one verified customer with
`--customer-id` when investigating a specific account. This view is read-only;
it does not advance a rollout or establish that a flag caused an outcome. When
configuration or targeting rules changed during the window, rows for the same
value may include decisions from more than one configuration version.

## Run an experiment

An experiment compares the variants of one weighted rule of a variant flag by
the share of customers who, after first seeing a variant, send a conversion
event. The flag's own allocation is the experiment: nothing else changes in
the configuration.

1. Make the behavior a variant flag with a weighted rule. An on/off test is a
   two-variant flag (`off`, `on`); a plain percentage rollout of a boolean flag
   cannot be an experiment, because the customers it leaves out cannot be told
   apart from customers the rule never targeted.

   ```bash
   gregale flag create checkout --variants legacy,new --project shop
   gregale flag rollout set checkout 100 --project shop   # everyone, split by variant weight
   ```

2. Record the conversion in the application, inside the request, for the
   request's verified customer. Return evidence on the response as usual
   (`flags.responseEvidence()`); exposures are read from it.

   ```ts
   await flags.runRequest(request.headers, async () => {
     const checkout = flags.variant('checkout', 'legacy');
     // ...
     if (orderPlaced) flags.track('order_completed', { value: totalCents });
   });
   ```

   Python: `flags.track("order_completed", value=total_cents)`. Go:
   `flags.Track(ctx, "order_completed", totalCents)`. Events are sent in
   batches every 5 s; `flush()` (Node, Python) or `Flush` (Go) sends the rest,
   and Python's `close()` and Go's `Close()` flush first. An event without a
   verified customer, with an invalid name (`^[a-z][a-z0-9_.-]{0,63}$`) or a
   non-integer value is dropped and counted in `droppedEvents()`.

3. Create and start the experiment, then read results whenever you like:

   ```bash
   gregale flag experiment create checkout-v2 --flag checkout --rule everyone-percent \
     --control legacy --metric order_completed --start --project shop
   gregale flag experiment result get checkout-v2 --project shop
   gregale flag experiment stop checkout-v2 --project shop
   ```

Results compare each variant with control: customers exposed, customers
converted, rate, absolute lift, a 95% confidence interval on the lift and a
p-value. They use a sequential test (mSPRT), so looking early and often does
not inflate false positives: a result marked `significant` stays trustworthy
however many times you checked. A variant reports `insufficient_data` until
each arm has 100 customers and 10 conversions. If the customers exposed do not
match the rule's weights (`sample_ratio_mismatch`), something other than the
flag decides who sees what, and the results are not trustworthy. Customers who
saw more than one variant (because the rule changed) are left out and counted.

Limits (`pkg/api/limits.go`): conversion events per month by plan, Free 0,
Hobby 100,000, Pro 1,000,000, Scale 10,000,000; events over the quota are
dropped and counted, never billed. Daily event rows are kept as long as the
plan keeps request telemetry (Hobby 3 days, Pro 7, Scale 14); each running
experiment keeps its own per-customer record for its whole lifetime, so
results cover experiments longer than that. 20 running and 200 total
experiments per environment, 100 event names per environment, one running
experiment per rule. Units are verified customers; per-end-user (subject)
experiments are not supported yet.

### Measure a value instead of a conversion

`--metric-kind mean` compares the mean event `value` per exposed customer,
such as revenue per customer from `track('order_completed', { value:
totalCents })`. A customer who sent nothing counts as 0. The 1% smallest and
largest customer values are capped to the 1st and 99th percentile before the
means are compared, so one very large order cannot decide the result. Values
from the last two UTC days are read live; older days are folded into each
customer's record, so results outlive the daily event rows.

```bash
gregale flag experiment create checkout-revenue --flag checkout --rule everyone-percent \
  --control legacy --metric order_completed --metric-kind mean --start --project shop
```

### Measure a funnel or retention

With product analytics on ([Analyze product events](#analyze-product-events)),
an experiment can measure what customers do over hours or weeks
([ADR-20261119102741](adr/20261119102741-experiment-funnel-retention-metrics-cuped-cohorts.md)):

- `--metric-kind funnel` with 2 to 6 `--step` events and a `--window` (1h to
  720h): a customer converts when it sends the first step after its first
  exposure and each later step after the previous one, all within the window.
- `--metric-kind retention` with `--return-event`, `--period day|week` and
  `--period-number N` (1 to 12): a customer converts when it sends the return
  event in period N after its first exposure.

```bash
gregale flag experiment create onboarding --flag checkout --rule everyone-percent --control legacy \
  --metric-kind funnel --step signup --step add_to_cart --step order_completed --window 72h --start --project shop
gregale flag experiment create week-one --flag checkout --rule everyone-percent --control legacy \
  --metric-kind retention --return-event order_completed --period week --period-number 1 --start --project shop
```

A step or return event may carry a property filter in the API
(`{"event": "order_completed", "where": {"plan": "pro"}}`). A customer is
counted only once its window, or N+1 periods, after its first exposure has
passed, so results appear that long after the experiment starts and a counted
customer's outcome never changes.

### Variance reduction (CUPED)

Results are adjusted for each customer's behavior in the 14 days before the
experiment started: the sum of the metric event's values for `mean`, otherwise
whether it sent the metric event (the first funnel step, the return event).
Customers with steady habits then add less noise, so significant results
usually come sooner. `result get` shows the adjusted comparison, which drives
status and the recommendation, beside the unadjusted one, with the variance
reduction. The adjustment needs product analytics; without it, or when no
customer has pre-period events, results are unadjusted and say why. It is not
applied to subject, ratio or percentile experiments (`not_supported`). Turn it
off with `--no-cuped` on create.

### Guardrails

Every experiment watches two guardrails from request telemetry, with no
instrumentation:

| Guardrail | Breached when the variant, compared with control, has | Default tolerance |
|---|---|---|
| `server_error_rate` | a higher share of 5xx responses (platform refusals excluded) | +0.5 percentage points |
| `p95_latency` | a higher share of requests slower than control's p95 latency | +2 percentage points |

Only requests that evaluated the experiment's rule count, and customers, not
requests, are the units. A guardrail breaches when a 99% always-valid
confidence interval on the difference lies wholly above the tolerance, after
each arm has 100 customers and 1,000 requests. Control's p95 is fixed as the
latency threshold once control has 1,000 requests; results also report each
variant's p95.

On a breach Gregale publishes a new flag configuration in which the rule
serves control to every customer it matches, then stops the experiment with
`stop_reason: guardrail_breach` and records the breach. Restore the previous
configuration version to undo it (`gregale flag history`, then
`gregale flag apply`). Change the tolerances, or turn guardrails off, when you
create the experiment:

```bash
gregale flag experiment create checkout-v2 ... --max-error-increase 0.01 --max-slow-increase 0.05
gregale flag experiment create checkout-v2 ... --no-guardrails
```

### Recommendations and shipping

`gregale flag experiment result get` ends with a recommendation and the
command that carries it out: `ship_variant` (a variant is significantly better
and breaches no guardrail), `keep_control` (every significant variant is
worse), `keep_running`, `fix_allocation` (sample ratio mismatch), `decide`
(stopped with no significant difference) or `rolled_back`. Gregale never ships
a variant on its own. To ship:

```bash
gregale flag experiment promote checkout-v2 new --project shop
```

`promote` publishes the rule serving that variant to every customer it matches
and stops the experiment. MCP agents read the same results and recommendation
with `gregale_list_flag_experiments` and
`gregale_get_flag_experiment_results` (release toolset).

### Experiment on end users

By default the units are customers. When each customer has many end users, an
experiment can count them instead
([ADR-20261119173702](adr/20261119173702-experiment-subject-units-layers-bandits-metric-rollouts.md)).
Give the rule `rollout_unit: subject` (the SDK then splits by customer and
subject), turn product analytics on, and create the experiment with
`--unit subject`:

```bash
gregale flag experiment create checkout-users --flag checkout --rule by-user --control legacy \
  --metric order_completed --unit subject --start --project shop
```

The SDK reports each end user's first exposure to the rule, at most hourly,
and only a hash of the subject is kept. Conversions are the subject's
`track()` events (send them with the subject, see
[Send properties and a subject](#send-properties-and-a-subject)). Subject
experiments take `conversion`, `funnel` or `retention` metrics and have no
guardrails: request telemetry is per customer. SDKs need capability
`subject-exposures-v1`; older SDKs get `426` while the experiment runs.

### Run experiments side by side

Experiments on different flags can share customers, so one can skew another.
Put experiments that must not overlap in one layer: each holds a slice of the
layer's customers while it runs, and the slices never overlap.

```bash
gregale flag experiment layer create checkout --project shop
gregale flag experiment create checkout-v2 --flag checkout --rule everyone-percent --control legacy \
  --metric order_completed --layer checkout --traffic 50 --start --project shop
gregale flag experiment layer list --project shop
```

`--traffic` is the percent of the layer the experiment takes; the start fails
with `409 flag_experiment_layer_full` when the layer has no free slice that
large. A customer outside the slice skips the rule. An environment has at
most 10 layers; a layer in use cannot be deleted.

The holdout keeps a share of customers (up to 20%) out of every experiment:
they see each experiment's control. Its report shows the cumulative effect of
everything you shipped while it ran:

```bash
gregale flag experiment holdout set 5 --project shop
gregale flag experiment holdout report --event order_completed --project shop
```

The holdout cannot change while an experiment runs. Layers and the holdout
need SDK capability `experiment-layers-v1`.

### Ratio and percentile metrics

- `--metric-kind ratio --denominator-event EVENT` compares the sum of the
  metric event's values over the sum of the denominator event's values, such
  as revenue per order. It uses the same always-valid test as other metrics.
- `--metric-kind percentile --percentile 95` compares the 95th percentile of
  each customer's summed metric values, with a bootstrap interval. It has no
  sequential guarantee: results say `fixed_horizon`, so read them once, at
  the end you planned.

### Split results by a cohort

```bash
gregale flag experiment result get checkout-v2 --segment pro-buyers --project shop
```

This adds the results of the cohort's members and of everyone else, with
p-values adjusted for the number of comparisons (Benjamini–Hochberg). Use
segments to explore; the recommendation ignores them. Binary metrics of
customer experiments only.

### Let a bandit allocate traffic

With `--allocation bandit`, Gregale moves traffic toward the variant that
converts best while the experiment runs (Thompson sampling), keeping at least
10% on each variant. Weights move at most hourly, once every variant has 100
customers. Results show each variant's probability of being best and its
expected loss instead of p-values, and recommend shipping a variant once it
is best with 95% probability at an expected loss under 0.1 points. The rule
must be the flag's only weighted rule, and the metric a conversion, funnel or
retention.

## Analyze product events

The same `track()` events also feed product analytics: event series, funnels
and retention, each of which can be split by the variant a customer saw
([ADR-20261117084505](adr/20261117084505-product-analytics-events-funnels-retention.md)).
It is behind the `product_analytics` platform flag and needs a plan with
analytics retention:

| Plan | Raw event retention |
|---|---|
| Free | off |
| Hobby | 30 days |
| Pro | 90 days |
| Scale | 365 days |

Analytics events share the monthly event quota with experiments; an event is
counted once.

### Send properties and a subject

An event may carry up to 10 string properties (keys
`^[a-z][a-z0-9_]{0,63}$`, values up to 256 bytes; at most 50 distinct keys per
environment) and an end-user subject. The subject defaults to the one the
request's flags were evaluated for; Gregale stores only a hash of it. The SDK
also attaches every flag decision the request made before the call, so any
event can be split by variant without extra code.

```ts
flags.track('order_completed', { value: 4200, properties: { plan: 'pro' } });
```

```python
flags.track("order_completed", 4200, properties={"plan": "pro"})
```

```go
flags.TrackWith(ctx, "order_completed", preview.TrackOptions{Value: 4200, Properties: map[string]string{"plan": "pro"}})
```

SDKs that send these fields advertise the `track-v2` capability; older SDKs
keep working without them.

### Query

```bash
gregale insight event list --project shop --by flag:checkout --from 14d order_completed
gregale insight funnel get --project shop --window 72h --by flag:checkout signup add_to_cart order_completed
gregale insight retention get --project shop --period week --periods 4 signup order_completed
gregale insight request list --project shop --flag checkout
```

- `--unit subject` counts end users instead of customers and ignores events
  without a subject.
- `--by flag:KEY` uses the flag value carried by the unit's earliest event in
  the range with that flag (for a funnel, its first-step event); `(none)`
  means the unit never sent an event with that flag. `--by property:KEY`
  splits by a property. At most 20 values are listed; the rest are `(other)`.
- Funnels and retention read at most 200,000 units and say `truncated` when
  they stop; narrow the range or filter.
- Ranges stop at the plan's retention and 90 days. `insight request list`
  reads request telemetry and follows its shorter retention.

The API routes are under
`/v1/projects/{slug}/environments/{environment}/insights` (`GET events`,
`POST funnel`, `POST retention`, `GET requests`; all read-only). MCP agents use
`gregale_get_insight_events`, `gregale_get_insight_funnel`,
`gregale_get_insight_retention` and `gregale_get_insight_requests`
(diagnose toolset).

### Target a cohort

A cohort is a saved query whose customers a flag rule can target:

| Kind | Members |
|---|---|
| `did` | sent the event (optional property filter) at least `--min-count` times in the last `--days` days |
| `did_not` | were active in the last 30 days but did not send the event in the last `--days` days |
| `funnel` | completed the `--step` events within `--window`, starting in the last `--days` days |

```bash
gregale insight cohort create power-buyers --kind did --event order_completed --min-count 3 --days 30 --project shop
gregale insight cohort list --project shop
gregale flag rule create power-tools --id power --attr cohort=power-buyers --project shop
```

Gregale recomputes a cohort when it is created, hourly, and on
`gregale insight cohort refresh`. A cohort keeps at most Hobby 1,000 / Pro
10,000 / Scale 100,000 customers (lowest customer IDs first; `truncated`
says when more matched), and an environment has at most 20 cohorts. Members
are verified customers; subjects cannot be cohort members.

The gateway stamps each request's cohorts as the `gregale.cohort` attribute,
so rules use `in` and `not_in` on cohort keys like any other gateway
attribute; `ends_with` is refused. Membership seen by a request can lag a
refresh by up to a minute, and if the lookup fails the attribute is missing,
so neither `in` nor `not_in` matches. Runtime SDKs need capability
`cohort-targeting-v1`. The API routes are under
`/v1/projects/{slug}/environments/{environment}/cohorts`; MCP agents list
cohorts with `gregale_list_insight_cohorts` (diagnose toolset).

### Save, share and export an insight

A saved insight is a named events, funnel or retention query over the last
`--days` days (default 7, 28 for retention). Anyone who can read the project
lists and runs it; saving, replacing and deleting one needs deploy rights.
An operator releases saved insights with the `saved_insights` platform flag
(on top of `product_analytics`).

```bash
gregale insight saved create checkout-conv --kind funnel --step signup --step order_completed --days 14 --project shop
gregale insight saved list --project shop
gregale insight saved run checkout-conv --project shop
gregale insight saved run checkout-conv --format csv --project shop > checkout-conv.csv
```

Each saved insight has one value: an events total (`--measure value` sums
event values instead of counting events), a funnel's overall conversion, or
the share of units that came back in the first retention period. Funnel and
retention values are fractions (0.25 is 25%).

Send the value to [notification channels](alerts.md) with a digest, an alert
or both (up to 5 channels):

```bash
gregale insight saved create orders --kind events --event order_completed --days 1 \
  --digest daily --alert lt:100 --channel CHANNEL_ID --project shop
```

- Digests go out at 08:00 UTC, `weekly` ones on Mondays. PagerDuty channels
  get no digests.
- An alert (`gt`, `gte`, `lt` or `lte` a threshold) is checked every 15
  minutes. It notifies once when the value crosses the threshold and once
  when it comes back. If the query fails, nothing is sent, the state is kept,
  `last_error` says why and the insight is retried in 15 minutes; a due
  digest goes out once a run succeeds. A funnel without entrants or
  retention before its first period ends has no value and does not alert.
- Replace a saved insight with `gregale insight saved create KEY --replace ...`;
  changing its query, range or alert resets the alert state.

An environment holds at most 50 saved insights. The API routes are under
`/v1/projects/{slug}/environments/{environment}/insights/saved`
(`GET /{key}/run?format=csv` exports). MCP agents use
`gregale_list_saved_insights` and `gregale_run_saved_insight` (diagnose
toolset).

## Promote a guarded rollout stage

Attach a progression plan to a boolean rule that selects `true`. The rule's
`rollout` must equal its current stage; stages use basis points, increase
strictly, and end at 10000 (100%). The rule's stable allocation seed keeps
customers in the same cohort as the percentage grows.

```json
{
  "key": "new-export",
  "enabled": true,
  "default": false,
  "rules": [{
    "id": "selected-customers",
    "rollout": 100,
    "value": true,
    "progression": {
      "stages": [100, 500, 2500, 10000],
      "current_stage": 0,
      "minimum_used_requests": 25,
      "maximum_http_5xx_rate_basis_points": 200,
      "maximum_p95_latency_ms": 800,
      "window_seconds": 1800
    }
  }]
}
```

After publishing the plan and observing its first stage, request one promotion:

```bash
gregale flags promote --project exports --key new-export \
  --rule-id selected-customers --expected-version 4
```

Promotion reads retained evidence matched to that exact rule, active
configuration version, and configured window. It advances one stage only when
application-reported `used` requests meet the minimum and both the 5xx-rate and
conservative p95 latency limits pass.
A `held` response explains the failed gate and leaves configuration unchanged;
refresh the flag version before trying again. `complete` means the final stage is
already active. Promotion requires debugger telemetry entitlement and retention
at least as long as the configured window. These thresholds are operational
signals, not proof that the flag caused an outcome; promotion is always an
explicit operator action when `auto_advance` is omitted or false.

Set `auto_advance: true` on the progression plan to opt this rule into
server-managed promotion. The platform waits for a complete configured window
after the active configuration version was published, checks evidence from
that exact version and rule, and advances at most one stage when all gates
pass. Insufficient or unhealthy evidence holds the stage; the reconciler logs
the reason and retries on a later pass. Automatic promotion respects debugger
telemetry entitlement and retention, and uses a version-checked write so a
concurrent operator edit wins. Rules without explicit opt-in remain manual.

A progression plan can also watch a conversion event. Add `metric` to it:

```json
"metric": { "event": "order_completed", "max_decrease": 0.01, "min_units": 100 }
```

After each stage's window, customers the stage serves are compared with every
other customer that evaluated the flag, on the share that sent the event
after their first evaluation. When their rate is clearly more than
`max_decrease` lower, Gregale sets the rule's rollout to 0, removes the plan
and audits `flags.rollout_auto_rolled_back`. The stage advances only when
both groups have `min_units` customers and the rate is not clearly worse.

The SDK does not add flags to cache keys automatically. Avoid shared caches for
customer-dependent behavior unless their keys include the relevant customer and
behavior identity and you invalidate them when configuration changes. Cache hits
do not represent a fresh SDK evaluation or exposure.

## Operator trust and recovery

Configure the existing vmmd workload identity signer. Publish its public JWKS
(including retiring public keys during rotation) to a local apid file. Never copy
the private signing key into apid or the app image.

```toml
# apid.toml
flags_enabled = true
flags_workload_jwks_path = "/etc/faas/workload-identity-public.jwks.json"
flags_workload_issuer = "https://identity.gregale.dev"
```

Release Flags with the `customer_flags` platform flag
([platform flags](ops/platform-flags.md), ADR-1700): `gregalectl flags enable
--key customer_flags` and then `allow` one account at a time, or a rollout.
It applies without a restart. `flags_enabled` / `FAAS_FLAGS_ENABLED=1`
(disabled by default) is only its boot fallback, releasing Flags to every
account until an operator writes the flag. Environment overrides for workload
trust are `FAAS_FLAGS_WORKLOAD_JWKS_PATH` and
`FAAS_FLAGS_WORKLOAD_ISSUER`. With neither set, Flags uses the cluster
workload trust Ansible installs (`workload_jwks_path`, ADR-1141; see
[workload identity](workload-identity.md)). Missing trust leaves the runtime endpoint unavailable;
malformed configured trust fails startup. Restart apid after replacing the public
JWKS file. Tokens must use the `gregale:flags` audience, last at most five minutes,
and identify an active account and live app instance. Runtime clients cannot
select another project or environment. Keys issued for federation are rejected.

Recover a bad release by publishing or rolling back a configuration with the
current expected version. During an API outage the SDK uses last known configuration
within the freshness window, then application defaults. Flags share existing app
and debugger operations; no new scheduler or privileged daemon is required.

## Acceptance and current boundaries

```bash
DATABASE_URL=postgres://... make test-flags
```

This gate builds the Node SDK and runs evaluator, SDK, gateway, API and real
PostgreSQL coverage. The integrated scenario targets three of four customers,
checks forged context replacement, filters their errors, and observes disablement
in the same Node process after simulated inactivity. This is application and
configuration acceptance; it does not exercise a native VM restore.

The `e2e-native.yml` hardware gate also runs
`TestFeatureFlagsNativeParkRestoreMetal` as a required test. It deploys a real
Node app, changes its configuration while parked, waits past the SDK freshness
bound, then verifies that the restored VM refreshes configuration and persists
the new decision with verified customer attribution. A skip fails the native
gate. The full runner needs the dedicated KVM host and fixtures described in
[`docs/ops/e2e-native-ci.md`](ops/e2e-native-ci.md); the focused target is
`make test-flags-metal` on a suitably provisioned host. Flags remain internal
until this native qualification has a green run.

Safeguards from `pkg/api/limits.go`: 100 flags and 100 groups per environment,
32 rules and 16 variants per flag, 1000 customer IDs per list, 256 KiB configuration, 32 evidence
entries and 16 KiB decoded evidence per request.

Synchronous decision inheritance is available for managed service calls when
the Node SDK fetch helper is explicitly configured. Producers can also carry
marked decisions into `queues/send` and the app inbox. Arbitrary user attributes
and propagation through cron, delayed tasks, and external broker deliveries
remain future work; those workloads otherwise evaluate flags using their own
environment and verified customer identity. Automatic advancement runs as a
bounded apid reconciliation loop and remains opt-in per rule.
