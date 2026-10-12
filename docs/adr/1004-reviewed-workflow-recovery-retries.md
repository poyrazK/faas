# ADR-1004: Reviewed retries from saved workflow recovery failures

Status: Accepted

A workflow recovery job can finish admission while a resumed workflow subsequently fails. Operators need a bounded second attempt tied to that saved result, rather than selecting a newer failure by accident.

Native workflow recovery requests may specify `parent_job_id`. Each explicit target must include a unique `parent_position` and the resume count belonging to that parent's saved failed outcome. The retained parent must be in the same account and app, target the same workflow, and have completed, cancelled, or expired admission. Only queued parent items with saved failed results qualify. Creation checks the current retained run against that result; admission still atomically checks the reviewed count and resume safety. Success, cancellation, missing evidence, and changed runs do not qualify.

`gregale automations recovery-retry-plan` previews eligibility using read-only calls. Saving requires explicit positions and a new plan key. Plans are submitted through `recovery-start`; local journal execution rejects parent plans because it cannot validate server-owned parent evidence. There is no automatic retry loop.

The child freezes its own targets and historical lineage. Repeating an identical retained child request returns that child before looking up its parent. Parent pruning therefore does not prevent idempotent submission or admission of an already-created child. A new child still requires retained parent evidence. Existing recovery controls, rate limits, outcome capture, and notifications apply without a new database table. An append-only migration broadens the existing child-request constraint to allow native workflow parents; downgrade refuses while such children are retained.
