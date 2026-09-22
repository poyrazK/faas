# Sidecars

Sidecars are bounded helper containers that run beside an app instance. Use them for telemetry, protocol adapters, or local caching—not for a second application control plane.

Declare each sidecar in the deployment request with a digest-pinned image, an explicit command, and a resource budget. Keep the interface narrow (usually localhost or a named Unix socket), make startup and shutdown graceful, and send logs to stdout/stderr. The API accepts at most two entries: one `init` and one long-running `sidecar`.

```bash
gregale deploy --dry-run
```

The dry run reports image, CPU/RAM budget, ports, and plan compatibility before upload. Sidecars share the app's lifecycle and failure domain, so a crash or resource spike can affect the main process. Keep them stateless and safe to restart.

## Startup probes

`startup_probe` is an optional deployment-level override for the image's OCI
`HEALTHCHECK`. Gregale runs the same exec probe before marking a sidecar
healthy and while monitoring it; omit it to keep the image probe, or use
`["NONE"]` to disable a baked probe.

```yaml
extensions:
  - name: metrics
    image: registry.example.com/metrics@sha256:<64-hex-digest>
    startup_probe:
      test: [CMD, /usr/local/bin/ready]
      interval_s: 5
      timeout_s: 2
      retries: 3
```

## Manifest presets

Telemetry extensions can be declared in either `gregale.yaml` or
`gregale.toml`. A preset supplies the stable name, port, and environment
defaults; the image is still required as an immutable OCI digest.

```toml
[[extensions]]
preset = "opentelemetry"
image = "registry.example.com/otel-collector@sha256:<64-hex-digest>"
env = { OTEL_EXPORTER_OTLP_ENDPOINT = "http://127.0.0.1:4318" }
```

The built-in presets are `opentelemetry`, `sentry`, and
`datadog-dogstatsd`. Explicit fields override preset defaults, and the normal
sidecar cap, digest, stateless-image, and plan validation still applies.
