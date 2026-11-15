# ADR-20261115072125: Read-only agent review of MCP Task releases

Status: Accepted
Amends: ADR-20261112200001

## Decision

Add `gregale_plan_task_tool_deploy` to the local management MCP `build-tools`
catalog. It reviews an existing native Task stack release JSON plan through
`gregale mcp tasks release plan --plan PATH --json`. Reuse the existing native
plan parser and release fingerprint. The existing automated release flow
supports Node workers, so this review has the same restriction.

The CLI validates bounded regular source files, enabled Task configurations for
web and worker, the Node release gate marker, declared tool permissions and any
release policy's catalog baselines. It does not resolve client token variables,
load database credentials, execute Node or customer code, access the API, run
migrations, create a journal, or change traffic. The fingerprint uses the existing
source, policy, namespace and API-context contract. It certifies no deployed
identity, binding, schema, lease, worker health or readiness observation.

Return structured review metadata: app roles, endpoint, access mode, tool scopes,
readiness role names, configured Task limit overrides and the release fingerprint.
List the required runtime and separate schema-owner database bindings, shared
namespace and retained payload keys, read-only observer, worker-scoped metrics
authority and live release qualification. Return neither credential references
nor values. Omitted limits retain starter defaults. `ready_to_apply` and
`bindings_verified` are always false.

## Authority and compatibility

Review is available to read-only local account connections with `build-tools` and
their own CLI. Hosted, organization and app-scoped connections cannot use local
plan paths. Accept no executable arguments, inline tokens, credential references
or migration database URL through the tool. Bound and decode the CLI response
into the shared review DTO, discard unknown output fields and return fixed
errors instead of reflecting CLI source/error output.

The result contains no publication plan ID. Existing publication apply cannot
consume it, and ordinary tool publication continues to reject durable Tasks.
Actual release still uses the owner-operated native Task release workflow and
its migration, key compatibility, worker drain, readiness and traffic guards.
Separate future milestones cover agent-managed release and resumable invocation.

Both management MCP protocol versions may inspect this source review. The
reviewed Task runtime uses the modern Tasks protocol. Existing release actions
retain their journal requirement; only `plan` runs without `--state` and refuses
`--resume`. No live staging or maturity promotion is included.

## Validation

Cover a CLI review with no executable available and no API requests; credential
non-disclosure; native fingerprint equivalence and changed-source detection;
unsafe, oversized and unsupported input rejection; management discovery and
read-only calls on both protocol versions; hosted exclusion; malformed or
executable CLI receipts and opaque CLI errors. Existing publication and Task
release regressions retain their assertions.
