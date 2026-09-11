# OpenAPI hosting

Gregale can validate an OpenAPI document before publishing an API host.

```bash
gregale openapi preview APP_ID
gregale openapi diff openapi-baseline.yaml openapi.yaml
gregale openapi apply APP_ID --confirm --preview-sha256 SHA256
```

Preview and diff are read-only. Apply requires the hash returned by the preview, making a publish explicit and idempotent. Keep the source document in version control, pin referenced schemas, and review breaking-operation warnings before promotion.

The generated contract is available at `/docs` after publish. Use [Deployments](deploys.md) for rollout status and [Errors](errors.md) for stable error handling.
