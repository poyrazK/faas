# ADR-20261114191145: Managed OAuth for private publication verification

Status: Accepted
Amends: ADR-20261114173309, ADR-20261113220000, ADR-20261113230000

## Decision

Allow the personal owner to approve an existing managed execution OAuth
connection for one private publication candidate. The owner supplies exactly one
of `credential_secret_ref` and `credential_connection_id` with the reviewed
candidate request digest. Approval remains under the existing private-publication
operator gate and MFA policy. Approval and status reads perform no OAuth request.

Bind the private approval to the immutable connection ID and authorization digest,
the candidate app and its current project, default scope, exact reviewed issuer
and resource, and coverage of the global and tool scopes. Guided connections must
already have completed owner authorization. Token generation changes during normal
renewal do not change this binding. Connection IDs, source references, leases and
credential values remain absent from publisher-facing records.

The API-owned verification worker claims renewal through publication authority.
It never creates an invocation grant or supplies a fabricated grant to the
invocation renewal path. Both purposes share the existing connection row, encrypted
credential cache and renewal lease. Each provider dispatch rechecks the running
publication, original publishing key, candidate/source/serving identities, owner
approval, connection binding, ordinary source credentials and exact renewal lease.
The existing provider transport, scope checks and redirect rejection remain in use.

Only the lease owner exchanges a refresh token. Concurrent consumers wait within
their existing execution deadline and reuse the durably saved access token. Lost
responses, rejected grants, changed scopes and expired renewal leases require
reconnection. They never authorize an automatic second exchange of the potentially
consumed refresh token. Publication fixtures still run at most once per explicitly
approved publication attempt.

Persist a successful rotating-token response even when the publication approval
was revoked after provider dispatch, provided the connection and source authority
remain valid. This preserves the connection's refresh-token lineage. Revocation
prevents subsequent application dispatch. Revoking the connection also invalidates
its outstanding renewal lease and prevents the response from being installed.

Verification uses one access-token snapshot, with its context additionally bounded
by that token's expiry. Expiry never causes a fixture replay or a mid-fixture token
exchange. Every application dispatch rechecks current connection authority. Known
access, refresh and client-secret reflections are rejected before catalog evidence
or behavioral fixtures are retained.

## Recovery and compatibility

Renewal failures produce a retained `reconnect_required` verification failure
without provider response data. Reconnect creates a new connection identity; the
owner must approve a new publication attempt. Repeated verification requests only
read the retained result. Static sealed test-token approvals remain compatible.

No schema migration, new VM owner, worker service or agent-facing credential API
is introduced. Private invocation still requires its own exact owner grant and
consent. Configured readiness roles and durable Tasks remain separate milestones.
This remains an opt-in preview; live staging acceptance remains skipped.

## Validation

Memory and PostgreSQL tests cover approval without effects, actual access-token
expiry and renewal, native publication finalization, separate agent invocation and
revocation. Additional cases cover mismatched connection contracts, incomplete
guided authorization, shared renewal, uncertain provider replies, changed scopes,
provider rejection, credential reflection and revocation during an accepted refresh.
