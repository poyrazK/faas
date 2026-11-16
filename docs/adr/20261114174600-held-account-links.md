# ADR-20261114174600 · Flag new accounts that resemble abuse-held ones

- **Status:** accepted
- **Date:** 2026-11-14
- **Relates to:** ADR-361 (account abuse hold), ADR-20261113133500 (stored abuse strikes), ADR-20261113160000 (sign-up facts)
- **Context:** The abuse hold stops an account, not the person behind it, who can sign up again in a minute. ADR-20261113160000 records each new account's sign-up address and email domain. Nothing compares a new account with the accounts already held, so a returning abuser is only caught when their new account is recycled twice. Whether to refuse or hold such accounts depends on how often a match is a false positive (a shared office, a university, a mobile carrier's NAT), which is not known yet.
- **Decision:**
  1. **When.** Right after a sign-up record is stored, apid compares the new account with every abuse-held account and with the owner of every abuse-held organization (all hold reasons, `operator` included).
  2. **Signals.**
     - `signup_network`: the new sign-up address shares a /24 (IPv4) or /48 (IPv6), the networks the sign-up caps already use, with the held account's sign-up address, or with any address the held account signed in from within `SignupFactsPersonalRetention` (90 days, revoked sessions included). Sign-ins cover accounts held before sign-up records existed. An index on `sessions (account_id, issued_at)` keeps that lookup cheap.
     - `email_domain`: the same email domain, only when it is private: not on the curated public-provider list in `pkg/disposableemail` (Gmail, Outlook, iCloud, Proton, …) and not a disposable domain.
  3. **Flag only.** Each match is stored in `account_held_links` (account, held account, held organization, hold reason, signal, time; at most 50 per sign-up; deleted with either account), audited as `accounts.linked_to_held` and counted in `apid_signup_held_links_total{signal}`. The new account is neither held nor refused, and the sign-up never waits on or fails because of the check.
  4. **Not decided here.** Acting on a link: starting the account held, starting it under review, or requiring a card. That decision follows a period of real match rates and false positives, read from the metric and the audit events.
- **Consequences:**
  - Operators see when a held abuser returns, by audit event and metric, the day they sign up.
  - A network match is weak evidence on its own (NAT, offices, hosting providers' shared egress); the stored signal lets later rules weigh it.
  - Only sign-ups after ADR-20261113160000 have a record to compare; older held accounts match through their sign-in addresses for 90 days and by email domain for as long as they exist.
