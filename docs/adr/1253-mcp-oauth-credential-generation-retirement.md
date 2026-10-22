# ADR-1253: MCP OAuth credential generations and retirement

## Context

Replacing a referenced refresh secret previously superseded retained grants without
retirement. A renewal could also install a result after its source secret changed.

## Decision

Customer secret mutations capture affected grants and saved renewal results in the
account-owned retirement ledger before committing the mutation. The PostgreSQL
trigger and memory-store hooks cover default-scope inserts, customer updates and
deletes, including paused observers. The frozen client credential is captured
before overwrite or deletion. Capacity exhaustion rolls back the secret mutation.

Every affected role changes revision and drops its lease and guided callback.
Refresh-source updates clear the old grant and renewal journal. Client-secret
updates preserve an installed grant and saved renewal result; unfinished renewals
require reauthorization. Client-secret deletion also retires the active grant.
Unfinished exchanges receive a durable unknown-issuance reservation, keyed by the
renewal operation ID. A late observed result is saved for retirement, while its
installation is rejected by the original revision fence.

Refresh-source identity uses app, scope, key and SecretVersion. Maintenance
resealing preserves the version and therefore the connection. Matching legacy
ciphertext-based identities are migrated without issuing a new grant.

Credential changes and in-flight renewals hold automatic retirement on the same
provider/client binding, protecting replacements. New exchanges wait for pending
retirements to become held or terminal. Explicit administrator approval can still
permit active-grant revocation. Disconnect releases the local replacement hold.

API and CLI expose credential_changed, retirement_held and reconnect_required.
Successful installation clears the local change marker; held ledger progress
remains visible. Rollback refuses active generation-based connections or unresolved
credential markers, avoiding replay by older workers.

## Consequences

Secret mutation, retirement intent and local fencing commit together. External
provider calls remain outside database transactions, so lost responses can still
require provider-side investigation. No VM or scheduler ownership changes apply.
