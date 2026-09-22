# ADR-220 · Provider-scoped credit receipts

- **Status:** proposed
- **Date:** 2026-09-22
- **Amends:** spec §4.7.1 credit-consumption identity
- **Decision:** qualify invoice credit receipts by account, provider and provider
  invoice ID, matching the existing invoice natural key.
- **Why:** an invoice ID alone is not a cross-provider identifier. Reusing one
  could suppress a legitimate debit or restore another provider's consumption.

## Decision

Persist `credit_ledger.provider` and include it in consumption reservations,
replay reads and refund compensation. The negative-row unique index becomes
`(provider, provider_invoice_id, credit_id)`; each credit belongs to one account.
Consumption requires one of the supported providers. Issuance rows keep an empty
provider and a NULL invoice ID. Balances and compensation remain integer cents,
transactional and append-only. No provider API or billing price changes.

Consumption and failed-refund compensation share the existing invoice advisory
lock. Refunds retain their invoice row lock, and credit balances retain their
row locks. Replays observe the original debit minus any compensation; a distinct
refund cannot restore an already-compensated debit again.

## Legacy cutover and rollback

1. Stop all legacy credit-consumption and refund writers before applying
   `20260922183947369_credit_ledger_provider_scope.sql`. This is **not** a
   mixed-version rolling upgrade: old code ignores provider and uses the old
   conflict target. Include all apid replicas and any administrative consumers.
2. The migration locks the ledger and invoices exclusively. It stamps a provider
   only where the same account/invoice ID has exactly one provider in retained
   invoice records. It updates metadata only, never balances or delta amounts.
   Budget lock time and disk space proportional to ledger size.
3. Start only the new writers. Unqualified invoice-linked history blocks both
   consumption and credit compensation with `ErrConflict`; it does not silently
   select a provider, ignore the history, or debit the account again. Unrelated
   invoice keys remain usable.
4. Audit unresolved rows with `provider = '' AND provider_invoice_id IS NOT NULL`
   against retained invoice, provider and audit evidence. Resolve the entire
   account/invoice group, including compensations, under a controlled writer
   pause. If evidence is missing or conflicting, leave it blocked. Do not infer
   ownership from the currently selected provider or change invoice IDs to make
   a retry succeed. Historical erroneous balances need a separate audited repair.

Prefer a forward fix. Down requires stopped writers and recreates the legacy
unique index before dropping the column. If different providers have consumed
the same credit/invoice pair, rollback fails atomically rather than deleting or
coalescing audit evidence. A successful rollback restores the old unsafe
cross-provider semantics and must not be treated as a transparent downgrade.

## Consequences and alternatives

This adds a small provider field and backfill cost, and intentionally makes
ambiguous legacy keys unavailable until investigated. Prefixing invoice strings
would break existing audit identities. Guessing the active provider would
misattribute history after provider switching. Treating legacy rows as absent
would allow repeat debits. None preserve the receipt contract.

## Verification

Shared MemStore/PostgreSQL regressions cover matching provider IDs, independent
debits, isolated compensation, replays and unresolved-history rejection. The
migration regression covers account-qualified backfill, ambiguous/missing
invoices, unchanged balances, unique constraints and an unsafe rollback.
