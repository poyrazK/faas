# ADR-20261112210000: Durable tool publications and private registry

Status: Accepted

## Context

ADR-20261112200000 makes local agent tool publication single-shot within one
management process. Its in-memory receipts cannot support another agent or
recover an interrupted attempt after that process exits. Deployment history
alone cannot prove that behavioral tests passed.

## Decision

APID owns a native account-scoped publication journal in PostgreSQL. Before
launching the CLI, management inserts an immutable review under its random
publication ID. Only a newly created row authorizes that launch. Duplicate
begins return the existing record without execution authority. A lost begin
response removes the local executable plan: recovery reads the native record.
No local-file fallback is used when the native service is unavailable.

Review metadata includes source-tree and fixture digests, account/app and
serving baseline, explicit access mode, tool scopes and ordered test names.
It excludes filesystem paths, token references, credentials and fixture values.
An attempt has 35 minutes to report its outcome, covering the 30-minute worker
and bounded final evidence write. An abandoned running row is exposed as
`outcome_unknown`; it is never reclaimed, restarted or converted to a plan.
Unstarted local plans still expire after 15 minutes and do not survive restart.

Completion is an immutable compare-and-set from running to completed, failed
or outcome_unknown. Identical terminal writes are idempotent; changed evidence
conflicts. A successful outcome retains the caller-visible format-2 catalog
from CLI runtime discovery and the ordered passing test summaries. APID checks
ownership, app identity, default scope, live serving state and the deployment's
native source-archive digest. The reviewed tree digest and archive digest are
different artifacts and are retained separately. The stable version is the
publication ID plus exact tool name, bound to the exact deployment ID.

Evidence is explicitly `publisher_attested`: the authenticated publisher ran
protocol discovery and tests; APID binds that statement to native deployment
identity. It is not an independent platform attestation that the tests ran.
The runtime catalog pin and canonical caller-visible contract digest are also
distinct: role filtering can change the caller-visible catalog. Canonical
contract bytes are base64 encoded so JSONB cannot reorder their keys or change
their digest. Native validation checks full MCP contract structure and the
exact reviewed tool set. Management also requires the CLI to compare the
observed tool names against the frozen tool_scopes before fixtures or promotion,
so extra or substituted tools cannot replace the reviewed release.
Test details, discovery exchanges and arbitrary CLI
output are not retained in the journal.

Completed records form the private registry. `registry` adds read-only
`gregale_search_tools` and `gregale_describe_tool`; a fresh local account agent
needs no source directory or CLI to discover versions. Search matches app/tool
names and returns bounded version summaries. Describe requires an exact version
and name and returns runtime schemas, access requirements, test evidence and
byte-exact catalog content in bounded chunks. Large individual schemas use
those chunks instead of exceeding the management response bound. Missing
versions never fall back to latest.

Both native handlers and MCP handlers restrict this first slice to unscoped
personal-account connections. Hosted, app/project, tenant and organization
principals do not inherit an account-wide catalog. Native route scope checks
and MFA policy protect writes. Responses are private and noncacheable.
Discovery grants no execution permission. Historical publication does not
establish current deployment availability; registry responses say not_checked.

The preview retains at most 256 journal identities per account, including
failures and abandoned attempts, until account deletion. The account row lock
serializes first writes and quota checks. No automatic deletion reopens an old
execution identity. Catalogs are bounded to 256 KiB, intents to 64 KiB, outcomes
to 512 KiB, tool/test sets to 64 entries and registry pages to 20 versions.
All limits live in pkg/api/limits.go. Retention lifecycle, independent native
qualification, hosted publishing and authorized version-pinned invocation are
subsequent slices. This decision does not change existing product maturity.

## Validation

Memory and PostgreSQL tests cover reconnects, immutable reviews/outcomes,
concurrent publisher election, failed updates preserving earlier versions,
account/principal isolation and runtime catalog/test identity. Management tests
cover both MCP protocols, fresh bridge discovery without source/CLI access,
byte-exact schemas and repeat apply after process-state loss without execution.
Portable tests do not establish a live cloud publication or native VM journey.
