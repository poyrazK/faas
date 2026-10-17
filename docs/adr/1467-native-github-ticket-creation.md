# ADR-1467: Native GitHub ticket creation receipts

Status: Accepted
Date: 2026-10-14

## Context

Native status operations reuse the app owner's GitHub installation (ADR-1466), but ticket creation still requires local provider credentials. GitHub issue creation has no idempotency key: a timeout or server crash after dispatch must never authorize a second create.

## Decision

Add explicit POST `/v1/apps/{slug}/issues/{issue_id}/native-tickets` and `issues tickets create --native`. Require Issues entitlement, deploy-write and session MFA. Reuse the owned installation, pinned bound repository and visibility checks. Keep installation credentials server-side. Require explicit repository, title and a stable request UUID. Hash the normalized inputs and immutable connection identity; changed inputs or rebinding conflict with the retained receipt.

Persist account-scoped request receipts before any provider write. Atomically claim `ready` receipts into `dispatching`; fence writes with a random claim identity. A dispatching receipt never calls create again. After a 15-second claim window, an explicit recovery URL can claim a new recovery attempt that verifies the operation marker using the existing provider client. Stale claim completions are refused. Only an explicit provider rejection permits returning to `ready`; timeouts, invalid response identities and persistence failures retain uncertainty.

Persist confirmed ticket identity as `created` before attaching it with the existing quota-checked and audited tracker-link store. Return `created` when attachment fails, allowing an identical request to retry attachment without contacting the provider. Return `linked` once the receipt records attachment. Linked receipts remain creation evidence even if the user subsequently removes the link; retrying never creates another ticket. Keep recovery receipts until the original issue, app or account is deleted. Cap retained receipts at 100 per original issue, defined in `pkg/api/limits.go`; existing IDs can retry at the limit.

Do not retain credentials or ticket descriptions in receipts. Native mode rejects local token and journal flags. Existing explicit provider creation remains available with its private local journal. Provider calls remain bounded, use fixed HTTPS origins, and sanitize diagnostics.

## Consequences

Receipts support retries from another CLI host and survive apid restarts. A crash before the provider receives a dispatched request can still require manual investigation: absence of a discovered ticket is not proof that another create is safe. API and provider writes are separate transactions; connection changes after checks cannot be made atomic with GitHub. Confirmed ticket evidence survives attachment failure or regrouping; attachment follows the requested issue's current canonical group. Link quota failure can leave a confirmed external ticket awaiting manual triage. A changed binding prevents replay until the original connection is restored. Linear connections and continuous reconciliation remain separate work.

## Validation

Mocked provider tests cover confirmed creation, uncertain outcomes, recovery claim fencing, changed inputs/connections, explicit rejections and attachment-only retries. PostgreSQL API tests cover durable replay, reference attachment, reporter denial, repository scoping and stale claim rejection. SDK and CLI contracts preserve stable request identities and recovery URLs without provider credentials.
