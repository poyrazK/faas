# ADR-1659: Publish prebuilt Issues source maps during CLI deployment

Date: 2026-10-17

Status: Accepted

Extends: ADR-1456 (deployment-bound Issues source-map artifacts)

## Decision

Add opt-in `deploy --issue-source-maps PATH --issue-source-map-directory PATH`
to the single-source CLI deployment flow. Read the portable manifest from the
final snapshotted source archive, relative to the selected workload root. Its
directory is the bundle root. Reuse the existing compiler to validate, bound,
sort and sanitize mappings. Hash every referenced uploaded bundle before any
deployment mutation, with Node discovery's 16 MiB per-bundle and 64 MiB aggregate
limits centralized in `pkg/api/limits.go`. Never read a working-tree manifest to
describe a HEAD upload or override packing exclusions implicitly.

Require zero production traffic and reject modes that cannot identify one local
source deployment. Submit the existing deployment request with zero traffic,
then publish with the existing authenticated artifact API for its confirmed ID.
Match its source-archive digest and build root against the verified upload even
with explicit idempotency keys; a replay for different bytes or a different root
must not attach this manifest to the wrong deployment. Missing or mismatched
deployment binding produces inspection guidance and no publication recovery command.
Recheck those properties with a fresh deployment read: an old queued receipt must
not authorize publication after a later promotion. Concurrent operator changes
remain subject to the existing API semantics; the CLI does not lock traffic state.
Require matching ready metadata, deployment identity, canonical digest and map
count. Include the digest in automatic deploy idempotency intent. No server API,
permission, migration, build sandbox or VM lifecycle change is necessary.

An ambiguous PUT failure may be reconciled by a current GET confirming that exact
artifact set. Definite client rejections, including authorization and conflicts,
stay failures. Never blindly retry writes or create a new deployment to recover
publication. An unconfirmed result returns an ordinary nonzero deploy error with
stage `source_maps`, the accepted deployment ID, a status command and a command
using the existing artifact publisher. Save the sanitized manifest in a private
temporary recovery directory (0700, file 0600); report a fallback if local saving
fails. The user removes this snapshot after recovery. Existing manifest-setting
compensation on deploy failure is unchanged.

Only after confirmed publication continue the existing wait or emit the queued
receipt. `--no-wait` skips the build wait, not artifact publication. Add optional
artifact metadata to deploy receipts, including timeout/terminal failure receipts.
Human output reports readiness before the existing deployment progress. Deployment
success and promotion remain governed by the existing readiness and traffic flows.

## Consequences

Prebuilt bundles no longer need a separate publication step for each deployment.
Local, committed/worktree, explicit-archive and workspace source selection stay
authoritative. The manifest must be beside its bundle tree and present after
packing; excluded dist output needs an explicit prebuilt archive. Empty discovery
is refused rather than advertised as symbolication coverage.

Zero production traffic prevents automatic production promotion; it does not
prevent startup, readiness, preview or worker execution. Publication and deployment
creation are not one transaction. Missing-artifact captures keep ADR-1456's rejection
semantics. This does not enable exporters/grouping or attest bundle transformations
inside a later server build. Customers use exact prebuilt bundles and runtime paths.
Indexed maps and extraction of artifacts generated inside builder microVMs require
separate work; customer build scripts are never executed on the CLI or host.

No image/repository/project/function-set/template/developer-sync/preview/create-only
path silently ignores these options. Canary/safe rollout controls are incompatible
with this zero-production-traffic flow. Existing explicit publication still works.

## Validation

Archive/workspace and snapshot identity, missing/modified/invalid/oversized maps and
bundles, packing exclusions, local flag refusal before API calls, exact deployment
publication order, queued/waited receipts, lost-response reconciliation, definitive
rejection, mismatched readiness metadata, private sanitized recovery and publication
retry without deployment resubmission. Existing deploy/source-selection, dark
rollout, idempotency, manifest compensation and source-map management regressions.
