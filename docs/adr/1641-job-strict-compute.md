# ADR-1641: strict compute funding for independent job attempts

- **Status:** local implementation candidate; native release gates remain open.
- **Extends:** [ADR-1640](1640-account-strict-compute.md) and ADR-566.
- **Decision:** include batch, scheduled and image-run jobs in account net compute
  authority; support compute-only job `strict` / `gross_usage` /
  `suspend_workloads` policies with zero drain and no alert thresholds.

## Attempt and funding authority

Jobs have no app identity. Job books use a separate durable table, and financial
leases carry mutually exclusive app or job identities. A job lease also binds
the run, task index, scheduler lease token and physical instance. An immutable
claim receipt records these fields before the boot RPC, including for legacy
schedulers. Admission verifies the current claimed task, active run and job,
node incarnation and exact billable RAM. Job and run ownership must both match
the immutable claim account, including legacy and bulk-copy writers. A replacement native incarnation cannot
fund a prior unstarted claim; schedd must create a new attempt. A late boot after cancellation, retry or retirement
cannot use a previous claim. Cancellation can resolve the immutable receipt by
instance even after task projection changes or the native owner restarts.

The existing account lock serializes claims, policy mutation, reservations and
settlement. The job gross book and account quantity/paid book commit atomically.
Every matching cap must fit. Negative receipts and holds consume no allowance.
Each retry uses a new physical instance and claim token. Earlier physical
attempts retain their money until native cleanup confirms absence. Policy edits,
deletion, UTC resets and negative billing corrections never erase commitments.

vmmd acquires funding before allocating a job slot, journal or network. Jobs
use the same monotonic local deadline, renewal and physical retirement contract
as apps, including held-start and interactive jobs. Internal qualification jobs
remain outside customer billing and require their existing authenticated frame.

## Activation and mixed versions

Initial activation requires quiet physical workloads, complete retained sampling
and current capable native owners. Job definitions and queued tasks may exist;
claimed tasks and unsettled physical leases prevent initial activation. Before
claiming a new period's first task, schedd prepares the books while the task is
still queued. Preparation grants no execution authority.

A separate `job_compute_incarnation` attestation is required. The proof must
match the live node incarnation at activation, claim and dispatch. Legacy
registrations preserve an existing proof and fail their incarnation CHECK on
downgrade. Database claim triggers require the selected owner capability for
capped jobs, including legacy writers; definition and queue writes remain
serialized with activation. Do not roll back these migrations while caps exist.

## Holds, billing and visibility

Queued tasks held by either account or job policies are deferred without
consuming an attempt. Their existing submission and start deadlines still apply. A policy that cannot fund a new window persists a hold;
existing jobs stop renewing and retire at their funded deadlines. Customer
cancellation, payment, security and ordinary job pause state remain independent.
Manual and next-period holds follow the existing revision and UTC rules.

Job sampling clips residency to funded windows and the recorded rate ceiling.
Unbudgeted job attempts remain billable under their ordinary contract. Retained
job history seeds gross job commitments and consumes the one account allowance,
including deleted jobs. API, SDK, CLI and console expose job gross balances and
holds through the existing financial budget surface.

## Qualification

Memory and PostgreSQL conformance must cover mixed app/job contention, all job
kinds, independent and combined caps, claim spoofing, cancellation and late
replay, retries, owner restart, UTC reset and billing after delayed cleanup.
Native acceptance must prove pre-allocation denial, local deadline retirement,
failed-kill funding retention and crash recovery with actual job VMs. Native KVM,
two-host, full-daemon and leak checks remain required before release; compilation
and simulated owners are not execution evidence.
