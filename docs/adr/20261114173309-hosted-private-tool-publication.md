# ADR-20261114173309: Hosted private tool publication with owner-approved candidate tests

Status: Accepted
Amends: ADR-20261113180001, ADR-20261114162429, ADR-20261112210001

## Decision

Allow hosted Node, Go and Python publication with `external-oauth` access. The
review pins issuer, JWKS URL, canonical resource, global scopes, tool policy,
legacy qualification, fixtures and source. The bridge freezes that source and
uses the existing fixed CLI/build path to stage a catalog-pinned, zero-traffic
candidate. It supplies no client token, token environment or secret reference.

Private candidate testing runs in the API through the existing bounded private
protocol executor. `FAAS_MCP_PRIVATE_TOOL_PUBLICATION=1` enables this separately
from delegated invocation. Publishing consent still permits reviewed source
builds, fixtures and guarded promotion. Test credentials require a separate
personal administrator decision under MFA policy for each exact candidate.

The verification request digest binds the publication review, publishing key,
immutable candidate deployment, native source archive and catalog pin. Source
tree digests and packed archive hashes differ, so the owner reviews the actual
native candidate/source identity before approving credential use. The owner
selects an existing ordinary `secret:MCP_*` test token on that app's default
environment. Scope or app/project principals and managed infrastructure
credentials remain excluded. Provider tokens never leave the API or appear in
publisher-facing records, CLI results or agent responses.

Store verification status and private approval metadata in the existing bounded
publication payload. Public journal reads decode only the public record. The
sealed credential source is pinned by ciphertext digest; its reference is
erased when verification finishes. No schema migration, VM owner, source queue
or additional execution service is introduced. One verification attempt fits
within the existing publication/account retention bound and shares the existing
four-worker private executor, with a 90-second execution deadline.

Only a fresh approved begin launches the API worker. Each outbound request
rechecks publishing key, project limits, candidate/source/serving identities,
approval and credential source. Revocation/rotation prevents subsequent dispatch;
an accepted HTTP request may finish. The worker checks OAuth resource metadata,
the exact reviewed catalog, public readiness and public/exact-replica stateless
protocol qualification, behavioral fixtures and catalog integrity. Reports retain
canonical schemas and named check outcomes, with fixed failure-stage codes.
Known credential reflections in the catalog fail verification.

The CLI waits for this native receipt and rechecks the existing catalog fleet
pin and serving baseline before guarded promotion. A successful hosted private
publication outcome must match the API-owned deployment, source, catalog, pin
and fixture evidence. Discovery and invocation still require separate exact
owner grants. Existing private invocation consent and credential bindings are
unchanged; publication never grants a tool to its publisher or another agent.

## Recovery and scope

Pending and accepted verification metadata survives transport loss and bridge
restart. Duplicate requests read the same attempt; abandoned work is never
resumed. A closed parent cannot authorize remaining candidate tests. Uncertain
or failed fixtures may already have external effects and require inspection
before another publication. Revocation can stop pending/running test authority;
completed evidence is consumed and cannot be reauthorized or revoked.

The first preview uses ordinary sealed test tokens. Managed connection renewal,
durable Tasks and configured platform readiness roles are separate work. The
CLI refuses configured readiness roles rather than promoting without their
evidence. The owner must trust the approved application and its deployment
authority with the selected token, as with existing private invocation.

Deploy matching API, CLI and bridge versions before enabling the operator flag.
Existing local private publication remains compatible. No GA maturity changes.

## Validation

Portable tests exercise owner approval, one fixture dispatch, retained receipts,
native finalization, exact granted invocation and revocation against memory and
PostgreSQL stores. Failure cases cover changed source/serving identity, publisher
revocation, credential rotation, reflected credentials and failed protocol or
behavioral checks. Hosted source review tests cover both MCP protocols, all
three runtimes and inline/archive uploads. Live staging remains skipped.
