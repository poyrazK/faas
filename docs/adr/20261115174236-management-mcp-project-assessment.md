# ADR-20261115174236: Project assessment and task guidance in management MCP

Date: 2026-11-15
Status: accepted
Related: ADR-20261112223000 (feature discovery), ADR-20261103193000 (focused catalogs)

## Context

Agents can inspect account capabilities but still need to connect repository
features to a supported workflow. The CLI has repository scanning and feature
hints; management MCP's source deployment remains a single-app workflow.

## Decision

Add two read-only, idempotent discovery tools to the core, release, diagnose,
provision and operate catalogs. Dedicated execute, build-tools, registry and
invoke catalogs retain their existing authority boundaries; empty operator
catalogs remain empty. `gregale_context` exposes the task index in each of the
five management workflow catalogs.

- `gregale_assess_project` accepts a local absolute directory on an unrestricted
  local connection, or explicit UTF-8 files on any connection. Hosted paths
  are refused before filesystem access. A bounded snapshot uses the existing
  offline repository scanner and feature-hint IDs. Source code is never executed,
  uploaded or planned. This is not the account-aware native deployment plan.
- `gregale_get_workflow` provides recipes for single-app deployment, project
  deployment, PostgreSQL, domains and failure diagnosis. Steps expose tools,
  supplied selectors, remaining inputs and completion obligations. Capability
  availability, catalog visibility and connection consent are separate checks.
  Plan restrictions, platform unavailability and unknown capability metadata
  remain distinct. Native authorization and capacity are checked by existing
  tools; discovery adds no authority and performs no step.
- Project deployment explicitly requires the CLI. Guidance names `gregale scan`
  and the reviewed project dry run; it never substitutes independent single-app
  deployments or invents an MCP project apply tool.

Local assessment reads at most 2,000 files, 256 KiB per file and 8 MiB total,
with a 4,000-entry traversal ceiling. Inline input reuses the source-upload
text bounds: 200 files and 96 KiB including paths. At most 50 workloads and
dependencies are projected. Oversized input is refused, never silently
truncated into a complete-looking assessment. Symlinks, common generated
directories and known secret-file names are excluded locally and refused in
inline input. The root boundary also fences concurrent symlink replacements.
Known binary files are omitted. Results contain names, classes, dependency and
environment-key names, feature IDs and bridge-owned explanations. They omit
raw warnings, commands, images, environment values and source excerpts.

## Validation and qualification

SDK workflows on both supported protocol versions verify discovery outside
core, the two-service/PostgreSQL assessment, static feature guidance, secret
redaction, the honest CLI handoff, and absence of API writes. Tests cover local
filesystem bounds, hosted-path refusal, consent/catalog/availability states
and cancellation. The existing catalog byte budget remains enforced.

The agent eval adds `assess-compose-project`, forces its MCP connection to
read-only, and grades returned assessment/workflow MCP evidence plus unchanged
files and absence of observed mutations. Initial returned-result parsing
supports Claude Code and Codex. Other transcript formats cannot claim an
evidence pass. Deterministic SDK tests and harness unit tests do not establish
a successful model run. Live qualification needs the built CLI, a dedicated
evaluation account and an installed agent; this change records no deployed
qualification result.
