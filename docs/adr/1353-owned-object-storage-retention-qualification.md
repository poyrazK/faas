# ADR-1353: Qualify format-5 recovery protection with built object nodes

Status: Accepted

## Context

The object qualification runner covers load, uncertain writes, replication,
backup/restore, process recovery and planned promotion. Its initial format-4
fixtures do not exercise the settlement delivery and indexed cleanup added by
ADRs 1350–1352. Independent-host execution also requires operator-selected test
hosts and dedicated data filesystems; a shared development container cannot
supply that evidence.

## Decision

Add an opt-in `--recovery-retention` qualification profile. It enables format 5
only in the newly created synthetic primary, before its seed backup. Followers
receive the barrier through the existing bootstrap and journal protocols. Enable
settlement outbox enrollment in the isolated gateway's in-memory backend
configuration. Existing data directories and live endpoints remain inadmissible.
The default qualification profile and existing host/storage guard remain intact.

Create a dedicated tracked gateway PUT and deliberately lose its first signed
settlement reply after the node commits acknowledgement. Observe only that
probe's bounded response body, preserving bytes for normal adapter validation.
Require the central terminal result to remain committed, the delivery job to
remain ready and a later retry to drain it without replaying object bytes.
Repeated acknowledgements must preserve the first node acknowledgement time.

Advance only the controller's existing in-memory retry clock by the delivery
retry interval. Storage-node clocks, request issue times and sealed retention
records are never backdated or accelerated. Overwrite the probe with a fresh
unacknowledged native tracked write, leaving a historical acknowledged proof and
a current unacknowledged proof to protect.

After maintenance/backup/restore, primary process recovery and planned promotion,
retry the exact saved terminal payload against the active primary. Require a new
reply with the original acknowledgement time, preserved historical/current proof,
and successful catch-up. Drive the ordinary bounded settlement reconciler to
drain terminal ready jobs, retaining waiting unknown jobs. Require enabled,
healthy indexed cleanup, observed worker steps and indexed candidates, with no
removals or cleanup failures during this fresh-proof run.

Version-3 reports gain optional `recovery_retention` configuration and per-mode
evidence with the retention window, lost-reply retry result, acknowledgement
timestamps, checkpoint observations and ready/waiting backlog counts. Explicitly
report `aged_retirement_exercised: false`. The remote agent protocol remains
version 2: it already transfers format-5 backups and starts the current daemon;
no new remote operation or production clock override is introduced. Install
matching current daemon, control and qualification binaries on both participants.

`--require-dedicated-storage` still performs a fresh read-only preflight before
creating fixture data. Distinct kernel boot identities and accepted dedicated
local mounts are required. Reports retain the separate physical failure-domain
limitation. Host selection, SSH authentication and backing-storage inventory must
come from the operator; the runner does not provision machines or select a live
deployment as a test target.

## Consequences

The same profile can exercise real disposable processes locally or through the
existing SSH transport. Local runs validate preparation and process behavior;
they cannot qualify independent hosts, disks or power-loss durability.

A short soak validates fresh-proof protection and persistence across process
failures. It does not exercise eligible 30-day retirement or certify an elapsed
retention interval. ADR-1352's controlled-clock tests cover aged collection and
crash replay locally. The gateway fixture remains in-memory, so PostgreSQL crash
and coordinated central/node restore qualification remain outside this profile.
