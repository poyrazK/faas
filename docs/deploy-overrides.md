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
