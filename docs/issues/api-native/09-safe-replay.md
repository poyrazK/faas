# Sub-issue #09 — Safe request replay with redaction

Parent: [README.md](README.md)

## Problem

Two replay surfaces exist today, both unsafe for the requested guarantee:

1. **Async invocation replay** at `cmd/apid/handlers_invocations.go:489-499`
   and `:541-556`. Documented contract in `api/openapi.yaml:5054-5075`
   says: *"method, path, payload, and headers are replayed verbatim."*
2. **App-error recording** at
   `cmd/gatewayd-internal/app_errors_recorder.go:249-268` already does
   redaction for stored error samples — but only there, not on replay.

Neither surface:

- Captures arbitrary gateway requests (only async invokes + error samples).
- Redacts before reissue.
- Tied to the consumer identity from #05 (so the redactor knows which
  headers belong to which key).

## Proposal

### Capture

A new flag on apps: `capture_failed_requests` (default off, GDPR-conscious).
When on, every failed request (status ≥ 500 or transport error) gets a
row in `request_logs` (sub-issue #08) **plus** a `request_captures`
row that retains the body + headers needed for replay:

```sql
CREATE TABLE request_captures (
  id              UUID NOT NULL DEFAULT gen_random_uuid() PRIMARY KEY,
  request_log_id  BIGINT NOT NULL REFERENCES request_logs(id) ON DELETE CASCADE,
  account_id      UUID NOT NULL,
  app_id          UUID NOT NULL,
  consumer_id     UUID NULL,
  body            BYTEA NOT NULL,
  headers         JSONB NOT NULL,
  captured_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at      TIMESTAMPTZ NOT NULL  -- now() + retention
);
CREATE INDEX ON request_captures (app_id, captured_at DESC);
```

`body` is raw, `headers` is a JSONB map of `[name, [redacted-values...]]`.
The raw values are **never stored**; the redactor runs at write time.

### Redactor

Two layers:

1. **Header-name denylist** — `Authorization`, `Cookie`, `Set-Cookie`,
   `X-Api-Key`, and any header that matched a consumer key prefix in
   #05 are written as the literal string `"<redacted:consumer_key:N>"`.
2. **Body pattern redaction** — vendored regex catalog under
   `pkg/apid/redact/patterns.go` covering:
   - AWS access key ids (`AKIA[0-9A-Z]{16}`).
   - Stripe live keys (`sk_live_...`, `rk_live_...`).
   - Generic bearer tokens (`Bearer [A-Za-z0-9._-]{16,}`).
   - PEM private keys (`-----BEGIN [A-Z ]+PRIVATE KEY-----`).
   - JWTs (three-base64 segments separated by dots).

The redactor is shared with `cmd/gatewayd-internal/app_errors_recorder.go`
so existing error-sample redaction uses the same catalog. Refactor:
extract `pkg/apid/redact` as a public package; both surfaces depend on
it.

### Replay

Extend the existing async replay endpoint to also accept a
`capture_id`:

```
POST /v1/request-captures/{capture_id}/replay
```

- Resolves the capture row.
- Reconstructs headers (redaction markers are NOT rehydrated — those
  fields are dropped).
- Re-issues via the same path the original took, with a synthetic
  `X-Faas-Replay-Of: <request_id>` for traceability.
- Returns the new request's outcome.

A second endpoint replays a customer-supplied payload:

```
POST /v1/request-captures/dry-replay
Body: { capture_id OR (method, path, headers, body) }
```

Runs the redactor in dry-run mode and returns the redacted shape without
actually issuing the request. This is the surface a debugging UI would
use.

### Limits (pkg/api/limits.go)

- `request_capture_retention_days` = 7.
- `request_capture_max_body_bytes` = 1 MB (reject larger at capture time).
- `request_capture_per_app_per_day` = 1000 (Free/Hobby), 10000 (Pro),
  100000 (Scale).
- `redact_patterns_count` = bounded by the vendored catalog (no customer
  regex upload in v1 — explicit non-goal).

## Acceptance

1. Customer enables `capture_failed_requests`; triggers a 500; the
   capture row exists with `Authorization` redacted.
2. POST .../replay reissues and the new request shows the redacted
   header as absent.
3. POST .../dry-replay returns a JSON shape showing what would be sent
   without sending.
4. The redactor catalog catches `AKIA...` in a body; the body in
   `request_captures` does not contain the original key.
5. Existing error-sample redaction at
   `cmd/gatewayd-internal/app_errors_recorder.go:249-268` still passes
   its tests (refactor preserves behavior).
6. Migration is numbered in the next available slot; cross-PR fence check
   runs before commit.

## Dependencies

- #05 (consumer identity → header denylist knows what to redact).
- #08 (request_logs is the index; `request_captures` extends it).

## Audit provenance

- `cmd/apid/handlers_invocations.go:489-499`, `:541-556` — verbatim replay.
- `api/openapi.yaml:5054-5075` — documented contract.
- `cmd/gatewayd-internal/app_errors_recorder.go:249-268` — existing
  redactor (only place it's applied).
