# ADR-20261114203121: Required readiness roles for private publication

Status: Accepted
Amends: ADR-20261114173309, ADR-20261114191145

## Decision

Allow hosted private publication for existing apps with enabled default-environment
MCP readiness roles. Pin the policy revision, endpoint, freshness window and each
role's protocol and catalog in the publication intent. Keep monitors and credential
sources out of publisher-facing plans and receipts. The candidate verification
digest additionally binds the existing readiness artifact fingerprint, including
configuration, ordinary secret delivery versions and managed grant authority.

Require explicit personal-owner `approve_readiness_roles` acknowledgement alongside
the existing candidate test-credential selection. Approval performs no network
request. It is immutable for that verification and invalidated by changed policy,
configuration or credential authority. Routine managed OAuth token renewal does
not change the approval fingerprint.

The existing API worker probes all required roles before behavioral fixtures.
Reuse the readiness protocol/catalog probe and owner-configured role sources:
anonymous, ordinary sealed app secret or a connected monitoring OAuth grant. Never
use readiness credentials for tools/call. Fixtures retain their separate approved
publication credential and once-only execution journal. Each role is bounded to
30 seconds within the existing 90-second worker deadline.

Managed roles reuse the existing durable observer lease and renewal journal. Keep
the lease through evidence recording; recheck publication authority before every
provider or application request. Preserve rotating tokens and refuse to replay
uncertain provider exchanges. Save only fixed probe checks, catalog hashes and
opaque evidence IDs, not raw role catalogs or upstream errors. Reject known role
credential reflections from the full publication catalog.

Recheck the exact role evidence after fixtures and when committing successful
verification. Extend the existing traffic update with optional
`expected_mcp_readiness`, bound to the authenticated app/account. Memory and
PostgreSQL check the current policy, artifact, exact evidence identities, protocol,
catalog and freshness at traffic commit. A newer observation supersedes the
publication receipt even if it passes. Emergency overrides cannot bypass this
additional publication condition. Ordinary traffic calls retain existing behavior.

## Recovery and compatibility

Failed or uncertain verification retains its receipt; retrying does not refresh
role evidence or repeat fixtures. Expired, replaced or changed evidence requires
reconciling the old attempt and explicitly reviewing another publication. Publishing
still creates no invocation grant. Durable Tasks retain their separate workflow.

No migration, VM owner, worker service or agent credential-input API is added.
The readiness evidence query uses sqlc against the existing table. New fields are
optional, preserving the digest shape for publications without readiness roles.
Deploy matching API, CLI and bridge under the existing private-publication gate.
This remains a preview; deployed staging acceptance remains skipped.

## Validation

Local coverage exercises reader/writer catalogs and managed OAuth with memory and
PostgreSQL, both MCP protocol versions, Node/Go/Python inline and archive plans,
owner acknowledgement, credential redaction, failed roles, policy/credential drift,
mid-fixture changes, retained retries, uncertain OAuth renewal, and atomic traffic
conditions including emergency overrides. API and CLI tests cover the owner and
promotion wire paths. No deployed acceptance or general-availability claim is made.
