# Internal services

Apps in the same account call one another by name over a private network. This page covers calling a service, deadlines and retries, which callers a service accepts, and how a service verifies who is calling.

## Call another app by name


Apps in the same account reach one another by name, on every plan. There is no
VPC, subnet, security group, internal load balancer, service registry, or DNS
record to configure:

```text
public-api  ──►  auth
            ├─►  billing
            └─►  recommendation
```

New HTTP bindings use port **10081**, which works with standard Node `fetch`.
Port 10080 remains available for previously deployed callers; redeploying a
caller refreshes its generated binding URLs. Both ports use the same caller
identity, account, binding, and target authorization checks. HTTPS bindings
remain an explicit transport choice.

Each dependency is an ordinary app. From `public-api`, call them as:

```text
http://auth.svc.gregale:10081
http://billing.svc.gregale:10081
http://recommendation.svc.gregale:10081
```

The name is the project workload name (or the app slug for a standalone app).
Declare the edges with `depends_on` in your Compose file (`compose.yaml` or
`docker-compose.yml`; `gregale.yaml` has no `services:` key, see the `project:`
block below) and Gregale injects the URLs for you, so nothing hard-codes a
hostname. A service that only other services call declares its port with
`expose:`; one without `ports:` or `expose:` deploys as a worker that serves no
HTTP:

```yaml
services:
  public-api:
    build: ./public-api
    depends_on: [auth, billing, recommendation]
  auth:
    build: ./auth
  billing:
    build: ./billing
    x-gregale-allow-callers: [public-api]
  recommendation:
    build: ./recommendation
```

`public-api` then starts with `GREGALE_SERVICE_AUTH_URL`,
`GREGALE_SERVICE_BILLING_URL`, and `GREGALE_SERVICE_RECOMMENDATION_URL` in its
environment. The dependency graph is validated before anything deploys —
unknown names, self-edges, and ambiguous names are rejected.

By default, `GREGALE_SERVICE_BILLING_URL` remains the legacy HTTP URL, and
`GREGALE_SERVICE_BILLING_HTTPS_URL=https://billing.internal` is available as
an explicit HTTPS alias. `GREGALE_SERVICE_BILLING_HOST=billing.svc.gregale`
is the bare host for non-HTTP clients (see
[Private TCP between services](#private-tcp-between-services)). A caller can opt into HTTPS-first transport for all
of its declared service bindings:

```yaml
services:
  public-api:
    build: ./public-api
    depends_on: [auth, billing, recommendation]
    x-gregale-service-transport: https
```

With this setting, each canonical `_URL` uses `https://<service>.internal`;
the `_HTTPS_URL` companion remains available with the same value. Plain HTTP
calls from that caller are rejected by the service proxy, so client retries do
not silently downgrade. The transport needs private HTTPS and workload CA
trust: where private service HTTPS is not enabled for your account yet,
Gregale refuses the setting and says so. Omitted transport keeps existing
workloads' stored choice and defaults new workloads to HTTP; set the extension
to `http` to explicitly return to the legacy endpoint. The app API exposes the
same choice as `service_binding_transport` for standalone apps. `gregale
bindings <app>` reports the effective transport.

The same declared edges are exposed as service bindings by the app API and by
`gregale app binding public-api`, alongside database, object-storage, queue,
and outbound integration bindings. New project workloads use the `project`
caller policy:
they may call every service in their own project without listing it. A
project service's `depends_on` bindings name only its own project's services:
a bound name never resolves to an app outside the project, and a call to a
service outside the project gets 403 unless the caller sets
`x-gregale-service-policy: account`. A target's `x-gregale-allow-callers` list
still applies. Existing apps
retain their persisted policy on reapply; set `x-gregale-service-policy:
project` on a service to move it over, or `declared` to require `depends_on`
for every call. A top-level `x-gregale-service-policy` sets the policy of
every service that sets none, which moves a whole project over in one change:

```yaml
x-gregale-service-policy: project
services:
  web:
    build: ./web
  billing:
    build: ./billing
```

A repository without Compose declares the same policy, per-service
visibility and `depends_on` in the root `gregale.yaml` `project:` block (formerly `.gregale/project.yaml`);
a Compose declaration wins where both set a policy.

An undeclared sibling has no injected URL; call it at
`http://<name>.internal:10081` or
`http://<app-slug>.svc.gregale:10081`. For an intentional same-account escape
hatch, set `x-gregale-service-policy: account` on the caller. Under the
`declared` policy the CLI reports service bindings as `enforced`; under
`project` and `account` it reports them as `declared`.

## Deadlines, retries and call scopes

Each declared outbound dependency can carry a request deadline and retry
policy. For a project workload, put it beside `depends_on`:

```yaml
services:
  public-api:
    build: ./public-api
    depends_on: [billing]
    x-gregale-service-reliability:
      billing:
        timeout_ms: 1500
        max_attempts: 2
        min_remaining_ms: 200
        retry_budget_percent: 10
```

The timeout covers routing, a cold wake, forwarding, and any retry after caller
authorization. An earlier caller deadline takes precedence. `max_attempts: 1`
disables proxy replay for that dependency; omitted settings keep platform
defaults. Retries still require a safe method or an explicit
`allow_non_idempotent: true`, a replayable body, enough remaining time, and an
available aggregate retry token. Only transport-stale attempts are retried.
Policies can name only services in `depends_on`; on reapply, an omitted extension
retains stored policies and removing a binding removes its policy. A WebSocket
handshake uses the deadline, but an established session is not cut off by it.

The target can independently restrict who calls it with
`x-gregale-allow-callers`. In the example, `billing` admits `public-api` but
not other same-account apps, even if they declare a dependency on `billing`.
An omitted list preserves legacy same-account reachability; `[]` denies every
internal caller. The list accepts at most 100 logical app slugs, not generated
PR preview names. In a project, a name in the list (or in
`x-gregale-allow-call-scopes`) is a Compose service of the target's project,
even when that service has a project-prefixed slug. An
app outside the project matches by its slug only when no service of the
target's project has that name. A deleted service keeps its name until it is
purged (7 days after the delete, while it can still be restored): its siblings'
`depends_on` names and caller lists neither resolve to nor admit an app outside
the project that holds the name as a slug. After the purge, a sibling's
`depends_on` binding still resolves only inside the project. Calls to the
service fail as not found until it is restored or a project apply removes the
dependency. This target check runs before routing or waking `billing`. Preview callers
must also pass the existing project and target preview policies.

For least-privilege access, a target can grant each caller only specific HTTP
methods and path prefixes:

```yaml
services:
  billing:
    build: ./billing
    x-gregale-allow-call-scopes:
      public-api:
        methods: [GET, POST]
        path_prefixes: [/v1/invoices, /v1/checkout]
```

When `x-gregale-allow-call-scopes` is present, it also acts as a caller
allowlist: callers missing from the map are denied. If
`x-gregale-allow-callers` is also set, both policies must allow the caller.
Configured methods are normalized to uppercase and matched against the request
method exactly; `*` explicitly permits every method. A path prefix matches the
exact path and its slash-delimited descendants, so
`/v1/invoices` allows `/v1/invoices/42` but not `/v1/invoices-archive`. Query
strings do not affect the path check. Ambiguous paths such as dot-segments or
duplicate separators fail closed, and the check runs before endpoint lookup or
wake. The platform-owned `bindings verify` probe remains available because it
does not invoke an application route.

Standalone apps use the same target-side policy in `allowed_service_call_scopes`
on `POST /v1/apps` or `PATCH /v1/apps/<slug>`. PATCH omission keeps the current
map, `null` clears it, and `{}` denies every caller. Project-managed apps must
set `x-gregale-allow-call-scopes` in source; they reject this PATCH just like
the existing caller allowlist.

For an app created outside a project, use the
[service binding CLI](service-binding-configuration.md) to preview and apply
these fields from a JSON policy file. The app API is also available: include
`"allowed_service_callers": ["frontend"]` in `POST /v1/apps`, or PATCH
`/v1/apps/customer-billing` with that field to replace the list. PATCH with `[]` denies
all internal callers; PATCH with `null` restores same-account access. Omission
leaves the current policy unchanged. Project-managed and preview apps cannot
change this field through PATCH; edit `x-gregale-allow-callers` in the source.

Standalone callers can declare outbound targets without a Compose project:

```json
{
  "service_binding_targets": ["billing", "identity", "email"],
  "service_binding_policy": "declared",
  "service_reliability": {
    "billing": {"timeout_ms": 1500, "max_attempts": 1}
  }
}
```

Send these fields on `POST /v1/apps` or `PATCH /v1/apps/frontend`. Gregale
injects `GREGALE_SERVICE_BILLING_URL`, `GREGALE_SERVICE_IDENTITY_URL`, and
`GREGALE_SERVICE_EMAIL_URL` into `frontend`; `GET /v1/apps/frontend` reports
the generated `service_bindings`. Under `declared`, the gateway rejects any
other internal target before waking it. Existing standalone apps stay on
`account` reachability unless they opt in; adding targets alone is discovery
only. A PATCH with `service_binding_targets: []` clears the bindings (and
denies every internal target if the policy is `declared`); setting
`service_binding_policy: "account"` restores same-account access. Target names
may be forward references, but a call cannot succeed until the target app
exists. Authorization changes take effect at the gateway immediately; new URL
environment variables appear when the caller next starts or redeploys, while
already-running instances retain their current environment. The generated
`GREGALE_SERVICE_*_URL` namespace is platform-owned.
For `service_reliability`, PATCH omission retains the map, `null` or `{}`
clears it, and an object replaces it. A policy must name one of the app's
declared `service_binding_targets`. Removing a binding also removes its
stored policy when the reliability map is omitted from the same PATCH.
Project-managed and preview apps reject changes to these fields on PATCH; edit the
project source instead. Binding declarations do not expose services publicly.

## The private `.internal` alias

Bound services can also be called through the short private alias
`http://billing.internal:10081`. Gregale's node-local DNS answers that name
only for a VM whose app declares a `billing` binding, or, under the `project`
service policy, whose project has a `billing` service. A
direct request to the service proxy with `Host: billing.internal` gets the same
check, so bypassing DNS cannot grant access. The existing same-account,
target allowlist, and preview checks still run before any target is woken.
Unbound `.internal` names are passed to the configured upstream DNS resolver;
Gregale does not claim the customer's entire private namespace. The existing
`*.svc.gregale` endpoint and default generated URLs stay as they are; callers
may opt into HTTPS-first URLs. Private service HTTPS is not enabled for every
account yet, and `_HTTPS_URL` answers only where it is. Where it is, Gregale builds a per-workload bundle in `/tmp`, exports
`GREGALE_SERVICE_CA_BUNDLE`, and configures common TLS clients (`SSL_CERT_FILE`,
Python Requests, and curl) to trust the private endpoint when the image includes
a standard system CA bundle. Node receives the CA through its additive
`NODE_EXTRA_CA_CERTS` setting. No image or global OS trust store is changed.
Other TLS stacks can explicitly use `GREGALE_SERVICE_CA_BUNDLE` with their own
verifier. For example, `curl https://billing.internal/` and
`requests.get("https://billing.internal/")` verify TLS normally; verification
is never disabled. The raw CA also remains available at
`/etc/faas/service-proxy-ca.crt` for explicit client-specific selection.
The legacy generated binding URLs remain unchanged.

## How calls are authorized

Calls are authorized by the platform, not by your code. The caller is
identified from the network identity of the calling VM, so a guest cannot
claim to be another app, and the proxy only permits calls between apps in the
same account. Cross-account calls are refused.

## Preview-to-production service policy

A pull-request preview first resolves a service to a preview workload in the
same account, project, and PR. It never selects a preview from another PR,
project, or account. The target preview's protocol and WebSocket settings are
used exactly as they are on the public edge.

Preview provisioning creates the bound app and the transitive `depends_on`
workloads present at the PR head. When the bound app's service policy is
`project`, it also creates every other callable workload of the project:
workers, jobs and scheduled workloads join only as declared dependencies.
A dependency that is absent from that set can still resolve to the production
app, so the gateway applies the project's production-dependency policy.

A PR preview of an app bound without a project has no project policy. It
calls only the previews of its own pull request's preview set, named by their
production slugs, and every call to a production app is refused with
`preview_production_dependency_denied`.
`gregale dev` sessions keep account-wide resolution.

All projects use `preview_service_policy: deny` by default. A denied call returns
`403 application/problem+json` with code
`preview_production_dependency_denied` before the proxy discovers or wakes the
target. Legacy `allow_marked` rows are migrated to `deny`. To configure the
policy from a checkout, run:

```bash
gregale github setup public-api --preview-service-policy deny
```

Set `allow_marked` only when the production dependency is designed to receive
preview traffic. A preview of `public-api` calling `billing` then reaches
production `billing`, and any side effects are real.

In `allow_marked` mode, a production service can independently refuse preview
calls with `x-gregale-preview-calls: deny` on its Compose service:

```yaml
services:
  billing:
    build: ./billing
    x-gregale-preview-calls: deny
```

The gateway checks the target's policy before waking or forwarding it and
returns 403 to a preview caller. The target policy defaults to `allow`, so it
preserves existing behavior; the project-level `preview_service_policy` can
still deny all preview-to-production calls. The app API and scan plan show the
effective `preview_service_calls_policy`, and target-policy rejections count
under `gateway_service_call_total{outcome="preview_denied"}`.

Every forwarded request from a preview app carries these markers, whether the
selected target is another preview or an allowed production dependency:

```text
X-Faas-Caller-Env: preview
X-Faas-Caller-Preview-Of: public-api
```

Both headers are platform-owned: anything a workload sends under those names is
stripped before the hop, so the marker cannot be forged. Production callers
carry neither header, so a service that ignores them is unaffected.

Use them to skip irreversible work, tag writes as test data, or refuse the call
outright.

## Verifying the caller (preview)

The platform authorizes every internal call before forwarding it, but by
default the target is not told which service called: the gateway's caller
header stays on the node and is removed before the request reaches your code.
Only the preview markers above reach the target. Signed caller assertions let
the target learn and verify the caller itself. They are a preview and are not
enabled on every installation.

Every internal call then carries `X-Faas-Caller-Assertion`: a short-lived
(30 s) EdDSA JWT stating `sub` = calling app id, `aud` = receiving app id,
plus the account and calling instance. The audience binding is what stops a
service replaying an assertion it received against a sibling service.

The header is platform-owned and stripped from anything a workload sends, so it
cannot be forged. It is additive: nothing rejects a call for lacking one, and a
signing failure forwards the call unsigned rather than dropping it.

Guest workloads can fetch the public-only JWKS from
`https://<api-origin>/v1/service-caller-keys` without an API token. The endpoint
is rate-limited, contains no tenant metadata, and publishes only Ed25519 public
keys. Responses are cacheable for five seconds and must be revalidated
afterward; refresh immediately when a token names an unknown `kid`. During
node key rotation, the previous key stays published for the assertion's
30-second maximum lifetime so requests already in flight remain verifiable.

Verification must check the signature, issuer `gregale.svc`, audience equal to
the target app's platform-injected `FAAS_APP_ID`, and the token time window.
For Go `net/http` services, the `servicecaller` package provides middleware with a
five-second JWKS cache, single-flight refresh, and an immediate refresh when a
new signing-key ID appears:

```go
middleware, err := servicecaller.NewHTTPMiddleware(servicecaller.HTTPMiddlewareOptions{
    JWKSURL:  apiOrigin + "/v1/service-caller-keys",
    Audience: os.Getenv("FAAS_APP_ID"),
    Require:  true,
})
if err != nil {
    log.Fatal(err)
}
mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
    if caller, verified := servicecaller.VerifiedCaller(r.Context()); verified {
        log.Printf("verified internal caller: %s", caller.CallerAppID)
    }
    w.WriteHeader(http.StatusNoContent)
})
if err := http.ListenAndServe(":8080", middleware.Wrap(mux)); err != nil {
    log.Fatal(err)
}
```

Node services can use the same verification contract from `@gregale/sdk-node`:

```ts
import {
  createServiceCallerVerifier,
  SERVICE_CALLER_ASSERTION_HEADER,
} from '@gregale/sdk-node';

const apiOrigin = 'https://api.gregale.dev'; // use your deployment's API origin
const verifier = createServiceCallerVerifier({
  jwksUrl: `${apiOrigin}/v1/service-caller-keys`,
  audience: process.env.FAAS_APP_ID ?? '',
  require: true,
  onFailure: (error) => console.warn('service caller verification failed', error.code),
});

async function handle(req, res) {
  // Consume the platform-owned header before handing the request downstream.
  const rawAssertion = req.headers[SERVICE_CALLER_ASSERTION_HEADER.toLowerCase()];
  delete req.headers[SERVICE_CALLER_ASSERTION_HEADER.toLowerCase()];
  let caller: Awaited<ReturnType<typeof verifier.verifyHeader>>;
  try {
    caller = await verifier.verifyHeader(rawAssertion);
  } catch {
    res.statusCode = 401;
    return res.end('service caller assertion required or invalid');
  }
  if (!caller) {
    res.statusCode = 401;
    return res.end('service caller assertion required or invalid');
  }
  if (caller.callerAppId !== 'frontend-app-id') {
    res.statusCode = 403;
    return res.end('caller is not allowed');
  }
  req.serviceCaller = caller;
  return routeRequest(req, res);
}
```

`verifyHeader` returns only authenticated identity; with `require: false` (the
default), absent or invalid assertions return `undefined`. `require: true`
throws on either case so the service can reject the request. In both modes,
the application still owns its allowlist and business authorization. The
verifier uses Node's built-in Ed25519 support and does not add a runtime
dependency.

`Require: false` is the default rollout posture: missing or invalid assertions
do not block a request, and are never added to its context as verified
identity. Set `Require: true` only after assertion signing and key publication
are enabled on every node that may originate calls; required mode returns 401
if the assertion is missing, invalid, expired, or its key endpoint is
unavailable. Requiring a valid assertion authenticates the caller but does not
authorize that app; handlers must still apply their caller/business policy.
Non-`net/http` integrations can use the lower-level
`FetchTrustedKeys` and `Verify` functions.

Until signed assertions are enabled everywhere a call can come from, an absent
assertion is still possible; never treat it as verified identity.
