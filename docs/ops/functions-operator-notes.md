# Functions: operator notes

Qualification status behind the customer [functions page](../functions.md).
It moved here so that page can be published (#5709).

## Native pool ownership and warm captures

In the dedicated native acceptance build, resource-aware SDK pools and the shipped database adapter register with
the generated Node adapter before their driver factory runs. Default lifecycle
endpoints drain and rebuild pools across every loaded interpreter. Custom hooks
are preserved: before-checkpoint runs, including its response body cleanup,
before pools drain; after-restore runs after fresh pools pass their read probes.
The registry allows 16 pool owners per interpreter, independently of connection
and plan limits. A timed-out close retains registration until actual drainage.
Qualified warm captures hold admission through paused publication and a fresh
all-worker recovery pass before the original VM continues serving. Failed or
cancelled capture also recovers; an unconfirmed recovery retires that VM.
The qualification path also supports measured migration-source capture and fenced
rollback after cancellation or lease expiry, using separate short-lived capture
and recovery permissions checked against current admission inputs. The destination
qualification path verifies captured bytes and fresh SQL resource recovery, keeps
execution closed until ownership commits, and retires the destination before a
source rollback can resume. Automatic registration and destination activation
remain off in normal production adapters; two-host native VM/SQL qualification
remains pending.
Automatic ownership requires the updated SDK runtime or regenerated bundled
helper. Direct `pg.Pool` instances and older helper versions are not registered
automatically.

The production completion fence covers the handler and its response stream.
Await work that must finish with the invocation. A negotiated Node background
completion protocol now has portable coverage for real `ctx.waitUntil()`
promises, early HTTP response completion, task limits and cancellation cleanup.
Acknowledged VM registration and VM-wide checkpoint admission are implemented
behind a native acceptance build gate. Production runners keep the protocol
disabled until native lifecycle, failure recovery and SQL pool draining are qualified.
The default Node `waitUntil` scaffold therefore still provides no instance
lifetime guarantee. Use a durable queue for work that must survive worker or
VM replacement; bounded post-response work does not provide durable execution.

See [ADR-1540](../adr/1540-function-cancellation-cleanup-and-lifecycle-context.md),
[ADR-20261018005023](../adr/20261018005023-function-background-completion-protocol.md),
[ADR-20261018030610](../adr/20261018030610-function-vm-execution-lifetime.md),
[ADR-20261018030611](../adr/20261018030611-function-postgres-resource-lifecycle.md),
[ADR-20261018030612](../adr/20261018030612-function-warm-snapshot-resource-recovery.md),
[ADR-20261018030613](../adr/20261018030613-function-migration-source-rollback.md),
[ADR-20261018030614](../adr/20261018030614-function-migration-admission.md),
[ADR-20261018030615](../adr/20261018030615-function-migration-destination.md)
and [backend concurrency acceptance](function-backend-concurrency-acceptance.md).
