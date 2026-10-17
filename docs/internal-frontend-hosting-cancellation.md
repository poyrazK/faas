# Internal CI cancellation

Canceling an exact recorded CI run stops it from resuming and queues its original
resources for safe cleanup. Cancellation does not cancel a GitHub job, revoke
existing signed R2 upload grants, instantly remove an edge cache entry, or assert
serving health. It requires no provider calls or live credentials.

All surfaces remain internal/operator-only and independently default-off:

| Surface | Opt-in |
| --- | --- |
| API | Hot `frontend_hosting_cancellation_enabled`, seeded by `FAAS_FRONTEND_HOSTING_CANCELLATION_ENABLED=1` |
| CLI | `FAAS_FRONTEND_HOSTING_CANCELLATION_ENABLED=1`, plus the main CLI hosting opt-in |
| Dashboard | `NEXT_PUBLIC_FRONTEND_HOSTING_CANCELLATION_ENABLED=true`, API capability `cancellation_enabled`, and the existing deployment activity opt-ins |

Terraform emits the new API environment flag as `0`. Apply migrations and upgrade
all API writers before enabling; do not roll back binaries while canceled
identities remain. Downgrade refuses to erase canceled markers. Legacy identities
backfill false. Live Cloudflare/R2/Access/GitHub qualification remains deferred.

```sh
export FAAS_FRONTEND_HOSTING_ENABLED=1
export FAAS_FRONTEND_HOSTING_CANCELLATION_ENABLED=1
gregale sites cancel --site SITE_UUID --run-id EXACT_RUN_UUID
```

Use operator authentication. Existing site CI tokens, including OIDC credentials,
gain no cancellation authority. Main API hosting, operator authorization, scopes,
MFA/same-origin write protection, automation and durable-site prerequisites remain
in force. The preview management flag need not be enabled to revoke an existing
preview safely.

The dashboard offers **Cancel CI run** only for eligible activity rows when its
independent build flag and API capability are enabled. Confirmation identifies the
exact run and explains permanent cancellation. Stored eligibility is advisory: the
server rechecks ownership, production history and other preview references under
the site lock. Failed or uncertain responses require refreshing and inspecting the
exact run before retrying cancellation. Cancellation is never automatically retried.

`POST /v1/internal/hosting/sites/{site}/ci/{run}/cancel` takes no body and returns
the original run with `canceled: true`. Its terminal state supplies idempotency;
no replay cache is used. Repeated cancellation cannot extend expiry or cleanup.
Cancellation does not free retained CI identity quota; the existing lifetime run
limit continues to apply. It preserves the original run/release/preview IDs, fingerprint and reservation
choice. Identical CI admission retries return the retained canceled identity;
the CLI stops before uploads and the Action reports `publication-state=canceled`
without recommending a retry of the canceled intent.

The transaction marks cancellation, revokes the original preview at generation
two if present, and retires the pending/ready release with an existing publication
generation. It rejects the whole action for active or previously promoted releases,
including inactive rollback targets, and for other preview references (even revoked
ones awaiting safe purge). These return `hosting_ci_cancel_protected` with 409.
No unrelated preview or production traffic is changed by a rejected cancellation.

Unused reservations stop counting immediately. A created preview still occupies
its slot until revocation is acknowledged and normal removal grace/purge completes.
Release bytes and retained-release counts remain accounted until normal route
acknowledgment, retirement grace, preview-reference removal and successful artifact
cleanup. Already-admitted provider work and upload grants may finish; new grants,
verification finalization, manual/CI preview creation and production promotion are
fenced by release retirement and the terminal marker.

Disabling cancellation stops new cancellation requests, including repeated calls;
already-canceled runs remain terminal and existing revocation/GC controllers keep
working. Reads, diagnostics and activity retain the canceled evidence after catalog
GC. Creating another run is a separate explicit intent, never an automatic recovery.
