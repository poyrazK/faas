# ADR-555: Durable object encryption journals

Date: 2026-10-03
Status: Accepted

## Context

ADR-554 validates owned native keys and provider encryption proof, but customer
writes cannot use that foundation until the selected encryption identity
survives retries and owner restarts. A mixed deployment also contains older
workers whose explicit SQL field lists omit newly added columns. Such workers
must not dispatch or settle an encrypted intent using ordinary receipt checks.

## Decision

Capture a private immutable encryption snapshot in tracked route/PUT/copy
receipts and multipart sessions. It includes the account, public selection,
native resource identity and enrollment fingerprint. KMS bucket-key selection
is explicit; native identities never enter public receipt projections. Bound
context, ownership, serialization and deep-copy rules apply at both stores.

Encrypted writes require an encryption-aware dispatch latch and exact verified
selection before settlement. Multipart claims bind an additional private token
to the worker lease. Database checks and triggers reject legacy dispatch,
claims, completion without verification, identity changes and unsafe rollback.
Memory stores enforce the same ownership and completion rules. A completed
result remains immutable; retry and cleanup retain the captured selection.

The multipart initializer and result worker consume the journal snapshot.
Current and historical write recovery require the complete native encryption
proof. Missing or changed bindings defer recovery rather than falling back to
ordinary confirmation. Confirmation and abort do not require the key to remain
enabled; new initiation still checks enabled key identity. Native KMS request
hooks and multipart list/create hooks account for actual outbound attempts.

## Consequences

This is a journal and recovery increment. Public encryption configuration,
customer request parsing, bucket defaults, response metadata, clients and GCS
remain separate acceptance work in the existing implementation ledger. Public
customer encryption directives remain unsupported until those paths are ready.
A local provider fixture and memory/PostgreSQL reconstruction qualify the
implementation; production provider access is not required.

Rollback refuses to discard any encrypted journal, including settled receipts,
because retained proof and enrollment identity must not be silently lost.

## Acceptance

Local memory/PostgreSQL tests cover private snapshot ownership, deep copies,
changed selections, missing verification, immutable terminal receipts, exact
event publication and rejection of legacy SQL claims/dispatch/settlement. The
real S3/KMS SDKs exercise native HTTP initialization, streamed part upload, a
lost completion response, owner/provider reconstruction and exact result proof.
Current-object write recovery rejects a tampered encryption digest and confirms
a lost acknowledgment after the key is disabled, without replaying the write
or probing the enabled key again. Request totals match actual native attempts.
Related storage, gateway and API regressions, focused races across state/provider/API workers, SQLC generation, changed-line lint and repository checks pass. SQL validation covers 19 snapshot ownership/shape/context cases; a transaction exercises migration down/up and refuses rollback when encrypted proof is retained. The generated schema snapshot is refreshed from the current migration set.
