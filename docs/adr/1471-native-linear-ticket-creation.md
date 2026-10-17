# ADR-1471: Native Linear ticket creation and UUID recovery

Date: 2026-10-14

Status: Accepted

## Context

ADR-1470 stores owner-scoped Linear personal API keys. Ticket creation needs the
durable recovery guarantees already provided for native GitHub creation, without
requiring a caller to know a ticket URL after an interrupted provider response.

## Decision

Expose POST `/v1/apps/{slug}/issues/{issue_id}/native-linear-tickets` and support
`issues tickets create --native --provider linear`. Require explicit title, team
UUID matching the stored connection and a UUID v4 request ID. Use that request ID
as the Linear issue UUID. Require app ownership, Issues entitlement, deploy-write
and session MFA. Recheck the live app/owner and current group scope; unseal only a
matching connection and verify workspace, viewer and active team before dispatch.

Reuse the existing account-scoped native creation receipts and dispatch leases.
GitHub and Linear share the retained receipt limit of 100 per original issue.
Pin normalized inputs, provider and verified connection identity in the request
hash. Check connection revision within an attempt, but omit it from the durable
Linear receipt hash so credential replacement with unchanged verified identity
can recover an interrupted request. Preserve existing GitHub receipt hashes.

An unconfirmed dispatch never creates again automatically. Explicit `recover=true`
(`--recover`) after the lease expires queries the exact stable UUID and verifies
the issue UUID and team. An optional recovery URL must match that query. Missing
issues, mismatched identity and partial GraphQL errors leave the outcome uncertain.
Only an explicit pre-creation provider rejection can reset a receipt to ready.

Persist confirmed provider evidence before attaching the tracker reference.
Attachment failure returns `created`; retry identical inputs to finish linking.
Recheck the connection/group before attachment. A linked receipt returns durable
evidence without another provider dispatch. Never store tokens or descriptions in
receipts, and never return provider diagnostics or credentials in API errors.

## Limits

Linear and Gregale do not share an atomic transaction. Disconnect or a competing
edit cannot recall an already dispatched write. Failed evidence persistence is
recoverable by the provider UUID. Changes of workspace, viewer, team or inputs
conflict with an existing receipt. OAuth and native Linear lifecycle sync remain
separate increments. The connection provides identity verification; provider
write permissions are checked by Linear when creating the issue.

## Validation

Domain tests cover unknown outcomes, dispatch fencing, same-identity credential
replacement, changed scope/group/revision, wrong ticket UUID, premature recovery,
UUID v4 validation and compatibility of existing GitHub receipt hashes. Provider
tests verify UUID-only recovery, exact team and issue identity and partial GraphQL
error rejection. PostgreSQL API tests verify encrypted credentials, ingestion-token
denial, durable retry/recovery, wrong teams, provider revocation, cross-app scope
and disconnect. CLI and Go/Node/Python SDK contracts preserve explicit recovery
without local provider credentials.
