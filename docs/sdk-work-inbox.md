# Resumable work inbox (SDK preview)

A new application or agent session can discover its existing pipeline and action
receipts even when it did not retain the original request IDs. Node and Python
use the same receipt tables, entry contract, and signed continuation cursors.
The inbox is a typed read tool that can also be used by a dashboard or mounted
as `work_inbox` in an [application toolkit](sdk-application-toolkits.md).

## Set up an inbox

Provide the existing pipeline and outbound action definitions you want to
surface. Mount the pipeline tool that owns a compensation or refund attempt
when you want to discover its waiting gate. Mount the outbound action separately
to discover the current result of the external operation.

```ts
import {
  createQueryToolMCP, defineWorkInbox, outboundWorkInboxSource,
  pipelineWorkInboxSource, postgresWorkInboxStore,
} from '@gregale/sdk-node/preview';

const inbox = defineWorkInbox({
  scope,
  cursorKey,
  store: postgresWorkInboxStore(readOnlyQuery),
  sources: [
    pipelineWorkInboxSource(cleanup.tool, { receiptSubjectId: "csv_cleanup" }),
    pipelineWorkInboxSource(refund.tool, { receiptSubjectId: "csv_refund" }),
    outboundWorkInboxSource(refund),
  ],
  authorize: loadCurrentPrincipal,
  businessReference: loadOwnedBusinessReference,
});
const adapter = createQueryToolMCP(inbox);
const work = await inbox.page({ state: 'awaiting_approval' }, principal, {
  timeoutMs: 10000, pageSize: 10,
});
```

```python
from faas_sdk.preview import (
    PostgresWorkInboxStore, QueryToolMCP, create_work_inbox,
    outbound_work_inbox_source, pipeline_work_inbox_source,
)

inbox = create_work_inbox(
    scope=scope,
    cursor_key=cursor_key,
    store=PostgresWorkInboxStore(read_only_query),
    sources=[
        pipeline_work_inbox_source(cleanup.tool, receipt_subject_id="csv_cleanup"),
        pipeline_work_inbox_source(refund.tool, receipt_subject_id="csv_refund"),
        outbound_work_inbox_source(refund),
    ],
    authorize=load_current_principal,
    business_reference=load_owned_business_reference,
)
adapter = QueryToolMCP(inbox)
work = await inbox.page({"state": "awaiting_approval"}, principal, timeout=10, page_size=10)
```

The inbox authorizer resolves the current stable subject and application owner
before and after each page. The PostgreSQL reader selects only identities and
request IDs for configured sources, the exact app/account/API scope, and that
owner. Pipeline reads also match the receipt subject and configured contract digest.
A normal pipeline uses the authenticated actor’s subject. For tools that
allocate receipts under a fixed subject, pass `receiptSubjectId` /
`receipt_subject_id` from backend configuration. Compensation and refund
workflow tools use their definition name as this namespace.
The SDK then uses the source's existing `inspect` method with the authenticated
principal. Each source applies current business access, so no view can be
inferred from a receipt's presence alone. Assign MCP scopes that cover the data
from every registered source, and recheck entitlement to each operation in its
original adapter. The inbox itself grants no authority to reconcile or approve.

The optional business reference callback resolves a label such as an import ID
from trusted application data after a candidate passes inspection. An absent
label leaves the item available unfiltered and excludes it from specific
`business_ref` matches. The inbox stores no new request or business payload.

## Inspect and continue work

Call `work_inbox` with `{input: {kind?, state?, business_ref?}, after?, page_size?}`.
Responses include `items` and `next_cursor`. Items contain an adapter name, the
existing request ID, the observed state, and a suggested next step. A verified
native run ID, action ID, or review link is included when applicable. The review
link goes through the existing authenticated human review endpoint.

| Hint | Follow-up |
| --- | --- |
| `open_review` | Open the returned human review link. |
| `read_result` | Use the existing pipeline result or action observe operation. |
| `reconcile_start` | Resolve the verified native run from the durable start receipt, then reconcile using the original request ID. |
| `reconcile_action` | Read provider evidence through the action's reconcile operation. |
| `inspect` | Inspect current state through the source adapter. |
| `review_action` | Send evidence that needs review through your application's process. |

The inbox does not start, replay, approve, cancel, or contact the provider.
Observations use the same inspection behavior as the existing lifecycle tools,
including their review expiry handling. Pipeline status and external action
state can change after a read. Retain the returned request or action ID for
follow-up calls, and use current adapter authorization when making those calls.

Pages can be empty with a non-null cursor when scans encountered filtered or
inaccessible work. Continue until the cursor is null or the caller reaches its
explicit budget. The default scan examines up to 24 candidate receipts per page
with one extra lookahead row. Page size is at most 20. The definition may set a
scan budget from 1 to 100 across up to 32 sources. State and business reference
filters apply to verified observations within that budget.

This is a live, deterministic order by receipt digest. The cursor contains a
second hash of the private receipt identity, plus a source kind suffix, so it
does not contain a discarded receipt's identifiers. It binds to the current
principal, filters, and source selection through the query cursor protocol.
Restart from the first page to refresh. Share the same cursor key across
Node and Python. A page shares one deadline across scanning, authorization,
and inspections. Normal query cancellation and iteration limits apply.

## Upgrade and verify

Install the [inbox indexes](../sdk/request-contracts/work-inbox.sql) after their
corresponding receipt tables. They keep scoped reads and keyset scans indexable
and use existing records, including receipts created before enabling the inbox.
The SDK exports the SQL for explicit application migrations as
`workInboxSchema` / `WORK_INBOX_SCHEMA`. Configure database statement timeouts
and use a restricted read-only query role. With a single source kind, install
only its index. Reserve/complete and dispatch protocols stay with their existing
writers.

The [CSV example](../examples/sdk-csv-jobs/README.md#resume-from-the-work-inbox)
runs both authenticated starters and resumes cleanup and refund recovery using
only an import ID at discovery. It uses PostgreSQL and Chrome with native
workflow and Stripe protocol fixtures.

To follow changing entries for one business record, use the
[live work inbox watcher](sdk-live-work-inbox.md).

Use [typed inbox follow-ups](sdk-work-inbox-follow-ups.md) to resolve the
current authorized review or operation from a receipt reference.
