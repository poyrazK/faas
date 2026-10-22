# ADR-1030: Policy-driven storage canary verification

Status: Accepted

## Context

Stored release policies describe required storage operations, but `verify --all`
only lists buckets and skips write-only credentials. Operators must manually
select each writable prefix, pin the deployment and then run a policy-aware
preflight. A listing pass alone cannot qualify a write requirement or establish
application adoption.

## Decision

Add `bindings verify APP --release-policy` with optional `--deployment ID|vN`,
`--dry-run`, and explicit `--object-storage-write` consent. Read and validate an
enforced scope policy containing storage requirements, confirm the app identity,
select one exact live deployment, and build a sorted bounded operation plan.
Every prefix must identify one active binding with sufficient permission and
confirmed settled rotation. Incomplete inventory and unknown configuration
stamps block execution. Confirm explicit deployment selection even when discovery
initially selected the deployment implicitly.

A dry run returns the plan and required consent without tasks or provider calls;
it is never a verification pass. If any planned write lacks consent, execute no
part of the batch. Read requirements use the existing bounded listing command.
Writable requirements use the existing upload/cleanup command with the binding's
exact permission; a read/write binding also downloads bytes when the minimum
requirement is write. Reuse existing admission permission, retention and live
deployment checks. No new API, state ownership, schema or SDK contract is needed.

Reconfirm app identity, policy revision and semantic content, the app-wide
configuration timestamp, required binding metadata and explicit deployment/scope
before each admission and after each successful canary. Verify task identities
and deployment receipts. Stop on the first failure or observed configuration
change. One overall timeout bounds discovery, tasks and final preflight. Interrupts
request bounded cancellation; ordinary wait timeouts leave the admitted task
running and retain its ID rather than submitting a replacement.

After all canaries pass, evaluate fresh inventory through the original snapshot
using the stored verification age, ACK requirements and operation requirements.
Return one JSON document containing the plan, attempted probes, original policy,
snapshot confirmation and final preflight. Other binding families and application
ACK blockers remain visible. Never synthesize application acknowledgements or
alter traffic, policies, bindings, credentials or rollout configuration.

## Consequences

Release pipelines can collect explicit storage evidence and inspect remaining
readiness blockers with one command. Existing single-prefix verification and
`--all` retain their behavior. Preview success means the observed plan was valid,
not that the provider, workload or release passed.

Inventory and CLI confirmations are best-effort observations, not atomic locks.
Configuration can change after a confirmation and before task admission or during
execution. Existing server-selected task metadata fences stale evidence, and
traffic promotion retains its locked policy/catalog/evidence checks. This command
neither freezes credentials nor gives authority to bypass those release gates.
Canaries run in deployment-attached task guests; they do not establish resident
application adoption or a complete production/GCS qualification.
