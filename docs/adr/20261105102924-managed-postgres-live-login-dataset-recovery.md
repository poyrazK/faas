# ADR-20261105102924: Managed PostgreSQL live logins use the pinned dataset

- **Status:** accepted for implementation; provider qualification remains separate
- **Date:** 2026-11-05
- **Owner:** apid managed PostgreSQL and live queries
- **Amends:** [live-query authorization](20261016151947-live-queries-incremental-engine.md) and [credential scope](461-managed-postgres-credential-privileges.md)

## Problem

A Neon root's lifecycle identity is a project, while its immutable dataset is a
project/branch. Bindings already use the dataset. Live-login issuance used the
project, producing a different credential scope, so enabling live queries could
succeed while PostgreSQL refused to assume any ordinary binding role. Default
branch changes could also redirect live issuance and revocation.

## Decision

Live issuance and revocation use the catalog dataset selector, with the existing
legacy fallback when no pin is recorded. A separate legacy selector names only
an older credential scope on that same branch. Neon accepts it only when it is
the pinned branch's project; it never resolves that selector's current default.

A new root login may assume marked runtime, reader and Data API binding roles
from the pinned scope and the older project scope on the same physical branch.
New binding logins continue granting SET without inheritance. Restored branches
do not acquire a legacy project scope. SQL ownership markers and inherited-login
restriction remain unchanged; SQL role names keep the format older restorers
recognize.

Issuing the replacement stops the older live login and its sessions but keeps
the replication slot and its WAL position. Disabling stops both possible login
identities before dropping the shared slot. A failed catalog write remains
recoverable through the existing missing-login worker; a retry may rotate the
replacement's password, as missing-login recovery already does. Application
writers continue operating, and live-query clients reconnect on the stored
replacement credential.

Two nullable catalog fields record a live login's authorized dataset and its
provider identity at authorization time. New upserts capture both with the
sealed login and reject a result prepared for a different current dataset.
The missing-login worker also finds root entries with absent or mismatched
authorization. It checks eligibility again under the database lock before
replacing an entry. Successful repair removes the entry from that work, so the
worker does not repeatedly open SQL connections to already repaired databases.
The customer's live keys and replication slot name are preserved.

## Release and recovery

Apply the expand migration `20261105141918793` and its separate constraint
validation `20261105141918803` before the control-plane code. Existing rows
remain valid with both fields NULL. No bulk backfill guesses a branch identity.
The existing five-minute worker replaces eligible entries in its bounded batches.

[ADR-20261105111902](20261105111902-managed-postgres-recovery-and-proxy-setup.md)
adds durable attempt ordering: untried databases and then oldest attempts run
first. The attempt commits before provider I/O, so failures and process restarts
cannot keep a healthy database behind the same failing first page.

N-1 writers can still insert or update the original fields. A changed provider
identity cannot reuse a receipt captured for the replacement identity; the new
worker discovers and repairs it again. Reverting control-plane code leaves the
nullable metadata intact. Production Neon qualification still needs to verify
owner SQL permissions, slot preservation and client reconnect behavior.

Local validation covers SET ROLE for existing and later bindings, legacy-role
replacement, a retained logical slot where the fixture supports it, disable
replay, invalid legacy selectors, catalog write failure, stale dataset results,
and N-1 writes.

Compute-cap suspension holds use the same pinned dataset for observation,
placement and release as deletion holds. Project-wide quota writes retain their
lifecycle selector; that selector does not authorize choosing a mutable default
branch for an endpoint hold.
