# ADR-1506: Consumer billing self-service

- **Status:** accepted
- **Date:** 2026-10-14
- **Decision:** An app's customers can read their own usage, spend and invoices with their own consumer key, so the app owner can build a billing page or let customers script it.
  - **Opt-in.** Self-service is opt-in per key. A consumer key carries the new `billing` scope only when the owner grants it (`gregale consumers key-create … --scopes read,billing`). `billing` grants no access to the app itself: the gateway's method check ignores it, so a key with only `billing` is a billing-only credential.
  - **Endpoints.** Three read-only apid endpoints take `Authorization: Bearer ck_…`. The app is named by its slug, because consumer keys are looked up per app:
    - `GET /v1/consumer-billing/{slug}`: the consumer's identity; month-to-date billable and unpriced units; the month-to-date amount priced exactly like a statement (ADR-949–953); and the current plan's monthly cap with its use.
    - `GET /v1/consumer-billing/{slug}/usage?since=&until=`: daily requests and billable units, at most 31 days per call (default: the current month).
    - `GET /v1/consumer-billing/{slug}/statements`: the latest 24 finalized statements with their exported invoice's payment status, amounts, due and paid dates, and Stripe's customer-facing hosted invoice URL (ADR-1504). It never includes the operator's dashboard URL, other consumers, or rate-card internals.
- **Why:** Consumers could not see what they owed until an invoice arrived. Owners had to proxy Gregale's account API, which needs an operator credential and exposes every consumer, to show usage in their own product.
- **Consequences:**
  - **Authentication.** It reuses the gateway's key model (ADR-120). The key must be well-formed, match the app's prefix row and hash in constant time, be neither revoked nor expired, belong to a live consumer, and carry `billing`. Every failure is the same 401, except a valid key without `billing`, which gets 403. Requests are limited per key prefix (`ConsumerBillingRequestsPerMinute`, burst `ConsumerBillingBurst`), checked before any database lookup.
  - **Read-only, no account session.** These routes never mutate state, issue no session, and do not count as account API activity. A consumer only ever sees its own rows; every read pins the account, app and consumer resolved from the key.
  - **Same numbers as statements.** The summary calls the same pricing path as the quote endpoint and statements, so a portal shows what the next statement will bill. It is not a statement: late usage can still change it.
  - **Migration.** `consumer_keys_scopes_vocab_chk` adds `billing`. Gateway method checks are unchanged.
- **Rejected alternatives:**
  - Serving the portal from the app's own domain through the gateway would put apid-owned billing reads on the request hot path and need a new gateway-to-apid read API.
  - A separate portal credential would duplicate key issuance, rotation and revocation, which consumer keys already have.
  - Making every key billing-capable by default would expose prices and invoices that owners have not chosen to show.
