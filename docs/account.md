# Account and organization management

Account settings cover identity, organizations, members, billing, and data export.

```bash
gregale account status
gregale account export -o export.json
gregale orgs ls
gregale org members ORG_ID
```

Use organization-scoped keys for automation and grant members the smallest role that supports their work. `gregale org members ORG_ID` lists current membership; invitations and role changes are explicit subcommands under `gregale orgs`.

## Feature availability

Run `gregale feature list` to inspect the features available to the authenticated
account. The output includes maturity, eligible plans, and an explanation when
a feature is unavailable. `gregale feature list --json` and
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

## Profile, sign-in and sessions

- **Display name:** `gregale account update --name "Ada Lovelace"` (at most 80
  characters; `--clear-name` removes it).
- **Sign-in email:** change it from the account page (`gregale account email
  update` opens it). Confirm with your current password, or a fresh
  authenticator code when two-factor authentication is on. The link mailed to
  the new address is valid for 1 hour; confirming signs out every browser,
  cancels any password-reset link sent to the previous address, and your
  previous address gets a notice. API keys keep working. Wrong passwords count
  against the same per-IP limit as sign-in.
- **Password:** `gregale account password update` opens the form.
- **Sign-in methods:** `gregale account provider list` shows whether a password
  and two-factor authentication are set and which Google or GitHub identities
  are linked. Remove a linked provider from the account page or with an API
  key (`DELETE /v1/account/sign-in-methods/{provider}`); the last way to sign
  in cannot be removed. A removed provider stays removed: "Continue with
  Google" with the same email is refused until you press **Link Google** on
  the account page while signed in. Linking a provider to an existing account
  emails you.
- **Sessions:** `gregale account sessions list`, `gregale account sessions
  revoke SESSION_ID`, or `--all` to sign out every browser. A sign-in from a
  browser and network none of your recent sessions used sends an email.
- **Leaving an org:** `gregale org leave SLUG`. Owners transfer ownership first,
  billing contacts are replaced first, and a personal org cannot be left.

## Export and deletion

Exports are rate-limited and produce an audit event; store the resulting archive securely and delete it when no longer needed.

Deletion requests have a grace period. Confirm the target account or organization in the command output before continuing, and revoke tokens first. The API rejects cross-organization resource references so a valid user cannot accidentally read or mutate another tenant's data.
