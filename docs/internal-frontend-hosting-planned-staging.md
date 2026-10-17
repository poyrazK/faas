# Stage the build from a reviewed deployment plan

Saved-plan staging connects the [deployment planner](internal-frontend-hosting-planning.md)
to the existing immutable-release workflow. It checks that the build, serving
settings, target and production intent still match the report you reviewed.
See [the decision](adr/202610171405-staging-reviewed-hosting-plans.md).

## Enable and use

Use the existing operator API credential, scope, MFA and internal hosting setup.
Each workstation feature remains independently default-off:

```bash
export FAAS_FRONTEND_HOSTING_ENABLED=1
export FAAS_FRONTEND_HOSTING_DEPLOYMENT_PLAN_ENABLED=1
export FAAS_FRONTEND_HOSTING_PLANNED_STAGING_ENABLED=1

gregale sites plan --site SITE_UUID --path dist > plan.json
# Check the plan command's exit code and review the complete report first.
gregale sites deploy --site SITE_UUID --path dist --plan plan.json --no-promote
```

Check both the plan command's exit status and JSON `state`. A shell redirect can
leave an empty/truncated file after an error. Only complete version-1 plans with
`state: "within_observed_limits"` are accepted. Reports generated before format
version and API-origin binding were added must be regenerated and reviewed.
Treat the file as review input; editing it does not confer API permissions.

Use the same `--spa` choice for both commands. Configuration-bearing builds still
need `FAAS_FRONTEND_HOSTING_CONFIG_ENABLED=1` and server configuration support.
The planning flag controls generating reports; the independent planned-staging
flag controls consuming them. Consuming a saved plan does not require the planning
flag or local-preview flag to remain enabled. Normal `sites deploy` without
`--plan` keeps its existing behavior and flags.

`--plan` requires an explicit matching `--site`, a real JSON file and
`--no-promote`. It cannot be combined with `--wait` or implicit combined
deployment/promotion. It is not accepted by `sites promote`. No Cloudflare or
R2 credentials are loaded by this CLI command; private upload grants are obtained
through the existing Gregale API. Site-scoped CI/OIDC credentials cannot read
the account usage needed here, so remain insufficient.

## What is checked

Before API calls, the CLI rejects malformed, unsupported, blocked or inconsistent
plans, another API/site target, changed files, changed SPA/configuration settings,
symlinks and invalid/oversized builds. Saved files are bounded to 4 MiB, including
pretty-printed comparison metadata. API origins use HTTPS, with loopback HTTP
allowed for local testing; host case and default ports are normalized.

The CLI freezes and verifies reviewed file bytes in memory before collecting API
evidence. A rebuild, file deletion, replacement or configuration edit after that
point cannot change uploaded content. Memory retains up to the existing 100 MiB
payload limit plus bounded file-read and metadata overhead. The CLI executes no
build/source code and starts no preview listener.

Fresh reads must show the same production revision and selected release, and the
same file/settings comparison. Current quota usage may differ from the saved
observation; a new blocker prevents creation. Quotas reserve one full new release
without assuming deduplication or future cleanup. Missing API capabilities and
failed/inconsistent reads also prevent allocation.

After creating a release, the CLI checks its identity and manifest, rechecks the
site, validates grants for that exact private upload path, uploads frozen bytes,
then finalizes once. It verifies the exact created release and checks production
intent again. Upload requests contain no Gregale bearer/cookies and reject
redirects. Success returns the usual staged JSON: `state`, verified `release` and
the original `expected_revision`.

Optional source-label flags keep their existing workstation/history API gates.
Those labels are unverified caller metadata and are outside the saved build
fingerprint. They do not attest to a repository or approve promotion.

## Handle races and incomplete work

Capacity checks are advisory. Server admission remains authoritative and enforces
quotas transactionally. Site rechecks do not lock production or add an atomic
revision condition to release creation. Concurrent changes can race any read;
an observed change after creation leaves the new release retained for inspection.
This feature always stages without publishing.

Errors preserve the CLI's existing exit codes and emit no successful staged
report. Inspect the reported retained release ID, or list site releases when
creation was uncertain. There is no automatic mutation retry, replacement release,
retirement, cleanup or rollback. Re-running the command after an uncertain create
can allocate another release; inspect first. Storage and pending reservations
remain until the existing lifecycle/cleanup completes.

The independently gated [exact-release recovery workflow](internal-frontend-hosting-stage-recovery.md)
can continue the retained release through `--resume-release RELEASE_UUID`, reusing
its existing reservation. A ready release is recognized through reads only.

Review the actual immutable release against current production, then explicitly
promote using that review's revision:

```bash
gregale sites diff --site SITE_UUID --release RELEASE_UUID
gregale sites promote --site SITE_UUID --release RELEASE_UUID --expected-revision REVIEWED_REVISION
```

Use the revision from the new uploaded-release review, rather than refreshing the
saved plan's revision silently. Staging is not evidence of live DNS/TLS/Access
behavior, CDN propagation or application health. Live pilot qualification remains
deferred and separate from these portable checks.
