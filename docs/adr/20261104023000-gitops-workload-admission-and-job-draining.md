# ADR-20261104023000: GitOps workload admission and Job draining

- **Status:** accepted
- **Date:** 2026-11-04
- **Related:** ADR-202610171905, ADR-20261103200001, ADR-20261103210001

## Context

GitOps workload reservation selected the creator's personal organization, so
PostgreSQL rejected new workloads in company projects. Adoption checked explicit
app names but missed duplicate physical identities hidden by persisted logical
mappings. Job updates waited for active tasks before stopping schedule admission;
an overlapping schedule could therefore prevent an environment update forever.

## Decision

1. Resolve new workload ownership through the existing locked project admission
   path. Preserve the project's storage namespace and organization.
2. Validate physical app uniqueness across persisted mappings and desired logical
   workloads during observation. Candidate, adoption and enforcement plans all
   report the conflict before changing ownership or intent.
3. Before applying changed workload intent for a linked Job, commit a schedule
   pause under the current enforcement lease and reviewed plan. Return pending
   drain steps until all queued and claimed tasks finish. Repeated attempts are
   idempotent, and stale plans, superseded leases and report mode cannot pause
   Jobs. Intent apply and later qualification/activation retain their existing
   guards; this preparation step cannot activate a release.
4. A paused Job's already claimed scheduled tasks retain service identity only
   while the existing exact task, frozen inputs and active release checks pass.
   Pause stops future occurrences; it does not cancel accepted execution or
   change its inputs. Qualification and activation reopen the updated schedule.

No stored columns or migrations are added. Production enforcement keeps its
existing native acceptance gates.

## Validation

Exercise company workload reservation and duplicate resolved mappings against
MemStore and PostgreSQL. Exercise image and source scheduled Jobs with modeled
qualification receipts: stale preparation is rejected, busy Jobs pause durably,
new occurrences are denied, accepted tasks retain their identity, and drained
updates keep the same Job identity paused with the approved inputs. These
control-plane tests do not establish native VM acceptance.
