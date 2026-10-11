# ADR-968 · Data API projects and owner workspace

- **Status:** accepted
- **Date:** 2026-10-10
- **Decision:** Generalize local development through a versioned project configuration, export schema and query helper types, expose verified SDK installation through the CLI, and add owner dashboard schema/type/refresh actions.
- **Why:** The notes starter should be an example rather than a requirement for developing a Data API. Application owners also need schema and types without manually creating app tasks.
- **Consequences:** Production SQL roles, JWT verification, provider qualification, quotas and scheduler ownership remain governed by ADR-650 and existing app lifecycles. GraphQL and Realtime are separate service decisions.

`data-api.dev.json` declares migration and optional seed paths, named local
identities, required saved verification scenarios, types output and explicit
client command arguments. Required scenarios must exercise two distinct
subjects. They run before readiness and after every migration reload; failures
pause the gateway. These are project assertions, not proof of every possible
authorization policy. Projects without a config retain legacy notes checks.
Seeds run once per disposable database, before the restricted serving role is
used. Configuration/identity changes require a dev restart.

Generated `Tables`, `TablesInsert`, `TablesUpdate` and `Enums` helpers preserve
the existing catalog contract, including generated-column write restrictions.
SDK query helpers infer projection and relationship results. Unsupported
catalog structures remain explicit rather than inventing writable contracts.

`gregale data-api install` uses the existing approved bundle manifest and
checksums with any project-relative client directory. It commits a reusable
artifact pin and npm file-package lock. Restore verifies the lock and archive
before `npm ci`. This decision neither changes licensing nor publishes npm
packages; approved distribution and live provider qualification remain release
prerequisites.

The hosted workspace is cookie-session authenticated, honors MFA and uses a
dedicated named CSRF envelope. Schema inspection and types export admit only
fixed existing `/app/types.mjs` commands through deployment-attached app tasks.
The control plane never connects to a customer database. Task reads are scoped
to account and app; downloads require successful, complete output from the
current live deployment. Both CLI and dashboard share a bounded, fingerprinted
schema-contract validator. Templates escape schema names and types and never
show raw task errors, SQL or credentials. Results describe one observation and
must be regenerated after migrations.

Refresh delegates to the existing fresh runtime restart handler and status
page. Completion of restart processing does not establish application health.
No SQL editor, account-key-to-application-token translation, public catalog
endpoint or new resource owner is introduced.
