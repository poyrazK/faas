# Application companions

Gregale can attach a small helper workload to every instance of an
application. Typical uses are an OpenTelemetry collector, a database proxy,
or a custom reverse proxy. The helper shares the application's lifecycle and
loopback network, but has its own image filesystem and resource controls.

Declare helpers under `companions` in `gregale.yaml`:

```yaml
companions:
  - preset: opentelemetry
    env:
      OTEL_EXPORTER_OTLP_ENDPOINT: https://telemetry.example.com
```

The built-in presets are `opentelemetry`, `sentry`, and
`datadog-dogstatsd`. A preset supplies a stable name, port, environment
defaults, and an operator-qualified digest-pinned image. Explicit fields
override the defaults. If a Gregale installation has not configured the
preset image, deployment fails with `companion_preset_unavailable`; use a
custom digest-pinned `image` or ask the operator to enable that preset.

## Custom companions

A custom helper uses an immutable OCI image reference:

```yaml
companions:
  - name: database-proxy
    image: registry.example.com/database-proxy@sha256:<64-hex-digest>
    port: 5432
    ram_mb: 128
    depends_on:
      - name: main
        condition: started
```

Every process receives loopback discovery variables such as
`FAAS_WORKLOAD_DATABASE_PROXY_ADDR=127.0.0.1:5432`. The application is
available to the helper as `FAAS_WORKLOAD_MAIN_ADDR`. A helper without a port
is valid for background processing.

## Startup and liveness probes

`startup_probe` gates a workload's `healthy` dependency state. It may use an
exec command, an HTTP GET, or a TCP connection check. Omit it to use the image's
OCI `HEALTHCHECK`. A separate `liveness_probe` monitors a running companion;
if omitted, Gregale keeps the previous behavior and reuses the effective
startup probe for liveness. Set `startup_probe: {test: [NONE]}` to disable the
baked image check when no startup or liveness check is desired; set
`liveness_probe: {test: [NONE]}` to keep startup gating but disable ongoing
liveness checks.

```yaml
companions:
  - name: metrics
    image: registry.example.com/metrics@sha256:<64-hex-digest>
    startup_probe:
      http_get:
        path: /ready
        port: 9090
      period_s: 2
      timeout_s: 1
      failure_threshold: 15
    liveness_probe:
      tcp_socket:
        port: 9090
      period_s: 10
      failure_threshold: 3
```

Use `exec: {command: [...]}`, `http_get: {path, port}`, or
`tcp_socket: {port}`. A zero/omitted probe port uses the companion's declared
port, then the image port, then Gregale's default port. Typed probes default
to a 10-second period and 1-second timeout; legacy OCI `test` probes retain
Docker's 30-second defaults and `interval_s`/`retries` aliases. Probe timeouts,
periods, initial delay, and thresholds are bounded by API validation.

Each companion also gets a named, in-memory shared directory at
`/tmp/gregale/companions/<name>`. The same path is visible to the application
and the other companions, and its location is exported as
`FAAS_WORKLOAD_<NAME>_SHARED_DIR`. Use it for Unix sockets or transient files.
It is instance-local, not replicated, not persistent storage, and disappears
on a cold replacement or redeploy. Never use it as the source of truth.

## Primary ingress

A long-running custom reverse proxy can receive the application's normal
public hostname and custom domains:

```yaml
companions:
  - name: edge-proxy
    image: registry.example.com/edge-proxy@sha256:<64-hex-digest>
    port: 8081
    primary_ingress: true
    depends_on:
      - name: main
        condition: started
```

Only one companion may set `primary_ingress`, and it must declare a port.
During a traffic-split rollout, all traffic-bearing deployments must agree on
the primary companion and port. Gregale fails routing closed when they do not,
so a rollout cannot accidentally bypass the proxy. Deploy matching companion
configuration in the new revision before moving traffic.

## Limits and lifecycle

- A deployment accepts at most two helper entries: one one-shot setup helper
  and one long-running companion.
- Helpers must be stateless and safe to restart. Database server images and
  other stateful images are rejected.
- Each helper has its own memory, CPU, scratch, and disk-I/O controls. Its
  reserved RAM is billable in addition to the application RAM.
- Long-running helpers receive graceful shutdown and are health-monitored.
  An essential helper failure fails or restarts the workload set; set
  `essential: false` only when the application can safely continue alone.
- Logs go to stdout and stderr. Secrets belong in the normal sealed
  environment/secrets flow, not in the shared directory.

To recover from a bad helper release, redeploy the last known-good companion
digest or remove the companion declaration and redeploy. Removing
`primary_ingress` restores the normal application listener after the serving
deployments converge. Existing clients may still send the deprecated
`sidecars` API field and manifests may still use `extensions`; new
configurations should use `companions`.
