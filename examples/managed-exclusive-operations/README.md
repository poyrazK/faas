# Managed CRM synchronization

This manifest declares one customer-scoped coordination lane shared by an
HTTP cron, a signature-verified inbound webhook, and a Kafka trigger. Replace
the example tenant UUID and app slug with active account resources. The
selected cron, webhook endpoint, and broker trigger must already exist.

Apply the policy and all three bindings with:

```sh
gregale operations reconcile --dir examples/managed-exclusive-operations
```

The business key is the same for all sources; the trusted tenant ID is stored
and checked separately. `queue` preserves each accepted request. Change it to
`reject` or `join_existing` only when those are the intended semantics. A
`join_existing` binding must also declare an `equivalence_key`.

Reconciliation upserts only the policies and bindings declared in this file.
Remove a binding explicitly with `gregale operations unbind-trigger` when a
trigger should return to its normal path.
