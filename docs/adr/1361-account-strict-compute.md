# ADR-1361: account strict compute with shared quantity allowance

- **Status:** local implementation candidate; native release gates remain open.
- **Extends:** ADR-566 and [strict app compute](20261017-strict-app-compute-reservations.md).
- **Decision:** enable account-scoped, compute-only `strict` / `net_usage` /
  `suspend_workloads`, zero drain and no notification thresholds. The account
  policy covers every app, worker and deployment, including newly created apps.
  App `gross_usage` policies and account policies must all fit atomically.

## Quantity and monetary authority

One durable account book per UTC month shares included MB-seconds across every
covered attempt. Account-row locking serializes grants, renewals, settlement,
policy edits and unsupported job creation on all nodes. App books retain their
independent gross commitments. Both books and the attempt receipt commit together;
a denial persists its policy hold and a zero-funded receipt without consuming
either allowance or money. Request identity, owner fencing, physical retirement,
deadline enforcement and funded billing remain those of the app contract.

Allowance is the largest effective recorded compute grant in the period, never
the sum of plan grants. A downgrade cannot revoke it. Positive historical
quantity, including deleted apps and former independent jobs, seeds the account
book. Negative corrections cannot grant execution. All historical compute rates
must fit the fixed paid compute ceiling; missing or higher contracts refuse
activation. New unsupported prices cannot produce funded billable seconds.

The book tracks committed and reserved quantity. Included committed quantity is
`min(grant, committed)`; included reserved quantity is
`min(grant - included_committed, reserved)`. Every lease has its own committed and
reserved quantity, retained after timeout until physical absence is confirmed.
Expired reservations never become free based on a clock or daemon restart.

Paid authority is a conservative integer ceiling of
`max(0, total_quantity - grant) * paid_rate`, rather than assigning free usage to
individual leases or price versions. The accounting ledger proportionally
reallocates allowance across historical versions (ADR-566). Since every supported
rate is at most the paid rate, the sum of floored net charges cannot exceed this
quantity bound. Changing the mix of plans cannot turn previously granted free
seconds into unfunded paid execution. Monetary commitments never decrease; a
larger allowance can reduce future reserves but cannot erase committed authority.
The API exposes paid committed/reserved/remaining millicents and included
granted/committed/reserved/remaining MB-seconds separately.

## Activation, coverage and recovery

Initial activation and re-enabling require quiet account workloads, no unsettled
account attempts, retained complete sampling from account creation or the UTC
month start, recorded compute contracts, and fresh native owners on every active
node. A new month cannot issue shared allowance while old physical attempts remain
unsettled. Revision edits do not reset books or release physical funding. A manual
hold survives the period boundary; `next_period` releases only in a new UTC month.
An explicit revision edit releases that policy's hold, while other policies and
account payment/security state remain independently enforced.

Independent jobs do not yet use native compute authority. Account activation
therefore refuses any nondeleted job definition or queued/claimed job task, and
job creation refuses active account strict policies under the same account lock.
This includes image-run cache and environment-created jobs. Builder VM usage
remains outside this app compute contract. Subscription, storage,
egress, builds and tax are excluded; this is not an invoice ceiling.

Native KVM deadline, owner crash, failed physical retirement, actual two-host
contention, full daemon graph, workload matrix and leak acceptance remain release
gates in the [strict compute runbook](../runbooks/financial-strict-compute-acceptance.md).
Portable memory/Postgres tests establish financial serialization and recovery,
but do not substitute for those native gates.
