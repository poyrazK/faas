# ADR-202610171119: Guided GitHub frontend deployment setup

Date: 2026-10-17
Status: Accepted for the internal default-off pilot

## Context

Site-scoped GitHub identity, a retry-safe staging Action and read-only deployment
diagnostics exist. Operators still assemble permissions, pins, workflow paths and
trust settings by hand. Live GitHub and provider qualification remains deferred.
Setup must reduce configuration mistakes without gaining GitHub write authority,
executing customer builds or implying serving readiness.

## Decision

Add an independently default-off `frontend_hosting_ci_setup_enabled` hot API flag,
seeded by `FAAS_FRONTEND_HOSTING_CI_SETUP_ENABLED`, plus separate CLI and dashboard
build opt-ins. Terraform leaves the API seed at `0`. Operator-only
`POST /v1/internal/hosting/sites/{site}/ci-setup` takes bounded setup options and
an existing immutable trust ID. POST carries read configuration; it has no state
mutation or generic idempotency replay. Normal allowlist, account ownership, MFA,
same-origin session-write and read-scope checks apply. Site automation credentials
remain denied this endpoint. No new schema or provider/GitHub operations.

A pure generator derives the workflow path, repository, branch, event and optional
GitHub environment from the trust. It rejects incompatible flags and suspended,
inactive or non-durable sites. It supports literal branches and push,
workflow_dispatch or an explicitly supplied schedule with a declared matching
default branch. Schedule declarations and remote contents are not verified.
The server re-reads trust, site intent and feature availability after rendering;
these are observations, not a permanent or atomic authorization guarantee.

Generated workflows pin checkout and the hosting Action to caller-reviewed full
commit SHAs, check out the run commit with credentials persistence disabled,
grant only contents:read and id-token:write, and verify the caller-reviewed CLI
SHA-256 before invoking the hosting Action. A caller-reviewed local composite
preparation Action supplies the CLI and prebuilt output. It executes with the
job's OIDC permissions, so its contents and dependencies require the same trust
review as the rest of the workflow. Generation checks syntax, not provenance.
Untrusted values stay in structured YAML or environment data. Expressions,
traversal and unresolved placeholders in configuration are rejected.

The workflow stages and publishes a private preview only. It inherits the native
Action's stable GitHub run ID and never adds a new credential, production promotion,
run ID override, GitHub write or build service. History is a prerequisite because
the Action supplies source labels. Recovery instructions preserve original IDs,
files, labels and TTL and point to independently flagged diagnostics.

Dashboard downloads include generated YAML and a JSON recipe; edits, trust changes,
site changes and failed reads make stale results unavailable. CLI `sites ci-setup`
exports to a new private file and refuses overwrite. `sites ci-preflight` refreshes
the expected configuration, reads the exact trusted workflow path beneath an
explicit repository root and checks the local preparation Action shape. Neither
local source file is uploaded or executed. Root-contained reads reject escaping
symlinks. Bounded YAML parsing rejects aliases, anchors, duplicates, extra documents
and custom tags. Only comments and mapping order may differ from generated YAML;
this is a narrow contract checker, not an arbitrary-workflow security analyzer.
Fixed mismatch messages do not echo local contents or raw diffs.

A successful report is `configuration_valid`, never a health/readiness claim.
Remote SHA existence, dependency contents, binary provenance, actual runner files,
GitHub defaults, DNS/TLS/Access, R2 and serving health still need manual/live checks.
Configuration generation and preflight neither require nor fabricate that evidence.

## Validation

Pure rendering/drift tests cover identity, pins, triggers, flags, placeholders,
permissions and ambiguous YAML. Service tests cover ownership, default-off behavior,
read-only generation, storage errors and revocation while reading. Server/middleware
tests preserve operator and CI boundaries. CLI tests cover opt-ins, no overwrite,
local drift, bounded recipes, source privacy and escaping symlinks. Dashboard tests
and hermetic browser checks cover flags, downloads, edit invalidation and read
failure. Live GitHub/Cloudflare qualification remains explicitly deferred.
