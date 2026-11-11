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
  authenticator code when two-factor authentication is on. Your current
  address is told about the request straight away, with how to stop it. The
  link mailed to the new address is valid for 1 hour; confirming signs out
  every browser, cancels any password-reset link sent to the previous
  address, and your previous address gets a notice. If someone else changed
  it, Gregale support can move the account back within 30 days after checking
  your identity. API keys keep working. Your billing email
  moves too, and so does the billing email of any organization that billed to
  your previous address; if the billing provider refuses, you get an email
  saying which one to update. Wrong passwords count
  against the same limits as sign-in.
- **Failed passwords:** after 20 wrong passwords for one email address within
  an hour, from any network, password checks for that address pause until
  the hour is over and you get one email. Sign-in, changing your password or
  email, and turning off two-factor authentication all count and all pause.
  A password reset lifts the pause.
- **Password:** `gregale account password update` opens the form. A reset
  (**Forgot password**) signs out every browser and revokes the keys that
  sign-ins created. API keys you created by hand keep working; the reset
  emails you a list of them and the account page shows it, so you can revoke
  any you do not recognise.
- **Recovery cancels pending grants:** resetting or changing the password,
  confirming an email change, signing out everywhere and claiming an
  unverified address with an email link also cancel a pending email change,
  invitations you sent that were not yet accepted, and CLI sign-ins approved
  in the browser but not yet picked up by the CLI. Security activity records
  it.
- **Sign-in methods:** `gregale account provider list` shows whether a password
  and two-factor authentication are set and which Google or GitHub identities
  are linked. Remove a linked provider from the account page or with an API
  key (`DELETE /v1/account/sign-in-methods/{provider}`); the last way to sign
  in cannot be removed. **Link Google** / **Link GitHub** on the account page
  links whichever provider account you sign in with, even if its email
  differs from your Gregale email; it is also the only way to add back a
  provider you removed ("Continue with Google" with the same email is refused
  after a removal). Linking needs a verified email and, like removing, a
  code from the last 5 minutes when two-factor authentication is on, else a
  sign-in from the last 15 minutes. Linking or removing a provider emails you.
  Signing in with a provider verifies your email only when the provider's
  verified email is your Gregale email.
- **Recovery removes unknown provider sign-ins:** a password reset, claiming
  an unverified address with an email link, and an operator's email revert or
  two-factor reset remove every linked provider whose email is not your
  Gregale email, and email you for each. Link it again from the account page
  if it was yours.
- **Signing in again for sensitive changes:** if your account has neither a
  password nor two-factor authentication, setting a first password,
  changing the email, removing a sign-in method and setting up two-factor
  authentication need a sign-in from the last 15 minutes. An older browser
  session is sent to the sign-in page first (`recent_sign_in_required` from
  the API). Deleting the account and transferring an organization's
  ownership from such a browser need the same recent sign-in.
- **Approving a CLI or agent (MCP) sign-in** in the browser needs a verified
  email, and a sign-in from the last 15 minutes (with two-factor
  authentication on, a code from the last 15 minutes). An older browser
  signs in again and comes back to the same code.
- **Two-factor changes:** turning two-factor authentication on signs out
  every other browser and revokes CLI and agent sign-in keys; hand-made API
  keys keep working. Turning it off with your password also needs a code
  from the last 5 minutes; without your authenticator, use a recovery code
  or the emailed disable link.
- **Sign-in keys and recovery:** CLI and agent sign-in keys are also revoked
  when you confirm an email change and when an operator signs out your
  browsers or resets your two-factor authentication.
- **Sessions:** `gregale account sessions list`, `gregale account sessions
  revoke SESSION_ID`, or `--all` to sign out every browser. A sign-in from a
  browser and network none of your recent sessions used sends an email. A
  browser session lasts at most 7 days and ends after 48 hours without use.
- **Remembered browsers:** with two-factor authentication on, tick **Remember
  this browser for 30 days** when you enter the sign-in code, and that browser
  skips the code at sign-in until then. Sensitive actions still ask for a
  code. The account page lists remembered browsers (at most 10) with
  **Forget** and **Forget all browsers**; changing your password or email, or
  signing out everywhere, forgets them all.
- **Security activity:** the account page's **Security activity** table and
  `gregale account activity list` (`GET /v1/account/security-activity`, a
  full-access key) show sign-ins, API keys, CLI and agent sign-ins, and
  password, email, two-factor and sign-in method changes of the last 90 days.
- **Credential emails:** creating or rotating a deploy token (the mail names
  the app), and creating or rotating an API key, a CLI or agent
  (MCP) sign-in, and a password sign-in through the API each email you, so a
  credential you did not create is noticed.
- **Rotation grace window:** `gregale key grace-window set --days N` keeps a
  rotated key working for N days (0 to 30). Changing it asks for a recent
  sign-in or authenticator code.
- **Leaving an org:** `gregale org leave SLUG`. Owners transfer ownership first,
  billing contacts are replaced first, and a personal org cannot be left.

## Export and deletion

Exports are rate-limited and produce an audit event; store the resulting archive securely and delete it when no longer needed.

Deletion requests have a grace period. Cancelling a deletion (`gregale
account restore`, or **Restore** on the account page) needs a full-access
API key or a browser sign-in, and emails you. Confirm the target account or organization in the command output before continuing, and revoke tokens first. The API rejects cross-organization resource references so a valid user cannot accidentally read or mutate another tenant's data.
