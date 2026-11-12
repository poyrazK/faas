# Gregale API specification

The Gregale REST API is described by an OpenAPI 3.1 document. Use it to
generate a client, import the API into a tool such as Postman or Insomnia, or
check a request against the contract. The [API reference](/docs/api) on this
site is rendered from the same document.

## Download the spec

The spec is public and needs no API key:

```bash
curl -s https://api.gregale.dev/v1/openapi.yaml
curl -s https://api.gregale.dev/v1/openapi.json
```

Both responses can be cached for five minutes. Most client generators
(`openapi-generator`, `oapi-codegen`) prefer the JSON form.

The spec is checked against the API on every change, so routes, request and
response fields, and error codes in the document match what the API serves.
The official [SDKs](sdks.md) cover the same surface.

## What the spec covers

The customer API under `/v1/`: account, apps, deployments, instances,
domains, crons, keys, secrets, usage and the rest of the resources the CLI
uses. A few public routes sit outside it:

- The CLI sign-in flow (`/v1/cli-auth/*`) is anonymous by design and is
  documented with the [CLI](cli-setup.md).
- `GET /v1/events` is a server-sent event stream.
- The DPA (`GET /v1/account/dpa`) and the status page are public documents,
  not API resources.

## Authentication

Send an API key (`fp_live_…`) as a bearer token:

```bash
curl -sH "Authorization: Bearer $FAAS_TOKEN" https://api.gregale.dev/v1/apps
```

Each operation in the spec lists the scopes it needs. See
[Authentication](auth.md) for keys and scopes.

## Errors

Errors use RFC 9457 `application/problem+json` with a stable `code` field.
Limit errors include the limit, the observed value and a documentation link.
See [Errors](errors.md) for every code.

`429 Too Many Requests` comes in two shapes:

- `application/problem+json` for plan limits and exhausted quotas
  (`plan_limit_concurrency`, `quota_exhausted`), with a `code`.
- `text/plain` from the rate limiter that protects unauthenticated
  endpoints. It carries no account context and no `code`.

Handle both: check the `Content-Type` before parsing the body, and honor
`Retry-After` when it is present.

## Retries

Most `POST` operations accept an `Idempotency-Key` header. For 24 hours, a
retry with the same key returns the original result, marked
`Idempotent-Replayed: true`, instead of repeating the operation. Reusing a key
with a different body, query or precondition header returns
`422 idempotency_key_reused` and does not run.
