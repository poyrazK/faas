# ADR 997: Active durable registry cleanup discovery

Status: implemented under the existing cleanup worker flag; native acceptance
pending. Extends ADR 996.

Routine scans of every quota root spend provider reads on inactive scopes and
delay unfinished cleanup as root counts grow. Discover active work through
disposable object-storage hints, while retaining an independent bounded root
scan for missing hints, old writers and index outages.

Key each immutable hint by scope hash, strictly increasing cleanup epoch and
canonical operation UUID. Known start acknowledges the root first, then publishes
the hint best-effort. Exact start replay repairs it; known completion removes
that exact hint best-effort. Index outcomes cannot change a known root outcome.
An uncertain root write still requires inspection/replay. Hint bodies provide
no authority and there is no cross-object root/index transaction.

Visit at most four index candidates per normal polling interval. Authenticate
each fresh root and require matching operation, epoch, unfinished phase, reader
grace and the configured app allowlist. Worker claims/backoff and the step's
fresh checkpoint validation remain unchanged. Never create an operation from a
hint. Retain unknown/future/unmatched-current keys and absent/unreadable roots.
Prune only a strictly older epoch or the exact completed current operation.
Epoch-qualified keys prevent delayed removal from deleting a later rotation's
hint even when an operation UUID is reused. Late publication can leave a safe
stale hint for a subsequent pass.

At startup and once per minute, visit up to four quota roots through a separate
rotating cursor. Repair active hints, including during grace, and provide bounded
fallback steps independent of index publication or listing errors. Index and root
discovery maintain separate cursors and bounded backoffs. Both lanes share one
45-second runtime-cycle deadline, with root repair first when due. No new flag,
schema upgrade, automatic cleanup start or production enablement is introduced.

This trades frequent full-root scans for active/stale-hint scans and lower-rate
reconciliation. Large populations, configured app subsets, persistent failures
and stale hints can still extend revisit times. No immediate repair or constant
completion-time guarantee is made. Startup probes bounded index listing and only
its own noncanonical private index key. Native acceptance must cover missed or
unknown publications, old readers/writers, delayed prune/publication, independent
index outages, cursor recovery and realistic request/storage/revisit costs.
