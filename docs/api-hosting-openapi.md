# OpenAPI hosting

Gregale can validate an OpenAPI document before publishing an API host.

```bash
gregale openapi preview APP_ID
gregale openapi diff openapi-baseline.yaml openapi.yaml
gregale openapi apply APP_ID --confirm --preview-sha256 SHA256
```

Preview and diff are read-only. Apply requires the hash returned by the preview, making a publish explicit and idempotent. Keep the source document in version control, pin referenced schemas, and review breaking-operation warnings before promotion.

The generated contract is available at `/docs` after publish. Use [Deployments](deploys.md) for rollout status and [Errors](errors.md) for stable error handling.

## Pre-promotion route checks

An imported OpenAPI document can mark up to ten static, read-only `GET`
operations for an exact-candidate check before Gregale switches live traffic:

```yaml
paths:
  /v1/health:
    get:
      x-gregale-hosting-check: true
      responses:
        '200':
          description: API is ready
```

Only marked operations are probed. Checks use an empty `GET`, do not follow
redirects, and cannot contain path parameters or query strings. A proven `2xx`,
`3xx`, `401`, or `403` response passes; `404`, `429`, and `5xx` responses fail
the candidate. A `401` or `403` can be a healthy response when the app itself
requires customer credentials. The checks prove that the selected route
responded from the candidate; they do not validate response bodies or schemas.

The deployment hosting receipt records each route result and the hash of the
imported OpenAPI document used to select the checks. An absent document or a
document without marked operations keeps the existing health/connectivity gate.
