# Internal preview admission and capacity

Reservation-backed CI checks the existing eight-slot preview quota before creating
its release or issuing uploads. This avoids uploading a build that cannot get a
preview. It does not bypass release counts, account upload concurrency, storage
limits, artifact verification, publication or Access.

Everything remains internal/operator-only and default-off. Migrate first and
upgrade **all API writers** before enabling. Old binaries do not enforce holds;
do not roll back binaries while reserved identities exist. Terraform emits the
new flag as `0`. No live Cloudflare, R2, DNS, Access or GitHub qualification has
been performed for this feature.

| Surface | Explicit opt-in |
| --- | --- |
| API | Hot `frontend_hosting_preview_reservations_enabled`, seeded by `FAAS_FRONTEND_HOSTING_PREVIEW_RESERVATIONS_ENABLED=1` |
| CLI | `FAAS_FRONTEND_HOSTING_PREVIEW_RESERVATIONS_ENABLED=1` and `sites ci --reserve-preview` |
| Action | `reserve-preview: 'true'` (default `'false'`), using a reviewed Action and CLI that support reservations |
| Dashboard | `NEXT_PUBLIC_FRONTEND_HOSTING_PREVIEW_RESERVATIONS_ENABLED=true` and API capability `preview_reservations_enabled` |

Existing hosting, automation, preview and durable-routing prerequisites still
apply. The setup form offers an optional reservation checkbox when enabled.
Setup JSON accepts `config.reserve_preview: true`; exporting/preflighting this
recipe through the CLI also requires the independent workstation opt-in. Pin
reviewed Action/CLI versions that implement the new input; old versions cannot
provide reservation-backed admission.

```sh
export FAAS_FRONTEND_HOSTING_ENABLED=1
export FAAS_FRONTEND_HOSTING_PREVIEW_RESERVATIONS_ENABLED=1
gregale sites capacity --site SITE_UUID
# With existing scoped CI authentication and automation opt-ins:
gregale sites ci --site SITE_UUID --run-id STABLE_RUN_UUID --path dist --reserve-preview
```

`GET /v1/internal/hosting/sites/{site}/preview-capacity` is an operator-only,
no-store snapshot: site ID, limit, preview row count, unused reservations,
available slots and observation time. CI bearer credentials retain their exact
endpoint allowlist and cannot read this inventory. The snapshot performs no
provider probes or mutations; it is advisory and may change before admission.

A reservation counts while its original release is pending/ready, the original
preview expiry has not passed, and conversion has not completed. Expiry or
retirement frees an unused hold immediately on the next capacity calculation;
no sweeper or new process is required. Existing expired/revoked preview rows
continue counting until publication acknowledgment, removal grace and normal
safe garbage collection finish. Conversion exchanges its own hold for a preview
row in the same transaction. Manual and legacy CI preview writers honor holds.

Keep files, source, preview TTL **and reservation mode** unchanged on reruns.
Run fingerprints bind the opt-in. Exact retries retain original IDs and expiry,
including after disabling new reservations. Disabling the new admission flag
stops new holds and capacity reads, while existing holds/conversion remain
protected under existing hosting/automation/preview gates. Removing or retiring
a release never allows its old run to allocate replacements. The migration's
false default preserves legacy retry fingerprints and behavior; downgrade
refuses to forget any marked run, including converted/expired identities.

When full, retire unused CI releases or allow unused reservations to expire.
Revoke unused previews and wait for safe cleanup. A rejected new CI intent
allocates neither release nor run. Production promotion authority is unchanged.
