# ADR-20261113181050: Pipeline freshness, durable CLI setup and index advice

- Status: implementation for the approved staging pilot; production disabled
- Date: 2026-11-13
- Amends: ADR-1591 and ADR-20261113160716

## Context

Customers need to know whether a mirror meets their freshness target, resume setup
after closing their terminal, and improve tenant-filtered read queries without
handing an application a writable mirror binding.

## Decision

Add optional, bounded freshness-confirmation targets. Status distinguishes a
recent confirmed checkpoint, an unconfirmed target, unknown monitoring evidence,
and inactive replication. An old checkpoint alone never proves time lag: an idle
publication may not advance its origin. Alerts describe missing confirmation and
never promise a measured source-to-mirror latency. Existing read gates remain
based on the replication and integrity proofs; a freshness target grants no data
access and does not silently change availability policy.

Expose the existing durable setup owner through public account-scoped API and
CLI commands. A job's immutable intent and resource IDs survive disconnects,
duplicate requests and deployment failures. Plan requests inspect configuration
and report billable resources, frozen storage/restore bounds, capacity evidence
and source-compute implications without creating a mirror or app. Pricing that
depends on future use or the customer's source provider remains explicitly
unknown. Existing CLI creation stays compatible; `--durable` and `pipeline setup`
select the server-owned workflow, and `--plan` previews it.

Add structured, bounded index advice for selected mirror tables. Customers submit
column names and ordering, never SQL or row values. Tenant advice uses the exact
C-collated text expression used by the row policy, including for UUID tenants.
Advice identifies its estimated storage and extra write work; estimates are not
measured latency improvements or billing quotes. Only a frozen, revision-bound
preview can be approved. The worker rechecks ownership, table identity, selected
columns, current storage budget and index quota before applying.

Index construction is deliberately bounded for this pilot: a short transactional
build under a table-size, temporary-storage and memory budget atomically records
the owned index and private receipt. It can briefly delay mirror apply. Larger
builds remain advice-only. Timeouts roll back completely and require a new user
decision; they do not start an unbounded background retry loop. Replays adopt only
the generation-owned receipt and exact index identity. Index advice/application
never writes the source or changes reader policies. Index-only acknowledgements
do not refresh replication observations. Source-independent reader revocation
retains priority over index work.

An expanded worker claim protocol fences approved index work and freshness-aware
saved setups from N-1 workers that cannot enforce them. Freshness-aware monitor updates are fenced independently, so N-1 monitors cannot
clear incidents by ignoring a configured target. Legacy serving contracts
remain claimable by their compatible workers. Deploy data, capable workers, then
the new controls. All account, provider, connection-capacity and production gates
remain in force.

## Validation

Verify freshness boundaries and idle/unknown observations, account isolation,
durable setup recovery and dry-run side effects, membership-preserving index
advice, storage/work limits, approval fencing, transactional failure/lost-ack
recovery and actual native tenant query plans. Regenerate schema/SQLC, clone
coverage, OpenAPI/SDKs and CLI metadata; complete the pipeline native acceptance
suite. Live Neon qualification remains a separate prerequisite for a real pilot.
