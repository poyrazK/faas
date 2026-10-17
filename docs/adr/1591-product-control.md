# ADR-1591 · Internal Product Control above Operations

- **Status:** accepted for internal implementation
- **Date:** 2026-10-15
- **Decision:** Introduce Gregale Product Control under `gregale product`, with
  every new API and portal asset behind `FAAS_PRODUCT_CONTROL_ENABLED`, off by
  default. Persist immutable per-app catalogs, revision-pinned tenant plans and
  operation-linked commercial receipts in PostgreSQL. Keep Operations' scoped
  preview gate and execution owners unchanged.

## Contract

A product feature binds an existing immutable Operation definition. A customer
is an existing platform tenant. Plans grant feature access, a bounded monthly
start cap and an integer millicent price per verified logical success. The
product policy is authoritative for every new Operation admission in an app
after its first catalog publication. This includes new definitions introduced
by later deployments and calls through legacy Operations routes. Free features
use explicit zero-price grants.

Catalog publication and customer assignment serialize on the same account lock
as Operations admission. Admission counts starts across all revisions, pins the
customer's price and commits one independently retained commercial receipt with
the operation and execution. Product request identity has its own domain and
uses account/app/tenant/feature/key, so a definition or deployment change cannot
turn a retry into another operation. Existing Operations identities are intact.

The common operation-save boundary settles a receipt exactly once on verified
success, in the transaction containing the operation and event. Recovery and
delivery retries reuse that logical receipt. Failed, cancelled and unknown
outcomes have no success charge. Starts remain consumed as the execution budget.
The admission month is the pricing period. Currency cannot change within an
app's catalog history. In-memory and PostgreSQL implementations share the
contract; SQLC owns all new SQL.

The internal customer portal uses tenant credentials and only exposes the
assigned plan. It uses a development token form, in-memory credentials,
same-origin requests and a restrictive CSP. Production customer-login integration
is deferred. Turning the flag off hides these new surfaces while previously
admitted work retains success settlement through Operations.

## Scope and consequences

This is the first product policy and verified-outcome slice of the layer bridge.
It does not invent another scheduler, workflow engine, payment processor or
cross-database transaction. Provisional usage statements require a later design
for finalization, adjustments, taxes and payment collection. Existing
request-based monetization has a separate charging basis; this pilot does not
combine or automatically invoice both. Global rollout flags are independent of
commercial entitlements. Product maturity stays internal pending qualification.

Environment cloning classifies catalogs and customer assignments as configuration
and help requests as customer data. Full clones retain explicit isolation blockers
until those strategies exist; they cannot silently share product terms or help
content. Financial/action journals are operational history, and personal email
consent is account identity. Neither is copied into another environment.

See [the service contract](../ops/product-control.md) for API, CLI, accounting and
rollback details.
