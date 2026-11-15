# ADR-20261115090000 · Operator account risk view

- **Status:** accepted
- **Date:** 2026-11-15
- **Relates to:** ADR-361 (account abuse hold), ADR-20261113133500 (stored abuse strikes), ADR-20261113160000 (sign-up facts), ADR-20261114174600 (held-account links), ADR-091 (operator PII policy)
- **Context:** Abuse signals now live in five places: the account's hold columns, `account_signup_facts`, `account_held_links`, `account_abuse_strikes`, and events written by schedd and imaged (`accounts.abuse_hold`, `instance.cpu_abuse`, `deployment.abuse_scan`, and the `egress_fanout`, `egress_flood` and `cpu_sustained` recycle transitions). An operator deciding whether to release a hold, or reviewing a new account flagged as resembling a held one, had to query each by hand.
- **Decision:**
  1. **`GET /v1/admin/accounts/{id}/risk`**, behind an MFA-completed operator session (customer data, like the egress flow log). It returns the account's status, plan and hold; its sign-up record; its links to held accounts and the accounts linked to it; its strikes inside the hold window; and its abuse events of the last 30 days (at most 100, newest first). Events are found by subject: the account, its apps, deployments and instances.
  2. **PII by opt-in.** Email, sign-up address and user agent appear only with `?include_pii=1`, which writes a `pii.accessed` audit row, as the 360 view does. Country, network owner and email domain are always shown.
  3. **`GET /v1/admin/held-links?since=`** lists links detected in the window (default 24 hours, at most 30 days, at most 1,000 rows) with the new account's country and email domain, for a daily review. It names no address, so it has no PII opt-in.
  4. **CLI.** `gregalectl accounts risk --account-id <id> [--include-pii]` and `gregalectl accounts held-links [--since 24h]`, with `--json` for both.
- **Consequences:**
  - One command answers "who is this account, why is it held, who else looks like it".
  - The view reads only; releasing or placing a hold stays `gregalectl accounts abuse-hold`.
  - Accounts owned by organizations: events are found through apps whose `account_id` is the account, so an organization's apps appear under the account that created them.
