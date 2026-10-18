# ADR-1243: Reviewed Git source staging through management MCP

## Status

Accepted. Extends ADR-1241 and ADR-1242.

## Context

Management MCP stages immutable container images, but agents also need to build
an exact Git revision. The existing source-ref endpoint resolves mutable refs
and applies repository declarations. Executing it directly from an image-style
release plan would change configuration outside the review and allow a moving
branch to replace the source the agent reviewed.

## Decision

Add a read-only, account-owned source review API. Verify a connected GitHub
installation and its immutable repository ID, resolve a branch to a canonical
commit SHA, and return the build root and selected environment's release review.
Full commit SHAs are accepted directly; tags require their exact commit SHA.
Planning neither downloads source nor creates customer work.

Add `gregale_plan_source_deploy` and reuse apply, wait and promote. Plans bind
the account/profile, app/environment IDs, configuration and serving baseline,
provider identities, root and exact commit. Source plans last the existing
fifteen-minute review window plus the platform's fifteen-minute build budget.
Connection expiry and consent remain independently enforced.

An `expected_source` request opts the native source-ref endpoint into reviewed
mode. Require the exact canonical commit, zero traffic and no rollout overrides.
Verify repository and installation identities before and after the fetch;
require terminal stream metadata to match the reviewed commit and archive size.
Run existing archive validation and secret scanning. Do not apply repository
configuration, trigger, workflow, operation or release-command declarations.
Retain current target workflow/operation declarations from the exact reviewed
serving release. Resolve target settings through the existing named-environment settings API and
pass its release review to transactional deployment admission. Named admission
freezes the first desired revision as it does for image staging. Return the
committed staging review alongside the queued deployment/build identities.

The existing build queue, builder VM pipeline, quotas and lifecycle own execution.
MCP wait includes the exact build's lifecycle after verifying its deployment
identity. Build success remains distinct from deployment readiness. Manual
promotion retains configuration/serving guards and rejects protected targets;
protected promotion uses the existing human-approved environment workflow.

Persist source plans and original attempts in the encrypted bridge journal and
reuse the same native idempotency key and body on retries. A partial native
enqueue failure fails closed on the original review rather than inserting a
second candidate. Retain source files on ambiguous reviewed enqueue errors so a
possibly committed build is not deprived of its source. Operators inspect native
deployment/build state before replanning after a partial enqueue failure.

## Consequences

Git configuration and declaration changes remain native configuration workflows.
Service/worker rollouts and local uploads remain separate features. Portable
tests cover immutable refs, provider drift, stale/default/named admission,
configuration preservation, build identity, restart retries, and client catalogs.
Live builder-VM/source acceptance remains a separate native-host qualification.
