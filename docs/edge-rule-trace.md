# Reusable edge-rule trace scenarios

`gregale edge-rules trace` accepts either individual request flags or a
versioned JSON scenario. Scenario files make a trace easy to replay from a
terminal, check into a private test repository, or share with a teammate.

To trace a named project environment, provide both `--project` and
`--environment` with request flags, or set both top-level fields in the
scenario. The URL host must be that workload's stable environment URL or a
verified custom domain assigned to the environment. The trace applies the
environment's effective declared-route policy and environment-owned
headers/CORS replacement; other edge-rule kinds remain application-owned.
Without the selector, the trace uses the app's current policy as before.

```sh
gregale edge-rules trace --config trace.json
```

The version 1 format is:

```json
{
  "version": 1,
  "project": "payments",
  "environment": "staging",
  "app": "demo",
  "request": {
    "url": "https://replace-with-environment-host/submit",
    "method": "POST",
    "headers": [
      "Content-Type: application/json",
      "X-Region: west",
      "X-Region: east"
    ],
    "client_ip": "203.0.113.10",
    "country": "US",
    "body": "{\"count\":3}"
  }
}
```

The equivalent flags are:

```sh
gregale edge-rules trace --app demo --project payments --environment staging \
  --url https://replace-with-environment-host/submit --method POST
```

`app` and `request.url` are required. `project` and `environment` are optional
but must be supplied together. `method` defaults to `GET`; headers,
client IP, country, and body are optional. The `headers` array preserves
repeated values. `body` is UTF-8 text; use `body_base64` instead for arbitrary
bytes. Set at most one of those fields. Supplying an empty body still marks the
body as present. Request bodies are limited to 1 MiB, and config files to 2
MiB.

The trace uses the URL's host and path; it does not evaluate query parameters.
For an environment trace, the project state read is used only for that
workload's route and edge-policy context; environment variable and secret data
are not included in trace output. Replace the example host with the workload's
exact stable URL or verified environment domain shown by the project environment
state command.
Scenario files are plain text. Keep credentials and sensitive body data out of
public repositories even though the resulting trace redacts credential-like
headers and withholds the body.

For `kind=cache` rules, the trace includes the configured fresh, stale-while-
revalidate, and stale-if-error windows, along with the method and `vary_on`
policy. It can identify deterministic method and credential bypasses. When a
request passes those checks, the simulation stops as incomplete: authentication,
async or pinned-deployment context, and live cache contents determine whether
runtime serves a fresh hit, refreshes stale content, falls through on a miss, or
serves stale content on an error. The trace never predicts one of those results.

When the app's effective request-budget limits are available, the trace also
reports the matching `kind=budget` rule's configured value, any accepted
override-header decision, the effective milliseconds, and the plan ceiling.
The selected header is the rule's `allow_override_header` or the platform
default `x-faas-budget-ms`. If no budget rule matches, it shows the app's positive
`request_timeout_s` override or the type-aware plan baseline. Invalid or
non-positive override values fall back to the rule value; values above the
plan ceiling are shown as clamped. This is a configured budget candidate, not
a predicted timeout: the gateway starts the deadline only after upload, wake,
routing, and per-VM admission. A possible response-cache lookup still stops the
trace as incomplete because a runtime cache hit can bypass guest execution.

For a matching `kind=throttle` rule, the trace reports configured and
gateway-effective requests per second and burst, key dimension, missing-key
behavior, and effective per-rule key cap. When available, it also shows the
plan validation ceiling plus the separate app-wide and account-wide limits. It
never resolves or prints an authenticated identity, consults GeoIP, or
reads/consumes a token bucket; therefore it does not predict whether this
request is admitted or receives HTTP 429. Those runtime gates remain explicitly
incomplete.

For a matching `kind=retry` rule, the trace reports effective total attempts
and replays, the request-budget floor, backoff, aggregate replay budget, and
whether this request's method/idempotency-key guard permits replay. It reports
only whether an `Idempotency-Key` is present, never its value. A replay still
requires the operator retry gate and a transport failure; body replayability,
remaining budget, a healthy sibling, response commitment, and live aggregate
budget are runtime state, so no retry or response outcome is inferred. The
gateway retries transport failures only; it does not replay an application
HTTP error status.

```json
{
  "version": 1,
  "app": "demo",
  "request": {
    "url": "https://api.example.com/upload",
    "method": "PUT",
    "headers": ["Content-Type: application/octet-stream"],
    "body_base64": "AAEC/w=="
  }
}
```

Unknown fields and unsupported versions are rejected. Use `--config -` to read
the JSON from stdin. `--config` cannot be combined with individual request
flags; edit the scenario file when changing a request. The file is read locally
and is not uploaded. Gregale fetches the app's current rules, evaluates them
locally, withholds request-body contents, and redacts credential-like header
values from trace output. Environment-owned routes replace the app route
contract, including an explicit empty allowlist; environment-owned headers and
CORS edge-rule rows replace only their app-wide kinds while all other app rules
remain in effect. The separate per-app default CORS setting remains app-owned.
