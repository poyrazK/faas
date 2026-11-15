# Deployment overrides

Use `gregale.yaml` for source-specific build and runtime settings. Keep the file in version control so a deployment can be reproduced.

```yaml
name: checkout-api
hosting:
  start: node server.js
  port: 8080
  health: /healthz
```

`hosting.start` is the command the app runs: it replaces the command the source build produces, including a Procfile `web:` entry and the builder's detected start. Without it, Gregale keeps the built image's command and uses its own inferred start only when the image has none.

The CLI validates the schema before uploading source. A declared port must match the process listener, and the health endpoint should be cheap, authenticated only when necessary, and free of side effects. An app that listens on another port fails its deployment with `app_not_listening` naming both ports. Put credentials in [secrets](secrets.md), not in this file or image layers.

The API's `overrides` object applies to JSON image deployments only. A source upload declares its port, start command and health path here; a multipart deployment that sends an `overrides` field is rejected with `400 validation`.

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

A source deploy can declare the same probe in `gregale.yaml`, with the same keys, and the CLI sets its path with `gregale deploy --readiness-path /readyz`. A request override (the API field, the multipart `readiness_probe` field or the flag) wins for that one deployment. For `--repo`, `--project` and GitHub deploys, use `gregale.yaml`:

```yaml
hosting:
  readiness_probe:
    path: /readyz
    period_s: 5
```

A liveness probe is deliberately not declarable in `gregale.yaml`: a strict one is the usual cause of restart loops. Set it per deployment with the API when you need one.

Without a `liveness_probe`, Gregale checks only that a running app still answers, not that its dependencies are healthy, so a database outage does not restart it. An app whose health path is `/healthz` must keep answering it with 2xx. An app with another health path, or none, gets `GET /healthz`, and any answer below 500 counts as alive: a 404, a 401 from an auth layer, or a redirect to HTTPS or a sign-in page. A gRPC app gets a gRPC health check on Pro and Scale and no default check on other plans, and a service on a raw TCP port gets no default check. A timeout, a refused connection or a 5xx three times in a row restarts the instance. Set `liveness_probe` to check a specific path instead.
