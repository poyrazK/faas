# ADR-20261018000100: Native running-job rehearsal qualification journey

Status: Accepted; native execution pending, rollout remains disabled

## Context

ADR-20261017201015 binds a running HTTP job's held checkpoint response to an
in-place suspend/resume receipt and an independent dispatch journal. Portable
tests use fake VMMs. The existing metal suspend/resume test exercises a real
guest outside a running job. Neither alone establishes that contained commands,
worker retries, fixture resources and external-action collectors compose on a
native installation.

## Decision

Add a dedicated native acceptance coordinator that drives the existing
`gregale test rehearse` command, without introducing another execution path or
changing lifecycle ownership. It requires a clean checkout matching a root-owned
installed-release stamp, accessible native x86_64 Linux KVM, root-owned host/API/
account enrollment, a dedicated acceptance account and the shared native builder
lock. Before provisioning it requires a clean leakcheck and the existing metal
pause test to execute and pass. It never enables admission flags or stops other
fleet processes.

The coordinator creates a separate, retained Git fixture repository with two
pinned revisions. The candidate changes only a notification retry switch. A
contained setup command checks readiness; a contained trigger admits one managed
HTTP import. Its first attempt commits a database update, uploads a stable CSV,
awaits the held checkpoint, simulates a notification, and returns 503. The real
managed retry must use the resumed VM. Both revisions finish with the same rows
and object bytes; only the candidate simulates a second notification.

Require both baseline-versus-baseline success and baseline-versus-candidate
failure specifically for notification multiplicity. A separate Go verifier uses
the production evidence validators and comparators, checks the independent
lifecycle observation against the sealed receipt, binds both dispatches to the
same instance and resource identities, requires intact contained-command
receipts, and verifies exact expected SQL, object and simulation outcomes.
Skipped, missing, replaced, mismatched or incompletely cleaned evidence cannot
qualify. Reusing execution identities across the four runs is refused.

The coordinator separately observes deletion of all eight serving workloads
through the API and runs leakcheck after the attempt, including failures.
Private reports, fixture commits, logs and a failed-by-default journal remain on
the host. Cancellation interrupts only its own coordinator child process group,
allows owner cleanup to finish, and retains failure if cleanup cannot be joined.
It never reaps resources before checking for leaks. A passing final journal binds
the retained artifacts by hash and identifies the exact installed platform commit.

## Scope and validation

This qualifies one composed warm running-job journey. It is an additional
acceptance prerequisite, not authorization to enable the earlier gates. It does
not replace native cross-run denial, packet containment, credential isolation,
cancellation, collector failure/restart, cleanup-failure and recovery matrices
required by ADRs 20261017201007–20261017201015. It does not claim dependency
outage injection, replacement-worker retries, production snapshots, provider
semantics or a production rollback boundary.

Portable tests validate the fixture's retry/checkpoint ordering, the manifest's
supported scope, report rejection cases, pinned fixture preparation, process
retirement and failure journaling. Compile-only or offline fixture preparation
never publishes native qualification. The dedicated native run remains pending
until its retained evidence is reviewed for the exact candidate release.
