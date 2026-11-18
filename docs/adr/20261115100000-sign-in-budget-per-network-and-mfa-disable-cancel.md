# ADR-20261115100000 · Per-network password budget, cancelling an emailed MFA disable, and provider links after an email change

- **Status:** accepted
- **Date:** 2026-11-15
- **Amends:** ADR-20261111150000 (shared failed-password budget), ADR-077 (MFA step-up and email-assisted disable)
- **Context:** The 2026-11-10 account-flow audit found:
  - F10-1: 20 wrong passwords an hour for an address, from any IPs, paused every password check for it fleet-wide: sign-in, email change, password change and turning off two-factor. Anyone could keep the owner locked out of those, and a reset only lifted it until the next 20 guesses.
  - F14-1: with only the mailbox, an attacker could reset the password, ask for the email-assisted MFA disable, and confirm it 24 hours later. Every notice went to that mailbox; the owner signing in with their authenticator did not stop it, and no page showed a pending disable.
  - F11-1: confirming an email change kept the Google or GitHub links made under the old address. Whoever still controls that identity (an old employer, for a work address) could keep signing in.
- **Decision:**
  1. Failed password checks count per address and client network (the /24 of an IPv4 address, the /48 of an IPv6 one), at the existing `LoginFailuresPerEmail` (20) an hour, plus a fleet-wide `LoginFailuresPerEmailFleet` (300) an hour per address. A check is refused while either is spent. Both keys are keyed hashes in the existing shared budget table; no schema change. A password reset lifts the resetting network's pause and the fleet-wide one. The owner is emailed when either runs out, as before.
  2. A successful TOTP verification or recovery-code sign-in cancels every pending email-assisted disable (legacy requests and durable tokens) and emails the owner. `POST /v1/account/mfa/disable-email-cancel` does the same on request; it is not on the MFA-pending allowlist, so a session from a password reset alone cannot use it. The two-factor page shows a pending disable, with the Cancel button only for a session that passed the second factor. The disable-request email now says how to cancel instead of "do nothing".
  3. Confirming an email change removes provider links whose email is the old address, unless the same provider also has a link under the new address, and emails the account for each removal. Links under other addresses are unchanged.
- **Consequences:**
  - An attacker can no longer lock the owner's own network out with a handful of guesses; spreading guesses over many networks is still capped per address.
  - The emailed MFA disable remains the last resort for a lost authenticator, but the real owner can stop one they did not ask for.
  - A customer who signed in with a work Google account and then moved to a personal address needs to link a provider again under the new address.
  - Release skew: no schema change. The fleet-wide key is the old single per-address key, so failures an older apid counts still count toward it; while both run, an older apid still pauses an address after 20 failures from anywhere.
