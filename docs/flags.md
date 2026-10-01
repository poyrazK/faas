# Gregale Flags

Gregale Flags releases already deployed application behavior to selected platform
customers without a new deployment. The initial operator qualification supports boolean flags,
named string variants, explicit customer lists, owner-managed customer groups,
sticky weighted allocation and percentage rollout, versioned configuration,
rollback, a decision inspector, a Node SDK and request cohort evidence. Business
logic must explicitly check a flag. Flags do not grant
permissions or paid entitlements and are independent from deployment traffic splits.

## Publish a customer release

Flags are scoped to a project and named environment. A project app's `default`
deployment scope reads `production`; other scopes must match the environment
registry. Standalone apps and PR preview apps do not currently read runtime flags.
Use existing platform tenant UUIDs, not external customer references.

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
the same allocation. Anonymous requests never match customer rules. Flags with no
matching rule use `default`.

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

## Check behavior in a Node application

Use `@gregale/sdk-node` in a managed Gregale VM. The workload needs the existing
loopback identity endpoint and operator-configured signing/trust described below.
Only accept these context headers on the app's Gregale gateway listener: the SDK
relies on the gateway replacing reserved headers. It does not authenticate an
arbitrary public request by itself.

```ts
import { createGregaleFetch, GregaleFlags, GREGALE_FLAG_EVIDENCE_HEADER } from '@gregale/sdk-node';

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
Do not use this mechanism for an instantaneous security revocation.

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
gregale flags inspect --project exports --key future-export --fallback-variant legacy
gregale flags rollback --project exports --expected-version 2 --version 1
```

An explanation contains value, matched rule ID, configuration version, source,
and the allocation bucket when a percentage or weighted variant rule matched.
Variant decisions identify their type and show a separate rollout eligibility
bucket when both percentage eligibility and weighted allocation apply. Inspector
simulations do not record exposure. Historical versions remain available; rollback publishes
a new version. History returns up to 100 versions; continue using
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
explicit operator action.

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

Enable the qualification with `FAAS_FLAGS_ENABLED=1` (disabled by default).
Environment overrides are `FAAS_FLAGS_WORKLOAD_JWKS_PATH` and
`FAAS_FLAGS_WORKLOAD_ISSUER`. Missing trust leaves the runtime endpoint unavailable;
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
marked decisions into `queues/send` and the app inbox. Arbitrary user attributes,
automatic stage advancement, and propagation through cron, delayed tasks, and
external broker deliveries remain future work; those workloads otherwise
evaluate flags using their own environment and verified customer identity.
