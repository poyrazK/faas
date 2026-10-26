# ADR: Customer bucket CORS on the branded S3 endpoint

Date: 2026-10-25
Status: Accepted
Related: [ADR 20261023115741](20261023115741-every-capability-behind-a-platform-flag.md), [ADR 20261023223000](20261023223000-object-storage-bucket-scoped-availability.md)

## Context

Customers' browsers reach object storage through the branded endpoint
(`s3.gregale.dev`): presigned URLs from `POST …/signed-url` carry a per-URL
access key, and upload grants carry a `gregale-upload` token. The gateway
allowed cross-origin requests only from the operator's configured origins
(the console), for every bucket alike. A customer's own site could not use
either URL from a browser: its preflight got `403 AccessDenied`. S3 lets a
bucket owner set CORS rules (`PutBucketCors`); Gregale had nothing like it
(GA review gap S165).

Provider bucket CORS is not the fix. Customer URLs never point at the
provider; only the console's direct uploads do, and those keep the
operator's origins.

## Decision

1. **Customers store S3-shaped CORS rules per bucket.** A new table,
   `object_bucket_cors`, holds a bucket's rules (up to 100, 64 KiB, as in
   S3) and a revision. apid serves `GET`, `PUT` and `DELETE
   /v1/apps/{app}/buckets/{bucket}/cors` with the storage manage scope and
   the bucket write grant. `PUT` replaces every rule. A rule names allowed
   origins (`*`, or an http(s) origin with at most one `*`), methods (`GET`,
   `PUT`, `POST`, `DELETE`, `HEAD`), allowed header patterns, exposed
   headers and a max age (at most 24 h). The limits are in
   `pkg/api/limits.go`.
2. **The gateway applies the first matching rule.** Operator origins are
   still allowed on every bucket before authentication. Any other origin
   needs a rule of the bucket the request is for:
   - A preflight carries no signature, so the gateway finds the bucket from
     the URL itself: the bucket of its upload grant or of its presigned
     access key (`X-Amz-Credential`), and only when the path names that
     bucket. A header-signed request names no bucket a preflight can see, so
     only operator origins can preflight it.
   - An actual request is checked after authentication, when its bucket is
     known. A match adds `Access-Control-Allow-Origin` and the rule's
     exposed headers; a miss is `403 AccessDenied`, as before.
3. **It is a preview capability.** `bucket-cors` in the product catalog owns
   the routes, so its platform flag is off by default and the routes answer
   like absent ones until an operator releases them. Without stored rules the
   gateway behaves exactly as before, so the gateway needs no flag of its
   own.
4. **No S3 `?cors` API yet.** apid is the only writer of customer intent, so
   `PutBucketCors` through the gateway stays unsupported. The JSON API is
   the way to set rules.

## Consequences

- A customer's site can upload through upload grants and use presigned URLs
  from the browser, limited to the origins, methods and headers it allows.
- CORS is not authorization. Every request still needs a valid signature or
  grant; rules only decide which browser pages may read the responses.
- A preflight with a presigned access key costs one credential lookup, like
  any signed request. A bucket's rules are read once per preflight and per
  cross-origin request; browsers cache preflights for the rule's max age.
- **Compatibility:** expand-only migration. An N-1 gateway ignores the rules
  and keeps the operator-only behavior.

## Validation

- `TestValidObjectBucketCORSRules` and `TestMatchObjectBucketCORS` cover the
  rule bounds and first-match semantics.
- `TestObjectBucketCORS{Mem,PG}` cover storage, revisions, account scoping
  and the gateway's read.
- `TestGatewayBucketCORS` covers preflights through a presigned URL (allowed
  and refused origins, methods, headers, bucket mismatch, no bucket in the
  URL), the actual request's headers and refusal, and that operator origins
  behave as before.
