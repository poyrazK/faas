# ADR 994: Epoch-fenced durable registry metadata cleanup

Status: implemented behind an operator flag; native acceptance pending.
ADR 995 supersedes the write hold for new schema-6 cleanups. Existing schema-5
operations retain the hold described here until completed.

Registry shards are immutable and content-addressed, but hashes can be reused.
Deleting an unreferenced hash based on one root snapshot is unsafe: a delayed
worker can delete a newly referenced copy. Provider DELETE has no transaction
with quota-root CAS. Cached data also must not serve as reachability authority.

Rotate the complete live registry into a monotonically increasing epoch and
publish schema 5, new references and a scope write hold atomically. Old-schema
writers reject the root; in-flight old CAS writes conflict. Every subsequent
registry write stays in the current epoch. A bounded cleanup worker only deletes
canonical keys in strictly older epochs, never current/future keys. Legacy v1
keys become permanently obsolete after the first cutover.

Allow maximum lease plus invocation timeout for readers to drain before deleting.
Persist operation/source revision, actor/time, barrier epoch, numeric checkpoint
revision, phase, cursor and observational delete count. Two cleanup/verification
passes, one for legacy and one for epoch keys, precede releasing the hold. An
expired cursor can reset the passes; unknown writes require inspection. Delayed
workers remain harmless after release because older epochs can never be live
again. Permanent retirement fences and recovery-byte charges are copied intact.

The cost is an explicit per-scope admission pause and 16 shard uploads per
rotation, even when hashes are unchanged. Interrupted work has a permanent,
resumable hold with no cancellation/expiry. Current-epoch or future provisional
objects are retained until a later successful rotation. Arbitrarily delayed
uploads can leave safe orphans for later collection. Cleanup counts are not
customer quota credit. Production needs provider and multi-node acceptance.
