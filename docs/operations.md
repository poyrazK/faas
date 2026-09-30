# Gregale Operations

Operations defines an application contract for customer work: typed input and
output, verified ownership, progress, result references, and completion delivery.
This contract foundation is **internal and not launched**. HTTP admission,
execution integration, customer endpoints, and SDKs are being qualified in
separate changes. See [ADR-384](adr/384-customer-operations.md).

An operation identifies the customer's logical request. Execution attempts
retain their own identities and recovery semantics. Business outcome and
notification outcome are separate fields: a successful export remains successful
while its completion webhook is awaiting retry.

## Contract

`OperationDefinitionSpec` captures a named HTTP target, platform-tenant ownership,
JSON input/output schemas, progress stages, a completion webhook, and recovery
policy. Schema validation accepts bundled local references and rejects external
resources. Canonical JSON supplies stable input identity, rejects duplicate
members, and treats equivalent number spellings consistently.

Result artifacts declare a managed object reference, exact byte count, and
SHA-256. A reference does not itself reserve or preserve object bytes. Storage
verification and download authorization belong to the execution integration.

Uncertain effects require reconciliation by default. `safe_retry` is an explicit
declaration that repeating the handler is safe; it does not establish exactly-once
external effects. Confirmed results and delivery receipts have independent
lifecycles.

## Limits

The proposed qualification policy lives in `pkg/api/limits.go`; it is independent
of existing VM and asynchronous invocation quotas. Free has no Operations
allowance. The following are contract bounds, not a rollout announcement.

| Bound | Hobby | Pro | Scale |
| --- | ---: | ---: | ---: |
| Definitions per app | 10 | 50 | 200 |
| Pending operations per account | 1,000 | 10,000 | 100,000 |
| Reports per operation | 1,024 | 4,096 | 16,384 |
| Recoveries per operation | 32 | 128 | 256 |
| Progress stages | 16 | 32 | 64 |
| Result retention | 7 days | 30 days | 90 days |
| Event retention | 1 day | 7 days | 30 days |
| Idempotency retention | 30 days | 90 days | 180 days |
| Artifacts per operation | 8 | 16 | 32 |
| Bytes per artifact | 8 MiB | 32 MiB | 64 MiB |
| Total artifact bytes per operation | 32 MiB | 128 MiB | 256 MiB |

Schemas are capped at 64 KiB, submission JSON at 1 MiB, and idempotency keys at
128 bytes. Retained idempotency receipts outlive results. A replay during that
window must not silently create another operation after its result expires.
Quota problems expose numeric `limit` and `observed` values and link here.
