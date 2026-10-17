# Release source details and deployment history (internal)

This default-off extension connects staged releases and private previews to
source context and accepted promotion history. It adds no builds, GitHub writes,
provider provisioning or edge behavior. See
[ADR-202610170902](adr/202610170902-internal-hosting-release-provenance-history.md).

## Enable deliberately

Apply `20261017090210137_hosting_release_provenance_history.sql` and upgrade all
apid writers before opting in. Pause hosting writes during rollout if capture
must start without old-writer gaps. Existing releases receive no invented source
labels or events. The feature does not need a new Worker version or flag.

| Surface | Independent opt-in, default off |
| --- | --- |
| API | Runtime `frontend_hosting_history_enabled`, bootstrapped by `FAAS_FRONTEND_HOSTING_HISTORY_ENABLED=1` |
| CLI workstation | `FAAS_FRONTEND_HOSTING_HISTORY_ENABLED=1` for source options/history reads |
| Dashboard build | `NEXT_PUBLIC_FRONTEND_HOSTING_HISTORY_ENABLED=true`, plus API `history_enabled` capability |

The existing main hosting flag, internal operator authorization, MFA, scopes and
account ownership remain authoritative. Terraform's generated apid environment
keeps the new flag at `0`; no infrastructure apply enables it. There is no public
entitlement or CI credential relaxation.

The API flag controls source admission, promotion capture and the history read
endpoint. Disabling it preserves ordinary create/promote behavior and existing
stored metadata/events, but new promotions have no timeline event. The history
endpoint returns `frontend_hosting_history_disabled`; dashboard panels/forms hide
when either their build flag or API capability is off. Existing release DTOs may
still carry metadata captured earlier, just as previously admitted release
configuration survives its admission flag. Disable does not erase data. The
metadata opt-in also runs before create-request idempotency replay.

## Deploy with source context

Use prebuilt output and the existing [stage/review/publish workflow](internal-frontend-hosting-releases.md):

```bash
export FAAS_FRONTEND_HOSTING_ENABLED=1
export FAAS_FRONTEND_HOSTING_HISTORY_ENABLED=1
gregale sites deploy --site SITE_UUID --path ./dist --no-promote \
  --source-repository org/storefront \
  --source-commit 0123456789abcdef0123456789abcdef01234567 \
  --source-branch main \
  --description 'Update storefront navigation'
gregale sites diff --site SITE_UUID --release RELEASE_UUID
gregale sites promote --site SITE_UUID --release RELEASE_UUID --expected-revision 7
gregale sites history --site SITE_UUID
```

Source options are explicit; Gregale does not inspect local Git configuration,
fetch repositories or run a build. Before creating a release with metadata the
CLI validates the fields and advertised API capability. It leaves the existing
content-only deploy contract intact. The dashboard offers optional source fields
beside the built-folder selector and retains the default stage-only workflow.

`POST /v1/internal/hosting/sites/{site}/releases` accepts the original manifest
fields plus an optional sibling `provenance` object:

```json
{
  "files": [{"path":"index.html","size":4,"sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","content_type":"text/html"}],
  "spa_fallback": false,
  "provenance": {
    "repository": "org/storefront",
    "commit_sha": "0123456789abcdef0123456789abcdef01234567",
    "branch": "main",
    "description": "Update storefront navigation"
  }
}
```

These labels are caller-provided and **unverified**. They do not prove a build
came from that commit. Enter no secrets. Repository values are plain text, not
trusted links or fetch targets. The server records the authenticated account/key
for promotion separately and rejects caller-supplied actor fields.

Source text is immutable once the release is created. To correct it, create a
new release. The limits in `pkg/api/limits.go`, advertised by capabilities, are
256 UTF-8 bytes for repository, 128 for branch and 280 for description; a provided
Git SHA must be full lowercase hexadecimal of length 40 or 64. At least one field
is required in a non-null object. Text must be valid UTF-8 without control/format
characters or surrounding whitespace. No metadata is inserted into the immutable
content manifest, R2 files, response headers or browser runtime configuration.

## Read and review history

`GET /v1/internal/hosting/sites/{site}/history` returns `events`, `retention_limit`
and an optional `next_before_revision`. Continue using that exact exclusive
cursor, or CLI `sites history --site SITE_UUID --before-revision N`. Reads are
account-owned, read-scope protected and `Cache-Control: no-store`; pages contain
at most 50 entries, newest accepted revision first. New promotions do not shift
older revision cursors. Pruning/site deletion can remove entries between reads.

Each event records target/prior release IDs, accepted/prior revisions, principal
account ID and optional key ID, acceptance timestamp and a target source snapshot.
Initial promotion has no prior release. Rollback is a new promotion event to a
retained release; a repeated promotion to the same release also has its own
accepted revision. Concurrent/stale requests produce no extra event. A history
insert or prune failure rolls back the pointer, marker and outbox with it.
Idempotency replay returns the earlier accepted result without another event.

The timeline distinguishes the site's current intended release, retained ready
releases and releases whose files are unavailable. A retained candidate opens the
existing comparison at the current observed site revision. No history action
silently promotes or refreshes that reviewed revision. Cleaned-up releases cannot
be selected. Existing active preview links appear only while preview controls are
also enabled; Access, expiry, revocation and suspension remain Worker-enforced.

This is **accepted intent**, not proof of deployment health or global publication.
Use site publication status and existing serving qualification independently.
History covers only API promotions that selected enabled capture, not previous
legacy/internal store calls or periods with the flag off. It does not synthesize
an old history from first-promotion timestamps or audit logs. The UI says so.

## Retention, cost and recovery

Keep the latest 1,000 events/site, including immutable source snapshots, enforced
inside the site-locked promotion transaction. The API advertises this limit. This
is a bounded operational timeline, not an append-only compliance archive. Existing
audit retention is separate. Source snapshots and actor identifiers deliberately
have no release/key FKs: physical release/key retirement neither erases history
nor gets blocked by it. Site purge removes its history; archived entries do not
keep release files alive and cannot restore retired bytes.

Storage adds bounded SQL metadata/indexes only: one source object per retained
release and at most 1,000 small events/site. There is no per-site process, resident
RAM, VM, CDN lookup or R2 copy. Page reads are indexed; pruning scans only the
bounded per-site history. These bytes are outside artifact-storage reservations.
They are not billed provider storage or a new customer plan entitlement.

If promotion returns an uncertain error, refresh site status/history and review
before retrying. Do not automatically resend with a fresh revision. A durable
history failure fails the whole enabled promotion; disable capture explicitly
only if accepting a documented timeline gap is intended. Downgrade refuses while
source metadata or events exist. Export/retain needed records, drain writers, and
perform any destructive archival/removal as a separately reviewed operation;
the feature never does that automatically.

Local evidence covers memory/Postgres CAS/concurrency, retention/pagination,
release cleanup snapshots, failed-insert rollback, migration/downgrade guards,
API actor/flag/replay protection, CLI source intent/cursors and dashboard forms,
preview linkage, comparison and disabled controls. Live Cloudflare qualification
remains deferred and is not required to exercise this control-plane feature.
