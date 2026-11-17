# ADR-20261116163708: Hosted read-only Task recovery inspection

Status: Accepted
Amends: ADR-20261115105244, ADR-20261115171000

## Decision

Expose `gregale_inspect_task_tool_recovery` and
`gregale_plan_task_tool_recovery` to hosted personal-account agent keys when
the operator enables `mcp-bridged --allow-task-inspection` and the connection
selects `build-tools`. The flag defaults off and is independent of hosted
publication, invocation and connection write consent. Inspection uses the
existing native GET-only recovery reader; no CLI, uploads or local files are
required.

Keep the native journal's existing authority. The agent must use the same
owner-issued account key that originally prepared the intent, retaining
`agent,apps:read,deploy:write` scopes. The deployment scope remains required by
the native publication-authority check even though these MCP tools are read-only.
The bridge reauthenticates each request and the native journal reloads current
key, expiry, revocation and project restrictions. Account peers and rotated or
replacement keys gain no journal access. A returned recovery report must also
match the authenticated bridge account.

Browser OAuth, organization, project-limited and app-bound connections remain
excluded. Their credentials do not identify the original personal requester;
cross-key delegation needs a separate explicit design. This change adds no
scope, grant, migration, API endpoint or authority to the native journal.

Hosted catalogs receive only the two credential-free recovery projections,
including observed intent/execution state, bounded checkpoints, lifecycle,
blockers and next step. Raw intent and execution records remain local-only.
Neither bindings and claim hashes nor owner/qualification records, local paths,
tokens or arbitrary upstream fields enter the report. Existing time bounds,
incomplete-history and changing-journal rules remain intact; absent facts remain
unknown. Recovery plans grant no execution, approval renewal, takeover, fixture
retry, traffic change or database access. Task release execution and owner
recovery qualification remain on the original trusted local adapter.

## Validation

Exercise the actual hosted HTTP transport on both MCP protocol versions with
connection writes disabled and no CLI or upload store. Reconnect with the
original key, retain checkpoint identities, observe changed native status and
deny subsequent requests after key revocation. Check default-off/operator and
catalog boundaries, OAuth/scoped exclusions, native original-key/account
enforcement and cached-principal revocation/project changes. Reject foreign
account reports and prove native reads never invoke release mutations or expose
private record fields. Retain all existing local Task/recovery tests and gates.
Live staging and native KVM acceptance remain separate and are not run here.
