# Account and organization management

Account settings cover identity, organizations, members, billing, and data export.

```bash
gregale account status
gregale account export -o export.json
gregale orgs ls
gregale orgs members ORG_ID
```

Use organization-scoped keys for automation and grant members the smallest role that supports their work. `gregale orgs members ORG_ID` lists current membership; invitations and role changes are explicit subcommands under `gregale orgs`.

## Feature availability

Run `gregale capabilities` to inspect the features available to the authenticated
account. The output includes maturity, eligible plans, and an explanation when
a feature is unavailable. `gregale capabilities --json` and
`GET /v1/capabilities` expose the same account-scoped view.

For a disabled feature, `unavailable_reason` is `plan_not_entitled` when the
account's plan does not include it, or `runtime_unavailable` when its existing
platform runtime gate is closed. Plan restrictions take precedence when both
apply. `unavailable_detail` provides a customer-safe explanation and next
action; use the reason code, rather than that text, for automation. Enabled
features omit both fields. Older servers may omit the explanation fields even
when a feature is disabled; clients should retain a generic unavailable state.

Internal capabilities are omitted. Availability is informational and does not
guarantee fleet health, remaining quota, or successful resource creation; each
operation still enforces its own authorization and admission checks.

## Export and deletion

Exports are rate-limited and produce an audit event; store the resulting archive securely and delete it when no longer needed.

Deletion requests have a grace period. Confirm the target account or organization in the command output before continuing, and revoke tokens first. The API rejects cross-organization resource references so a valid user cannot accidentally read or mutate another tenant's data.
