# Account and organization management

Account settings cover identity, organizations, members, billing, and data export.

```bash
gregale account status
gregale account export -o export.json
gregale orgs ls
gregale org members list ORG_ID
```

Use organization-scoped keys for automation and grant members the smallest role that supports their work. `gregale org members list ORG_ID` lists current membership; invitations and role changes are explicit subcommands under `gregale orgs`.

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
  an hour from one network (an IPv4 /24 or IPv6 /48), password checks for
  that address pause on that network until the hour is over, and you get one
  email; someone guessing from elsewhere does not lock you out. After 300
  wrong passwords within an hour from all networks together, checks pause
  everywhere. Sign-in, changing your password or email, and turning off
  two-factor authentication all count and all pause. A password reset lifts
  the pause on your network and the overall one.
- **Password:** `gregale account password update` opens the form. A new
  password needs at least 12 characters and is refused when it is a common
  breached password (checked against a list built into Gregale, without
  sending the password anywhere), one repeated character or a plain
  sequence such as `123456789012`. A reset
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
  if it was yours. Confirming an email change removes the providers linked
  under your previous address (for example a work Google account you no
  longer control) and emails you for each.
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
  signs in again and comes back to the same code. With the one-click
  `gregale login`, the page shows the login's code and the port it hands
  the sign-in to: approve only when your terminal shows the same.
- **Two-factor changes:** turning two-factor authentication on signs out
  every other browser and revokes CLI and agent sign-in keys; hand-made API
  keys keep working. Turning it off with your password also needs a code
  from the last 5 minutes; without your authenticator, use a recovery code
  or the emailed disable link. While an emailed disable is waiting out its
  24 hours, the two-factor page shows it with a **Cancel** button (only to a
  session that passed two-factor), and signing in with your authenticator
  or a recovery code cancels it; either way you get an email.
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
  sign-in or authenticator code. To end it sooner for one rotation, for a key
  that leaked, run `gregale key rotate KEY_ID --revoke-old` (or
  `--grace-days N`); the old key stops working at once.
- **Key lifetime:** every new API key, full-access keys included, expires a
  year after it is made (agent keys after 90 days); rotate it before then.
  `gregale login` keeps your five newest CLI sign-ins and signs older ones
  out.
- **Leaving an org:** `gregale org leave SLUG`. Owners transfer ownership first,
  billing contacts are replaced first, and a personal org cannot be left.

## Members, roles and invitations

An organization shares apps, projects, databases and billing between people.
Every account also has a personal organization, which cannot take members,
change owner or be deleted on its own. To work with others, create a team
organization and invite them:

```bash
gregale org create --name "Acme" acme
gregale --org acme billing plan hobby       # a new organization starts on Free
gregale org members invite --org acme --email ada@example.com --role developer
```

A new organization starts on the Free plan, which has no seats for other
members: inviting fails with `403 org_invitation_cap_exceeded` and names the
upgrade. Hobby seats 10 members, Pro 50 and Scale 200. The owner changes the
organization's plan with `gregale --org SLUG billing plan PLAN`.

### Working in an organization from the CLI

Put `--org SLUG` before the command to act for the organization instead of
your personal account:

```bash
gregale --org acme deploy                 # a new app is created in acme
gregale --org acme add postgres           # the database belongs to acme
gregale --org acme billing status         # acme's plan and billing
gregale --org acme billing portal
gregale --org acme deploy --project       # acme's projects
```

`--org` applies to projects, new apps, managed PostgreSQL, buckets, the plan,
billing status, the billing portal, budgets, overage caps, costs and monthly
usage. Existing apps need no flag: an app's name is global and your role in
its organization decides what you can do.

A command that shows or changes only your personal account fails with
`unsupported_by_cli` under `--org`, rather than acting on your personal
account or showing its data as if it were the organization's. Examples are
`gregale billing cancel`, the billing forecast and daily usage. For the
organization's API keys, use `gregale org key list|create|rotate|delete
--org SLUG`.

### Roles

Each member has one role. The default for an invitation is `developer`;
invite people who only need to look, such as support staff or auditors, as
`viewer`.

| Role | Can |
|---|---|
| `owner` | Everything an admin can, plus change roles, remove members, change the plan, require two-factor authentication, transfer ownership and delete the organization. There is exactly one owner. |
| `admin` | Everything a developer can, plus delete apps and projects, change their access and security settings, manage databases and object storage, replay queue messages, invite members other than admins, and create or revoke organization API keys. |
| `developer` | Read apps and logs, change app settings, deploy, manage secrets, approve releases, attach databases and storage, and send and consume queue messages. |
| `viewer` | Read apps, logs, projects, databases, storage and queues. Changes nothing. |
| `billing` | Manage billing details and the organization's name, and read usage. Cannot see apps. |

`gregale org members list acme` shows each member's user ID, email, name and
role. An action your role does not allow fails with `403 org_role_forbidden`.

### Invitations

`gregale org members invite` prints a single-use invitation token once. Gregale
does not email it: send it to the person yourself. They run
`gregale org invitation peek TOKEN` to see the organization and role, then
`gregale org invitation accept TOKEN` while signed in as the invited email
address. An invitation expires after 14 days. Accepting an expired, revoked
or already used token fails with `410 org_invitation_invalid`; send a new
invitation.

Owners and admins can invite. Only the owner can invite an admin.

```bash
gregale org invitation list --org acme                  # pending invitations
gregale org invitation revoke --org acme --invitation ID
```

Resetting your password, confirming an email change or signing out everywhere
also cancels the invitations you sent that were not yet accepted.

### Changing roles, removing members and ownership

Only the owner changes roles and removes members:

```bash
gregale org members update --org acme --user USER_ID --role viewer
gregale org members rm --org acme --user USER_ID
gregale org owner set --org acme --to USER_ID
```

Nobody can be given the `owner` role directly. To transfer ownership,
`gregale org owner set` hands it to an existing member, and the previous owner
becomes an admin.
Transferring ownership and deleting the organization need a signed-in browser,
so an API key alone cannot do either. The last owner cannot be removed or leave
(`409 org_last_owner`).

### Member limits

| Plan | Members | Pending invitations |
|---|---|---|
| Free | 0 | 0 |
| Hobby | 10 | 5 |
| Pro | 50 | 25 |
| Scale | 200 | 100 |

The plan is the organization's own plan. `gregale org usage --org acme` shows
how many seats are used. An invitation past the pending limit fails with
`403 org_invitation_cap_exceeded`; accepting one when the organization is full
fails with `403 org_member_cap_exceeded`. Besides its personal organization,
an account can own one team organization on the Free plan; creating or taking
over another fails with `403 org_free_limit_exceeded` until one is upgraded.

### Organization API keys

Owners and admins create keys that belong to the organization rather than to
a person:

```bash
gregale org key create ci-deploy --org acme --scopes deploy:write
gregale org key list --org acme
gregale org key rotate --org acme KEY_ID
gregale org key delete --org acme KEY_ID
```

Creating one emails the owner. Scopes, lifetime and rotation work as for
personal keys; see [API keys](auth.md#api-keys).

### Require two-factor authentication

The owner can require every member to use two-factor authentication with
`PUT /v1/orgs/{slug}/security-policy` and `{"require_mfa": true}`. The owner
needs it turned on first. Members without it get `403 org_mfa_required` until
they turn it on.

## Export and deletion

Export your data with `gregale account export -o export.json`
(`--no-secrets` leaves out the sealed secret ciphertext). Exports are
rate-limited and produce an audit event; store the resulting archive securely
and delete it when no longer needed.

Delete your account with `gregale account delete`. Deletion waits 30 days, and
until then `gregale account restore` (or **Restore** on the account page)
cancels it. Cancelling needs a full-access API key or a browser sign-in, and
emails you. Confirm the target account or organization in the command output before continuing, and revoke tokens first. The API rejects cross-organization resource references so a valid user cannot accidentally read or mutate another tenant's data.
