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
