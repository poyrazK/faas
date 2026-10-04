# ADR-552: Owned S3 notification destinations

Status: Accepted (2026-10-03)

## Context

ADR-551 publishes confirmed object mutations atomically with their journals.
Customers also need S3 notification configuration and queue destinations, with
the same durable acceptance and recovery boundary. Gregale queues are owned
application invocation queues; arbitrary external AWS destinations have no
platform authorization or delivery contract.

## Decision

Provide signed S3 GET/PUT `?notification` and control API GET/PUT/DELETE
`/v1/apps/{slug}/buckets/{bucket}/notifications`. Replace the complete rule set
atomically. An empty set clears future routing and preserves revision history.
Configuration reads and clearing require no provider request and remain usable
when ingress is disabled. S3 reads require bucket read; S3 replacements require
bucket write. Control operations require storage manage, MFA and bucket write.
Delegating bucket write therefore also delegates selection of eligible
destinations in its account. Destination filters are routing, not permissions.

Use Gregale ARNs with canonical account/application UUIDs:

- Functions: `arn:gregale:lambda:REGION:ACCOUNT:function:APP_UUID`.
- Queues: `arn:gregale:sqs:REGION:ACCOUNT:APP_UUID/QUEUE_NAME`.

Validate same account and bucket region, a non-deleted owned application, plan
entitlement, and an existing enabled queue binding. Destination validation is
local and cannot be skipped. It does not send an AWS `s3:TestEvent` or probe a
function. Queue and CloudFunction XML configurations are supported. SNS,
EventBridge and AWS ARNs are rejected. Configuration limits live in
`pkg/api/limits.go`; unknown/repeated XML fields and unsupported events fail
before replacement. ObjectCreated PUT/copy/multipart completion, ObjectRemoved
delete/marker creation and LifecycleExpiration delete/marker creation are
supported, including their group wildcards. Prefix/suffix values are decoded
once at the XML boundary. Reject overlapping key filters for intersecting
event types; disjoint suffixes or events may share a prefix.

Capture matching notification rules, logical bucket identity, queue binding
identity and retry configuration inside each confirmed mutation transaction.
Use the existing fanout receipt and recipient progress ledger. A rule revision
and ID determine the recipient identity; the existing event identity determines
its stable invocation ID. Policy removal/replacement cannot redirect accepted
events. Removed queue bindings cannot redirect a receipt to a newly created
binding with the same name. Deleted destinations fail through existing fanout
failure/replay inspection. No historical backfill occurs.

Functions receive ordinary async POST invocations and queues receive named
queue invocations. Both carry a JSON `Records` array using S3 event schema 2.1,
logical bucket name, configuration ID, form-encoded object key, confirmed size
and ETag when known, and the owned public version selector. The bucket ARN is
Gregale-owned. Lifecycle expiration is distinguished from customer deletion.
Native versions, physical bucket placement, credentials and provider tokens
never enter the message. Canonical Gregale event headers supply delivery
identity. Provider sequencer, request/actor provenance and AWS test-message
semantics are outside this profile; there is no provider mutation ordering
proof from which to invent those values.

Notification queue admission locks the account and application, counts live
queue rows and inserts through SQLC in one transaction. The application lock
also excludes key-share locks from concurrent invocation FK inserts. A
committed identical admission succeeds before capacity is checked again,
covering a lost recipient checkpoint at full depth. Queue capacity/entitlement
changes retry through the existing bounded fanout policy and operator replay.
Roll out compatible scheduler consumers before producers and configuration
ingress; older schedulers do not understand queue notification snapshots.
Downgrade refuses active configuration or retained notification snapshots,
including delivered receipts that can still be inspected/replayed.
Normal dispatch retains account concurrency, retries, queue consumers and
dead-letter behavior. This does not redesign the other legacy queue producers.

## Consequences

Configuration and captured destination snapshots add bounded durable writes.
Failure to append a notification snapshot rolls back local mutation settlement;
existing provider-proof recovery remains responsible for an accepted write.
Delivery is at least once at the application-effect boundary. Consumers must
deduplicate external effects using the event headers; there is no ordering
promise. Direct URL/provider-external mutations still lack authoritative event
proof and remain separate work. Full AWS destination compatibility and ordering
are not claimed by this profile.

Local AWS SDK HTTP, memory/PostgreSQL reconstruction, full-queue checkpoint-loss,
concurrent admission, configuration/tenant validation and SDK/control tests
qualify this implementation without accessing real providers.

Protocol references: [S3 configuration replacement](https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutBucketNotificationConfiguration.html),
[key filters](https://docs.aws.amazon.com/AmazonS3/latest/userguide/notification-how-to-filtering.html),
and [notification message schema](https://docs.aws.amazon.com/AmazonS3/latest/userguide/notification-content-structure.html).
