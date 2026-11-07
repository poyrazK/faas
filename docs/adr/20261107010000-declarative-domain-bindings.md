# Declarative domain bindings and guarded moves

Status: Accepted

Date: 2026-11-07

## Context

Deleting and recreating a verified domain loses ownership and certificate state
and introduces a routing gap. Binding changes need a reviewable diff and must
honor the wildcard, tenant, access and traffic publication guards.

## Decision

Expose `POST /v1/domains/plan` and `POST /v1/domains/apply` over a version-1
manifest with at most 20 explicit bindings and a 128 KiB request limit. The CLI
provides `domain plan --file --out`, `domain apply --plan --confirm`, and
`domain move --app [--environment] --dry-run|--confirm`. Use a separate domain
file; platform and application deployment manifests are unchanged.

Planning reads one consistent snapshot without writes, DNS probes or certificate
requests. It reports create, move and noop actions. Unlisted domains stay
attached. Removal, default selection and DNS-proof takeovers retain their
explicit APIs. Apps must share an account namespace and organization. Every
source and destination is authorized; storage repeats ownership and environment
checks under the access fence.

The fingerprint covers the canonical manifest, public diff, private claim
identity, binding version, verification/default selection and serving destination
release/deployment/traffic set. Polling and certificate renewal do not invalidate
a review. A nullable UUID binding version changes on every move, fencing a move
away and back without rewriting existing rows. New rows get a random version;
a trigger also versions binding/proof changes by older control planes. Reusing
an account-bound DNS proof after deletion or takeover cannot revive an old plan.

Apply takes the existing batch traffic binding transaction and pending-claim
billing-subject session lock before its repeatable-read snapshot. It locks apps,
recaptures the reviewed state, checks the digest, repeats public wildcard and
tenant exclusions, validates final routing, and commits all bindings together.
Any rejection rolls back the entire set. New claims use normal quotas and the
verification lease.

A move requires verified ownership and a serving destination in the chosen
scope. Environment targets follow their active release graph. Internal apps,
previews and jobs cannot receive bindings. Moving retains TXT proof, verification,
certificate state, wildcard allowlists, known hosts and issuance budgets. It
clears the source default selection and legacy redirect, invalidates cached
doctor observations, advances the observation fence and queues a fresh check.
Probe identity includes the environment; observations started before a move
cannot replace its observation fence.

Request-bound successful-response caching runs after authorization. Failed
responses are re-evaluated after repair, including publication conflicts whose
plan fingerprint remains unchanged. The Go client uses the plan fingerprint as
Idempotency-Key. A crash after the database commit but before
caching its response makes the old plan stale; inspect bindings before another
review. The CLI compares the complete public diff with a fresh plan before apply,
including edited before/after fields. Reusing an already-applied CLI artifact
therefore requires a fresh review.

Responses include previous bindings as a manifest for a separately reviewed move
back. This does not remove newly created claims or restore default selections.
The CLI saves it with `--rollback-out` before sending apply, and refuses to
overwrite an existing artifact. Saving failure leaves bindings unchanged. If
apply then fails or is interrupted, the file remains available for inspection.

## Consequences

Atomic publication is not a zero-downtime application guarantee. A serving
deployment is control-plane evidence, not an HTTP smoke test. Guided production
cutover remains the owner of public DNS/TLS, exact-deployment response proof and
traffic-budget checks. Apply does not edit provider DNS, deployment traffic or
application policy. Publication policy can change after planning and is always
validated at apply.

Cross-account transfers, implicit prune, apex/www groups, provider DNS automation
and pre-provisioned TLS remain separate features.
