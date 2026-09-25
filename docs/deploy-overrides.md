# Deployment overrides

Use `gregale.yaml` for source-specific build and runtime settings. Keep the file in version control so a deployment can be reproduced.

```yaml
name: checkout-api
hosting:
  start: node server.js
  port: 8080
  health: /healthz
```

The CLI validates the schema before uploading source. A declared port must match the process listener, and the health endpoint should be cheap, authenticated only when necessary, and free of side effects. Put credentials in [secrets](secrets.md), not in this file or image layers.

Overrides apply to the selected deployment only; they do not mutate organization defaults. Use [Deployments](deploys.md) to inspect the resolved configuration and roll back to a known-good revision.

For API deployments, `overrides.readiness_probe` optionally configures a recurring HTTP or gRPC check for a long-running primary app. After the configured number of consecutive failures, Gregale withdraws that instance from request routing; a passing check restores it without restarting the VM. This is separate from `healthcheck`, which gates startup, and `liveness_probe`, which restarts a persistently unhealthy VM. Defaults are a 5-second period, 2-second timeout, and 3 consecutive failures. For example:

```json
{
  "overrides": {
    "readiness_probe": {
      "path": "/readyz",
      "period_s": 5,
      "timeout_s": 2,
      "failure_threshold": 3
    }
  }
}
```

Source deployments can keep the primary app's probe contract beside the code
in `gregale.yaml` (the same `hosting.*_probe` fields are accepted in
`gregale.toml`):

```yaml
hosting:
  startup_probe:
    path: /startupz
    interval_s: 5
    timeout_s: 2
    retries: 3
  readiness_probe:
    path: /readyz
    period_s: 5
    timeout_s: 2
    failure_threshold: 3
  liveness_probe:
    path: /livez
    interval_s: 10
    timeout_s: 2
    consecutive_failures: 3
```

Startup probes accept either `path` or `grpc`; readiness and liveness probes
accept the same HTTP-path or standard gRPC health-check choice as their API
override fields. Values are validated with the deployment API's existing
bounds and plan gates. The older `hosting.health: /path` setting remains
available for inferred HTTP health paths. An explicit per-deploy API override
takes precedence over the corresponding manifest probe.
