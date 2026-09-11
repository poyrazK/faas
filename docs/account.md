# Account and organization management

Account settings cover identity, organizations, members, billing, and data export.

```bash
gregale account status
gregale account export -o export.json
gregale orgs ls
gregale orgs members ORG_ID
```

Use organization-scoped keys for automation and grant members the smallest role that supports their work. `gregale orgs members ORG_ID` lists current membership; invitations and role changes are explicit subcommands under `gregale orgs`.

Exports are rate-limited and produce an audit event; store the resulting archive securely and delete it when no longer needed.

Deletion requests have a grace period. Confirm the target account or organization in the command output before continuing, and revoke tokens first. The API rejects cross-organization resource references so a valid user cannot accidentally read or mutate another tenant's data.
