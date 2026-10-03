# ADR-477: Immutable regional deployment targets

Date: 2026-10-03
Status: Accepted.

## Context

Spec §4.2 makes apid the owner of deployment intent and accepts image and source
inputs through the regional deployment pipeline. ADR-475 identifies each
independent cell. ADR-476 stores verified regional app UUIDs in the global
directory. A slug can be renamed or reused after deletion. Resolving a binding
and then posting to its slug could deploy to a different app.

## Decision

Regional apid exposes `POST /v1/cells/{cell_id}/apps/{app_id}/deployments`.
Both path IDs are canonical, nonzero UUIDs. The configured immutable cell must
match the supplied cell. apid loads the app by UUID, checks its local account
and active personal organization ownership, and rejects deleted apps. This
initial adoption path accepts regional personal-account API keys with admin or
deploy-write scope. Shared organization, app-scoped, platform-tenant and session
principals need separate global authorization work before using this route.
The normal authentication, MFA and verified-email gates still apply.

These checks run before idempotent replay. A retry cannot replay success after
the target is deleted, the key is revoked, or the request goes to a different
cell. Existing idempotency keys are already scoped by account, method and full
path, including both UUIDs. The existing 24-hour replay contract applies.

The resolved app row stays on the request context through preflight. No slug
lookup selects the deployment target. Both JSON images and multipart source
uploads reuse the existing plan, signature, build, notification and audit
pipeline. The store's existing app-row lock checks the same app UUID before
inserting deployment intent, so deletion between lookup and insertion cannot
redirect work to a replacement app. A rename preserves the app identity.

Go, Node and Python clients expose this endpoint and refuse redirects for its
requests, including when the caller supplies a client configured to follow
redirects. A cell change requires a new explicit request, never a redirected
mutation. Credentials remain regional; a directory token is not forwarded.

## Scope and consequences

This primitive lets a future coordinator deploy to verified identities safely.
It creates regional deployment intent; it does not reserve global quotas,
build once across cells, replicate data or secrets, publish global ingress, or
report a multi-region release as ready. A successful response is regional
acceptance, not successful runtime rollout. Serving from all selected eligible
regions remains the global default from ADR-476.

## Validation

Exercise the actual HTTP and Go SDK flow against two independently authenticated
TLS cells. Cover wrong cell and account, malformed/noncanonical IDs, disabled
identity, deleted apps and slug reuse, rename, revocation and scope/MFA gates,
retry isolation by both UUIDs, image validation, source upload and plan caps.
With private PostgreSQL, pause after identity lookup, rename and delete the
app, and reuse its former slug (soft deletion retains the current name).
Then prove the store refuses the original UUID without inserting a
deployment for either app. Check replay rejection after deletion and that no
second slug lookup is made. SDK tests must prove redirects are refused for
image and source requests. Native build/wake and regional failure acceptance
remain required for the complete multi-region feature.
