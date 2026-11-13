# ADR-20261113160000 · Record what an account looked like when it signed up

- **Status:** accepted
- **Date:** 2026-11-13
- **Relates to:** ADR-361 (account abuse hold), ADR-20261113133500 (stored abuse strikes), ADR-20261104145353 (sign-up caps), ADR-091 D21 and ADR-143 (DB-IP Lite GeoIP databases), ADR-1190 (disposable-domain list)
- **Context:** The account abuse hold stops an account, but a held abuser can sign up again in a minute. Telling that a new account belongs to someone already held needs facts from the sign-up itself, and Gregale keeps almost none. `accounts` has the email address. `sessions.issued_ip` has the address of each sign-in, but sessions expire. `auth.signup` records only the method and source. The per-address and per-network sign-up caps count in memory and forget each day. An operator reviewing a hold cannot see where the account came from, and nothing can later link accounts by network.
- **Decision:**
  1. **One record per account.** `account_signup_facts` keeps:
     - the sign-up method (password, magic link, Google, GitHub);
     - the client address (the same `middleware.ClientIP` the rate limits use) and the user agent (at most 512 characters);
     - country and network owner (ASN number and organization), looked up from the local DB-IP Lite country and ASN databases gatewayd-internal already uses;
     - the email domain, and whether it is a disposable-inbox domain on the CC0 list the customer auth template ships (`pkg/disposableemail` keeps a copy; a test keeps the two identical);
     - when it was created.
     The first record wins; a retried sign-up never overwrites it.
  2. **No third party sees the address.** Country and ASN come from files on the control plane (the `geoip` Ansible role now also runs there; apid reads `FAAS_GEOIP_DB_PATH` and `FAAS_GEOIP_ASN_DB_PATH` with gatewayd's defaults). A missing database leaves that fact empty.
  3. **Best effort.** The record is written after the account is created. A failure is logged and counted in `apid_signup_facts_failed_total` and never fails the sign-up.
  4. **Retention.** The address and user agent are personal data, kept for `SignupFactsPersonalRetention` (90 days). meterd then clears them and sets `reduced_at`. The method, country, network owner and email domain stay for the life of the account, which is enough to recognise a pattern but not a person. The row is deleted with the account.
  5. **Use.** This decision only records. Showing the record to operators, linking new accounts to held ones and sign-up friction are follow-ups that read it.
- **Consequences:**
  - Abuse review and future account linking have the sign-up address for 90 days and the country, network owner and email domain for as long as the account exists.
  - The privacy policy already covers this (`pkg/legal/templates/privacy.html` §1 security data: IP addresses and user agents; §2 abuse prevention as a legitimate interest; §6 security records kept as long as needed, then deleted or anonymised). Naming the derived country and network owner and the 90-day period there is a wording change for the policy owner, not a precondition. No new subprocessor: the lookups are local.
  - The control plane downloads the DB-IP Lite files monthly, as compute nodes already do.
