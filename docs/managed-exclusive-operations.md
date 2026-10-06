# Managed exclusive operations

Gregale's managed exclusive operation API coordinates accepted work under a
durable named key. A policy can allow selected apps and Jobs to share one lane.
The authenticated account and, for supported app operations, verified
platform-customer identity define the security scope; the key is only a
business identifier.

Managed ownership is available for app invocations, deployment-attached
AppTasks, Job runs, HTTP and command cron/webhook/broker triggers, and recurring Job
schedules. Policies with `member_job_ids` require account scope and cannot use
an app project environment. Gregale validates each selected Job ID against the
authenticated account. Native KVM stale-snapshot acceptance remains an open
release gate.

## Configure a policy

Create a policy file with the resource IDs allowed to participate. Apps and
Jobs can share one account-scoped lane:

```json
{
  "scope": "platform_tenant",
  "member_app_ids": ["APP_ID"],
  "contention": "queue",
  "lease_seconds": 30,
  "max_attempt_seconds": 600,
  "max_attempts": 5,
  "retry_after_seconds": 5
}
```

For a policy that includes a Job, use `scope: "account"` and add its UUID
under `member_job_ids`. Jobs cannot join a platform-tenant policy because
their execution contract has no trusted tenant-specific context.

Then save it to the account:

```sh
gregale operations policy upsert --file crm-sync-policy.json crm-sync
gregale operations policy list
```

For repository-managed configuration, declare the same policy under
`exclusive_operations.policies` and its cron, webhook, broker, or Job schedule bindings under
`exclusive_operations.bindings` in `gregale.yaml` (or `gregale.toml`). Apply
those declarations explicitly with `gregale operations reconcile --dir .`.
The CLI resolves app slugs, Job names, and trigger selectors against the
authenticated account before it writes policy or binding state. See the
[CRM example](../examples/managed-exclusive-operations/README.md).

The policy scope can be `account` or `platform_tenant`. Contention must be
selected explicitly:

- `queue` keeps each accepted request as distinct FIFO work.
- `reject` returns a conflict when the lane already has pending or running work.
- `join_existing` links only an equivalent request; it requires an
  `equivalence_key` and equal normalized request content.

Request idempotency is a separate concern. `Idempotency-Key` replays the same
submission; it does not turn two different requests with the same lane key into
duplicates.

## Submit and inspect work

An account operator can submit for a trusted tenant ID:

```sh
gregale operations start --policy crm-sync \
  --key '"customer:acme:crm-sync"' --tenant TENANT_ID \
  --payload '{"mode":"incremental"}' crm-api
```

A downstream platform-customer credential can use `--self`; the tenant is
derived from that credential:

```sh
gregale operations start --policy crm-sync --self \
  --key '"customer:acme:crm-sync"' \
  --payload '{"mode":"incremental"}' crm-api
```

Use `gregale operations get ID`, `wait ID`, and `cancel ID` to inspect or
control the accepted operation. Tenant credentials add `--self`. The receipt
shows the current state and committed result but never exposes renewal tokens
or accepted request contents.

Submit a Job run through the same policy with `gregale operations start-job`:

```sh
gregale operations start-job --policy customer-sync \
  --key '"customer:acme:crm-sync"' nightly-import --tasks 1
```

A deployment-attached command can use that lane too:

```sh
gregale app crm-api exec --operation-policy customer-sync \
  --operation-key '"customer:acme:crm-sync"' -- bin/sync --incremental
```

Gregale creates the AppTask or JobRun only after it acquires ownership. Use
`--equivalence-key` only when `join_existing` should link requests with the
same explicit equivalence identity and normalized target/input.

## Route scheduled work and external triggers through the lane

Bind an existing HTTP or command cron, or a verified inbound-webhook endpoint, to the same
policy, key, and optional equivalence identity:

```sh
gregale operations bind-trigger --policy crm-sync \
  --key '"customer:acme:crm-sync"' cron CRON_ID
gregale operations bind-trigger --policy crm-sync \
  --key '"customer:acme:crm-sync"' inbound_webhook ENDPOINT_ID
gregale operations bind-trigger --policy crm-sync \
  --key '"customer:acme:crm-sync"' broker TRIGGER_ID
```

For command crons, the accepted operation owns creation of the deployment-pinned
AppTask. Scheduled occurrence history exposes its `exclusive_operation_id`, and
the task's result is committed under that operation generation. Fire-now
receipts expose the operation immediately and gain a task ID when the worker
materializes the owned AppTask.

Bind a recurring Job's native schedule using its Job ID:

```sh
gregale operations bind-trigger --policy customer-sync \
  --key '"customer:acme:crm-sync"' job_schedule JOB_ID
```

Each due Job schedule occurrence is admitted with stable retry identity before
the schedule cursor advances. The resulting JobRun carries the ownership
generation. Manual `start-job` requests and app work with the same account
policy and business key therefore contend in one lane.

`broker` binds a non-cron Trigger resource, including an in-platform queue
trigger or a Kafka, NATS, Redis Streams, SQS-compatible, or AMQP consumer.
Gregale admits each stable broker message identity into the operation lane
before acknowledging the delivery. A `reject` policy dead-letters a competing
message with an explicit operation-rejection reason; it does not silently
turn rejection into automatic queueing.

For a `platform_tenant` policy, an account operator must also pass
`--tenant TENANT_ID`. Gregale checks that tenant is active and linked to the
trigger's app through an active tenant surface. The business key never supplies
customer identity. `join_existing` bindings require `--equivalence-key`; use
the same equivalence key and normalized invocation when a manual submission
should join scheduled or webhook work. `gregale operations unbind-trigger
SOURCE ID` returns that trigger to its normal dispatch path.

Scheduled cron occurrences use their scheduled instant as the retry identity,
so scheduler retries do not create a second operation. Manual fire-now returns
an `operation_id` in its receipt. A verified webhook uses its provider event ID
for deduplication and acknowledges only after operation admission commits.
Queue mode preserves separate occurrences. Reject mode consumes a scheduled
occurrence that collides with an owner; an inbound provider receives a conflict
and may retry according to that provider's delivery policy.

## Ownership and fencing

The scheduler acquires a monotonically increasing fencing generation for each
lane and renews its lease while it dispatches work. Expired ownership cannot
be renewed by the previous worker. A replacement receives a newer generation,
and Gregale checks the operation, generation, lease, and VM incarnation in the
same database transaction that commits supported results or effects.

An active owner prevents snapshot capture and parking. Instance replacement or
restore revokes the old claim before new work is delivered; restoring guest
memory never restores ownership authority.

Fencing applies to Gregale-controlled commits. Arbitrary database writes and
provider calls need a compatible transaction, adapter, or idempotency key to
reject stale work. A lease check immediately before an external call cannot
close the check-to-use race.

## Current integration boundary

Manual app, Job, and AppTask submissions, HTTP and command cron ticks/fire-now,
recurring Job schedules, signature-verified inbound webhooks, and bound
broker/queue triggers admit through the same operation store and scheduler
claim path. A command-cron occurrence and its cursor advance commit atomically
with operation admission; the claimed generation creates the deployment-pinned
AppTask and fences its result. Fire-now receipts point to the accepted operation
and are linked to its task when dispatch begins. AppTask and Job result
transitions carry and enforce the ownership generation. Bounded admission,
dispatch, lease-renewal, and due-candidate metrics avoid customer IDs and
business keys.
See [ADR-393](adr/393-managed-exclusive-operations.md)
and the [implementation checklist](implementation/managed-exclusive-operations.md).

## Retiring a policy

Finish or cancel pending and running operations, then remove each producer with
`gregale operations unbind-trigger`. Retire the idle policy with
`gregale operations policy retire crm-sync --json`. A 409
`operation_policy_in_use` means work or a trigger binding still uses it.

Retirement is permanent and idempotent. It releases an active-policy quota slot
and preserves the policy ID, operation receipts and ownership generations. The
retired record remains listed; its name cannot be recreated or accept new work.
