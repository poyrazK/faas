# Stage, review and publish internal frontends

This workflow uses the existing default-off internal hosting API, CLI and
dashboard gates. It adds no public entitlement, provider provisioning or new
serving service. See [ADR-202610161920](adr/202610161920-staged-internal-hosting-releases.md).

Before allocating a release, use the independently gated
[local deployment planner](internal-frontend-hosting-planning.md) to compare built
output with production and inspect prospective quota usage without uploading.
The separate [saved-plan staging opt-in](internal-frontend-hosting-planned-staging.md)
adds build/target/revision checks when staging the output you reviewed.

## Stage a verified release

After upgrading all writers and publishers as described below:

```bash
export FAAS_FRONTEND_HOSTING_ENABLED=1
gregale sites deploy --site SITE_UUID --path ./dist --spa --no-promote
gregale sites diff --site SITE_UUID --release RELEASE_UUID
```

`--no-promote` uploads and finalizes an immutable release, then returns JSON
with `state: "staged"`, the verified `release`, and the `expected_revision` read
before uploading. It never calls promotion, changes the active release or
suspension, or makes the new release routable. Verification still advances the
existing publication outbox, but the generated routing inventory excludes
never-promoted releases. Their assets cannot enter the live site's retained
asset fallback, and their pinned release URLs cannot serve staged bytes (404 on an active site; an
empty/unpublished site remains unavailable). They still reserve
storage and count against release limits. Retire an unwanted staged release
through the existing grace-period cleanup flow.

Staging requires `staged_releases_supported: true` in API capabilities. The
CLI/dashboard reject unsupported APIs before creating a release. This capability
is an implementation contract, not proof that a rolling deployment has finished;
operators must first upgrade every writer and publisher. The dashboard defaults
to **Stage and verify without publishing**. An explicit checkbox can retain the
combined deploy-and-publish behavior. The CLI preserves combined deployment as
its default for compatibility; pass `--no-promote` to stage.

The existing independent configuration flag is still required when the output
folder includes [gregale-hosting.json](internal-frontend-hosting-config.md).
Interrupted or uncertain work retains a release ID for inspection. Creation and
promotion are never automatically repeated with a newer revision.

## Review and promote

The read-only `sites diff` command compares the target with the active release
and returns the observed `expected_revision` and `base_release_id`. The target
must be verified. An active baseline missing from the fetched inventory is an
error; it is never treated as an empty first deployment.

The comparison includes sorted added/changed/removed paths, old/new sizes,
digests and content types, total content bytes, and before/after SPA and response
configuration. It compares manifests, not downloaded contents, storage invoices
or the live edge. Removed paths may continue serving from previously promoted
retained releases under the existing asset-retention rules. A verified staging
release has not passed live Cloudflare qualification or a browser health check.

In the dashboard, choose **Review / publish** on a verified release. The
comparison holds the original data and revision while status polling continues.
A changed revision, active release, retirement or stale inventory blocks
publishing from that comparison; refresh and review again. Publishing preserves
suspension. The backend's existing account/operator/MFA/scope checks and revision
CAS remain authoritative if a change races the final click.

Publish with the revision actually reviewed, for example:

```bash
gregale sites promote --site SITE_UUID --release RELEASE_UUID --expected-revision 7
```

The command sends revision `7` directly. It does not fetch a newer revision and
replace the operator's choice. Conflicts and ambiguous errors require a status
refresh and a new review. The comparison is not an authorization token; normal
API permissions and CAS still enforce the mutation. The older `sites rollback`
command remains available, while `diff` followed by `promote` provides explicit
review for a rollback as well.

## Wait for publication

For durable sites, either combined deployment or explicit promotion can wait:

```bash
gregale sites deploy --site SITE_UUID --path ./dist --wait --wait-timeout 2m
gregale sites promote --site SITE_UUID --release RELEASE_UUID --expected-revision 7 --wait --wait-timeout 2m
```

The default wait is two minutes and the maximum is ten minutes, with one-second
polls. Timeout validation happens before writes. `--no-promote --wait` is invalid.
A leased site has no durable receipt, so waiting is rejected before deployment
creation or promotion. The wait starts after accepted promotion, and its single
context deadline also bounds every status HTTP request.

With `--wait`, stdout contains a `HostingPublicationObservation`: `site`,
`publication` and `wait_state`. Success requires the exact accepted site, origin,
active release, revision and suspension, plus a valid acknowledgement of the
current durable generation. `wait_state: "published"` returns exit zero. A newer
revision produces `superseded`; provider/API failure produces `unavailable`;
expiry or interruption produces `timeout`. These return nonzero without undoing or repeating
accepted promotion. Inspect `sites status` before another mutation. Without
`--wait`, existing promotion output remains the accepted site JSON.

A publication receipt confirms routing stored at the origin. It does **not**
prove every CDN location has refreshed, that a custom domain/certificate is
ready, or that the application serves correctly. The dashboard separately shows
ongoing publication status. Live pilot qualification remains a separate gate.

## Upgrade, compatibility and recovery

Migration `20261016192057719_hosting_release_promotion_history.sql` adds nullable
`first_promoted_at`. Existing ready releases are backfilled to their creation
time to preserve their previously routable inventory. This historical backfill
records compatibility eligibility; it is not the true historical promotion time.
New verification leaves the marker null. Successful CAS promotion marks first
promotion in the same transaction as active release and outbox updates; rollback
preserves the first marker. Routing includes active/previously promoted ready
releases, and retirement removes eligibility as before.

Drain publication controllers and hosting mutations, apply the migration, upgrade
**all** apid writers/publishers, then resume management and publication. Old
publishers include every ready release and can expose staging; old writers do
not maintain the marker. Keep the normal hosting gates off until that coordinated
upgrade finishes. The schema snapshot/SQLC output is regenerated from PostgreSQL.
No Worker wire format change is necessary: the existing retained-release list
now contains only eligible IDs. Route refresh grace still applies.

Downgrade refuses to remove the marker while never-promoted ready releases
remain. Retire and finish cleanup of those releases and drain all publishers
before considering downgrade. Disabling the API hosting flag stops management;
the existing independent Worker switch still controls serving. No credentials,
Cloudflare resources, provider plans or infrastructure flags are changed by this
workflow or by shipping the dashboard.

Local coverage includes memory/PostgreSQL visibility and atomic CAS, marker
cloning, migration backfill/constraints/downgrade, CLI conflicts/timeouts and
read-only review, immutable comparisons and browser upload behavior. Live
DNS/TLS/CDN/Access and regional validation remain pending.

Private immutable URLs for staged releases are documented in the [preview guide](internal-frontend-hosting-previews.md). They require separate default-off opt-ins.

The [release source and history guide](internal-frontend-hosting-history.md) explains optional caller-provided source labels and bounded accepted-promotion history, behind a separate default-off opt-in.
