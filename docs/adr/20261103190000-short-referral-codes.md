# ADR: Short, manually entered referral codes

Status: Accepted 2026-11-03

## Context

The sales and customer referral program initially used shareable URLs and a
first-touch cookie (ADR-20261103180000). The product requirement is now a short
code such as `POJU43`, entered by the new customer during signup.

## Decision

Generate six uniformly random uppercase letters or digits using crypto/rand.
Store the public code in a unique nullable `share_code` column, with eight
bounded retries on code collisions. Preserve the original private source key
and historical attribution foreign keys. Existing sources receive a short code
when their owner explicitly creates or recovers their code.

Replace referral URL handling with an optional `referral_code` signup field.
Normalize surrounding whitespace and letter case. Validate active sources
before account creation; reject invalid codes without revealing their owner.
Google/GitHub signup explicitly saves the selection in a signed, host-only,
Secure/HttpOnly, SameSite=Lax cookie for 15 minutes. Empty input clears it.
There is no first-touch visit tracking. Existing account authentication never
creates attribution. Account creation and attribution remain transactional.

## Consequences

Both salespeople and customers share a simple code. Codes are public identifiers
and grant no access; authenticated portal authorization remains unchanged.
The database enforces global uniqueness, including across program types.
Add the column with a NOT VALID constraint, then build its unique index
concurrently and validate in a later migration. Migrate before deploying apid,
then deploy the matching faas-web signup form.
Terms remain configurable, snapshotted on signup, and payouts remain disabled.
This supersedes the URL and thirty-day first-touch portions of the prior ADR.
