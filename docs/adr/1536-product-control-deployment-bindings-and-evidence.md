# ADR-1536 · Product Control deployment bindings and admission evidence

- **Status:** accepted for internal implementation
- **Date:** 2026-10-15
- **Decision:** Extend internal Product Control features with an alternative
  named Operation binding in an explicit environment. Resolve deployed code
  independently of immutable commercial revisions. Save the selected binding
  and allowance decision with each independently retained commercial receipt,
  and expose an owner-only `gregale product explain` read.

## Contract

A feature binds exactly one definition UUID or `{operation: {name, scope}}`.
Named bindings select the matching definition on the sole fully serving live
deployment in the selected environment. All other live revisions must carry
zero traffic. Missing definitions, absent serving code and split canaries block
new work; resolution never guesses another environment or older contract.
Existing source deployment already installs immutable Operation definitions, so
this adds no new deployment writer, activation worker or scheduling path.

Resolution is preliminary. Existing Operations admission rechecks the selected
binding under its account, tenant and code locks, then commits the allowance,
price receipt and execution together. A code-only deployment changes neither
the catalog hash nor a customer's assigned revision, prices or start count.
Fixed UUID bindings retain their original behavior. Catalog references do not
extend code retention; accepted Operations retain code through existing pins.

Product request identity continues to use account/app/tenant/feature/key.
App-and-feature submission resolves a retained duplicate's original definition
before evaluating current entitlement or deployment availability. Operations
still validates exact input and reserves expired identities for its existing
deduplication window. Retry and recovery never silently move accepted work to
new code. The separate Operations preview/trust gates remain authoritative.

Each new receipt also records definition revision, deployment/release,
environment, Operation name, monthly start cap and starts consumed before this
admission. PostgreSQL saves this evidence in the receipt transaction; the
existing once-only success transition saves its settlement timestamp. The
explanation reads receipt and retained outcome at one storage boundary, with
separately read delivery state and paginated execution history. It omits customer input, result payloads,
storage locations and execution authority. Evidence survives result/event
retention, while owner deletion keeps the existing cascade contract.

Older receipts have nullable admission evidence: no backfill invents historical
allowance usage. Retained success is settled at its original price; expired
unsettled work remains explicitly unsettled. These projections remain
provisional and do not finalize invoices or collect payments.

All new behavior and the explanation route remain behind the default-off
`FAAS_PRODUCT_CONTROL_ENABLED` surface, with Product Control maturity internal.
Already admitted work keeps Operations execution, recovery and settlement while
the flag is off. See [the service contract](../ops/product-control.md).
