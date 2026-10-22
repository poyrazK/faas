# Guided internal GitHub deployment setup

This default-off helper generates reviewable configuration for the existing
[GitHub OIDC trust](internal-frontend-hosting-oidc.md) and
[frontend hosting Action](../.github/actions/frontend-hosting/README.md).
It stages prebuilt files and creates a private preview. It does not publish to
production, configure GitHub, build on Gregale, or verify serving health.

## Enable for an internal operator

Keep the main hosting flag, operator allowlist and MFA controls. The API has a
separate hot flag `frontend_hosting_ci_setup_enabled`, initially seeded from
`FAAS_FRONTEND_HOSTING_CI_SETUP_ENABLED=1`. Infrastructure leaves it `0`.
The CLI independently requires that environment opt-in; the dashboard separately
requires `NEXT_PUBLIC_FRONTEND_HOSTING_CI_SETUP_ENABLED=true` at build time.
The dashboard also needs the main build flag and API `ci_setup_enabled` capability.
It can select existing trusts without enabling the separate trust-editing UI.

Actual generation requires durable hosting, OIDC with its verifier, automation,
private previews and history. An active, unsuspended durable site and existing
account-owned immutable trust are required. Configuration blockers produce a
report without an export. Site automation bearer credentials cannot use setup;
use the existing operator session or operator credential. The exported workflow
uses its explicit OIDC trust, without a stored Gregale token.

## Review preparation and generate

In the site's **GitHub deployment setup** panel, select a previously reviewed trust.
The binding fixes repository and owner IDs, repository, branch ref, workflow path,
event and optional GitHub environment. Select a new binding if those values change.
Provide these reviewed inputs:

- The Gregale HTTPS API origin that may receive the job's signed identity.
- Full lowercase 40-character commit SHAs for `actions/checkout` and
  `poyrazK/faas/.github/actions/frontend-hosting`. The hosting commit must actually
  contain the compatible Action and CLI contract. Generation does not look it up.
- A local composite preparation Action under `./.github/actions/`. This existing,
  reviewed Action supplies the trusted CLI and prebuilt output. It can wrap your
  own build/install process. The generated workflow does not install or build a CLI.
- The executable CLI's absolute or `./relative` runner path and reviewed SHA-256.
  A checksum step runs before the hosting Action executes the CLI. Obtain that
  digest from trusted release/provenance evidence, not an unreviewed download.
- Relative output directory, original SPA option, preview lifetime and publication
  wait within the API's CI limits.

Preparation runs in the same job as the OIDC grant. Review all its code, nested
Actions and dependencies accordingly. A checksum verifies file integrity against
your supplied digest; it does not establish who produced that binary.

Supported events are push on the exact literal branch, workflow_dispatch guarded
to that branch, or schedule. GitHub schedules run on the default branch: provide
its declared name matching the trust and a five-field UTC cron with a fixed minute
or `*/5` through `*/59` minute cadence. This is a declaration; verify the real
GitHub default branch. Dispatch workflow discovery also follows GitHub's default
branch rules. Pull request events are excluded. Branch names with glob syntax,
expressions, unresolved placeholders and traversal paths block generation.

Review the checks, generated YAML and recovery instructions. Download the workflow
and `gregale-ci-setup.json` recipe. Save YAML at the **exact workflow path** shown,
in the trusted repository. Filenames affect OIDC workflow identity.

## Export or preflight from your workstation

The setup JSON contains `site_id`, `trust_id` and `config` exactly as downloaded;
it contains no operator token. With your existing operator CLI login and configured
API origin, export again if needed:

```sh
export FAAS_FRONTEND_HOSTING_ENABLED=1
export FAAS_FRONTEND_HOSTING_CI_SETUP_ENABLED=1
gregale sites ci-setup --config gregale-ci-setup.json --out reviewed-preview.yml
```

Export creates a new mode-0600 file and refuses overwrite. It does not put files in
your repository automatically. Copy the reviewed file to the displayed workflow
path yourself, then run:

```sh
gregale sites ci-preflight --config gregale-ci-setup.json --repo-root .
```

Preflight refreshes the site/trust/flags and compares the saved workflow with the
freshly generated contract. It reads the selected local `action.yml` (or fallback
`action.yaml`) and requires a well-formed composite Action with steps. Reads must
stay inside the repository root, including symlink targets. Local workflow and
preparation contents are **never uploaded**, logged in diffs or executed.

Only comments and mapping order may differ. Changed job permissions, triggers,
pins, steps, environment, paths, IDs, TTL or other executable settings block the
check. Intentional changes require updating the recipe and regenerating/reviewing
the contract. Extra documents, duplicate keys, YAML aliases/anchors, custom tags
and oversized files are rejected. This is not a security analyzer for arbitrary
custom workflows. Exit `0` means configuration checks passed, `2` means a report
with blockers, and `1` means a local/API error.

The API is `POST /v1/internal/hosting/sites/{site}/ci-setup` with
`{"trust_id":"…","config":{…}}`; the generated artifact is returned under
`workflow_yaml` only when `configuration_valid` is true. It is read-only despite
POST and has no replay cache. No local workflow contents belong in this request.

## Qualification and recovery

Check remote commits, preparation content, binary provenance, actual runner file
availability, GitHub defaults and provider readiness separately. No successful
setup report proves those checks or customer-facing health. Review and commit the
files through your normal repository process; this helper makes no GitHub writes.
Live GitHub/Cloudflare qualification remains deferred until pilot inputs exist.

A rerun uses the original CI run ID and retains preview expiry. Keep files, source
labels and TTL unchanged. Use the Action's run/release/preview outputs and
[read-only diagnostics](internal-frontend-hosting-diagnostics.md) to inspect an
uncertain result. Enable diagnostics separately. Do not invent a new run ID to
hide a failure or recreate expired/revoked resources.
