# Resolve a work inbox follow-up

The Node and Python SDKs can turn a paged or live inbox entry into a typed
follow-up. Enable `work_inbox_follow_up` on the same authenticated application
toolkit as the source operations. The resolver uses the receipt's current state
and the toolkit's publication and scope rules.

## Enable the resolver

Pass the existing [work inbox](sdk-work-inbox.md), its read scopes, and the
existing [toolkit mounts](sdk-application-toolkits.md) to your toolkit.

```ts
import { createApplicationToolkit } from "@gregale/sdk-node/preview";

const toolkit = createApplicationToolkit(mounts, {
  workInboxFollowUp: { inbox, scopes: ["imports:read", "payments:read"] },
});
const next = await toolkit.resolveWorkInboxFollowUp(entry, access, {
  timeoutMs: 10_000,
});
```

```python
from faas_sdk.preview import ApplicationToolkit, WorkInboxFollowUpOptions

toolkit = ApplicationToolkit(
    mounts,
    work_inbox_follow_up=WorkInboxFollowUpOptions(
        inbox, ["imports:read", "payments:read"]
    ),
)
next_step = await toolkit.resolve_work_inbox_follow_up(entry, access, timeout=10)
```

Pass a verified `ToolkitAccess`, as for other toolkit calls. The resolver takes
an inbox entry or a reference containing `kind`, `name`, `request_id`, and
optionally `business_ref`. The MCP tool takes those reference fields. Keep the
business reference when continuing work tied to a particular record.

The PostgreSQL inbox store uses the existing receipt primary keys. Custom
stores can enable exact lookups by implementing `hasReceipt` or `has_receipt`
and matching the supplied identity, application scope, owner, request ID, and
source selector. Pipeline selectors also bind the receipt subject and contract.
The SDK then runs the source's current authorized inspection. It rechecks the
inbox principal before returning the result and applies one request deadline to
the lookup.

## Use the result

| Result | Meaning |
| --- | --- |
| `human_review` | Open the current authenticated link. The toolkit requires access to the corresponding pipeline status tool. |
| `tool_call` | Invoke the returned `name` with its exact `arguments` when ready. The toolkit requires that operation's scopes and that the operation is published. |
| `no_action` | The current state has no supported follow-up, or the SDK could not verify a retained run for an uncertain start. |

For example, an uncertain refund can return the action's `_refresh` operation
with its action ID. If the provider outcome was already recorded, the same
reference resolves to `_observe`. A pending pipeline review returns the existing
review link. A succeeded pipeline can return its `_result` operation with
the original request ID.

When a pipeline start is uncertain, the resolver can offer its published
`_reconcile` operation with both `request_id` and `run_id`. It looks up the
original native start only for callers with the destination scopes and current
application reconciliation authority. The native response is checked against
the durable receipt. The caller makes a separate authenticated call to attach
the checkpoint.

Disallowed or missing receipts and destinations yield `not_found`. The resolver
returns a description without invoking the destination. Use a fresh authenticated
call for that operation, where current business authorization and approval
rules apply. Re-resolve the reference when state may have changed.

The [CSV example](../examples/sdk-csv-jobs/README.md#resolve-the-next-step-from-the-inbox)
runs both MCP hosts with current access checks and uses the returned operation to
reconcile an uncertain refund.
