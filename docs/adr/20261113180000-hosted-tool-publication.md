# ADR-20261113180000: Reviewed hosted tool publication from source uploads

Status: Accepted
Amends: ADR-20261112200000, ADR-20261112210000

## Decision

Expose the verified publication workflow to unscoped personal hosted publishing
connections. Reuse the existing connection-bound source upload store, trusted CLI
worker, isolated builder VMs, reviewed source snapshot and APID publication
journal. There is no new execution service, VM owner or argument queue.

Operators enable `--allow-tool-publishing`, disabled by default, and offer
`build-tools`. Browser OAuth additionally requires `gregale:tools:publish` with
`gregale:write`, an explicit final publishing/fixture checkbox, account-wide app
consent and default-environment writes. Existing grants do not acquire this
scope on upgrade or refresh. Personal headless agent keys need native
`deploy:write`; their key scopes supply write consent. Native authentication,
MFA, ownership, plan quotas and admission remain authoritative. Shared-org,
project/app, tenant and hosting-site principals remain excluded.

Hosted connections list the Node starter, source-upload creation, a new
`gregale_plan_uploaded_tool_deploy`, and the existing apply/status tools. They
cannot call local-path authoring or standalone fixture execution. The preview
accepts open-access Node projects with `package.json` and a start script. Sources
arrive as existing bounded inline files or one-time connection-bound tar uploads;
normal extraction refuses links, devices, traversal, git and linked-project
configuration. No arbitrary host path, command, token environment or credential
value is accepted. Dependency/build scripts execute only in normal builder VMs.

Review performs no build or behavioral execution. It binds source content and
modes, fixture digest and names, explicit tool policy, app/account and serving
baseline. Apply rechecks these facts, snapshots the reviewed source and uses the
existing native single-shot begin before launching the fixed CLI deployment
command. The worker performs catalog pinning, protocol and behavioral checks,
and guarded production promotion. Its credential is the publishing connection's
native key, never an operator credential; application calls receive no native
account bearer.

APID permits unscoped personal agent/MCP-login keys with explicit deploy-write
scope to use the account publication journal, including receipt reads. This is
account-wide publisher authority, not receipt isolation per key. Discovery and
invocation routes retain their separate exact-grant checks: publication does not
self-grant invocation or unlock the private registry. Evidence remains
`publisher_attested`, bound to native deployment/source identity.

## Recovery and bounds

Unstarted reviews keep their server alive until their existing 15-minute expiry.
They and uploaded source are ephemeral and do not survive bridge restart.
Started records recover through the native journal on a fresh connection.
Repeated apply only reads the attempt; lost begin/worker replies, failures and
abandoned work never authorize another build or fixture run. There is no resume,
lease takeover or automatic retry. Existing four-worker, 30-minute publication
and native journal limits remain unchanged. The upload directory can be removed
once a verified private snapshot is owned by the worker; existing upload expiry
reclaims unused source.

Enabling requires matching API, CLI and bridge releases, in that order. With the
flag off, hosted catalogs remain unchanged. Disabling it drops restored publishing
OAuth grants. No product maturity is promoted, and durable Tasks/private OAuth
application credentials remain separate work.

## Validation

Both supported MCP protocols cover inline/archive source, reviewed publication,
repeat apply and fresh-connection recovery. Portable tests cover changed source,
source ownership/cleanup, consent and operator gates, OAuth persistence, native
scope/project/revocation fences and invocation separation. These tests do not
establish live cloud publication; deployed acceptance remains an opt-in staging
journey.
