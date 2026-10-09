# OpenAPI hosting

Gregale can validate an OpenAPI document before publishing an API host.

```bash
gregale openapi preview APP_ID
gregale openapi diff openapi-baseline.yaml openapi.yaml
gregale openapi apply APP_ID --confirm --preview-sha256 SHA256
```

Preview and diff are read-only. Apply requires the hash returned by the preview,
making a publish explicit and idempotent. Keep the source document in version
control, pin referenced schemas, and review breaking-operation warnings before
promotion. Removing a response field's `required` guarantee is breaking because
clients can no longer rely on that field being present.

`gregale openapi diff` reports changed `oneOf` or `anyOf` response schemas and
changes to unsupported schema facets such as `enum`, `format`, constraints, or
composition as `UNKNOWN`, because Gregale cannot establish whether those
changes preserve client compatibility. Confirmed breaks exit with code 2; an
unknown comparison exits with code 3. Production contract previews include
these rows in `unknowns`, and the contract gate blocks promotion while any
unknown remains. Older snapshots may report `union_baseline_incomplete` or
`schema_baseline_incomplete` when they predate the fingerprints needed to
establish whether unsupported facets were present or changed. With the gate
disabled, a fresh production deployment captures a baseline that later
previews can compare fully.

The generated contract is available at `/docs` after publish. Use [Deployments](deploys.md) for rollout status and [Errors](errors.md) for stable error handling.
