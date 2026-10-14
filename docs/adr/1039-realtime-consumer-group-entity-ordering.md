# ADR 1039: Per-entity consumer-group ordering

Status: accepted for preview implementation; migration/runtime validation pending.

## Decision

Configure immutable `ordering_metadata_key` and `dead_letter_policy` when creating
a group. Empty field disables ordering and preserves existing groups. Otherwise,
use an exact valid metadata field of at most 64 bytes. Its snapshot value fixes an
entity key at assignment; missing and empty values share one bucket. Values are
case sensitive. The existing metadata contract bounds values to 128 bytes.
Default dead-letter policy is `block`; `skip` explicitly lets later same-key
messages proceed after exhaustion. Policies cannot be changed on a live group;
create another group with an explicit cursor when a different policy is needed.

While holding the existing group lock, select the lowest eligible ready sequence.
An ordered message is ineligible if any same-key lease exists, or an earlier
same-key ready/leased message exists regardless of retry delay. Earlier dead
messages also block under `block`. Expired leases are normalized first through
existing expiry/attempt handling. A partial unique index on ordered leased rows
provides a second guard against simultaneous same-key leases across API replicas,
batches and account/scoped workers. Different entity keys may execute concurrently.

Assign retained events into the existing bounded snapshot window in sequence,
looking ahead until an eligible key is found or the 128-snapshot cap is reached.
Assignment cursor advances for snapshots, not processing receipts. Batches can
claim several keys but never two concurrent items of one key. Ready snapshots
remain counted in ready even while blocked; status exposes their blocked count.
Batch `ordering_blocked` is normal backpressure, so workers and long polling keep
waiting without interpreting it as a fatal capacity error. Single claims return
204 when only ordered work is blocked. A hot/failed key can fill the window and
prevent discovery of independent keys beyond it; this bounded preview does not
promise unlimited partition fairness.

Completion deletes the predecessor and releases that key. Retry preserves the
predecessor and its place. Under `block`, replay repairs a dead predecessor before
later same-key events can start. Under `skip`, explicit replay may execute an older
failed message after later work completed; original ordering cannot be restored.
Reject replay while another same-key lease exists. Replay does not bypass claim
ordering or permit concurrent execution. Deleting a group removes ordering state.
Pause/drain, long polling, credentials, gap recovery and lag alerts retain their
existing contracts. Blocked work contributes to pending age/lag observations.

Ordering concerns current lease ownership, not exactly-once effects. Expired or
uncancellable handlers may keep executing after replacement, and external effects
still require idempotency/fencing. Events edited before assignment use current
metadata; the assigned snapshot key is immutable thereafter. The inspector and
generated Node/Python models expose policy and blocked status; existing worker
helpers need no new permission or scheduler option.

## Deployment and validation

Apply `20261012102700490_realtime_consumer_group_ordering.sql` after group/drain
migrations before activation. Downgrade removes ordering guarantees, so stop
ordered workers before reverting. Go/Node builds and static specification/generated
Python checks are the validation scope. Runtime, migration and inspector-rendering
checks remain deferred by request.
