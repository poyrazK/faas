# Billing: operator notes

Worker, rollout and roadmap detail behind the customer [billing page](../billing.md).
It moved here so that page can be published (#5709).

## Spending notifications

An independent delivery worker claims durable events with fenced leases. Failed
attempts retry with capped exponential backoff; a process restart recovers expired
claims. The existing configured mail pipeline, suppression rules and transport
apply. `delivered` means the pipeline returned success, not confirmed inbox
receipt. Log transport is for operator-selected development/diagnostic delivery.
Every attempt uses the same message ID. Resend deduplicates within its provider
window; Postmark does not support idempotency keys, so delivery is **at least
once**, including the unavoidable send-success/commit-failure boundary.

The workers are visible separately as `financial_budget_evaluation` and
`financial_budget_delivery` in meterd health. Email failure cannot block sampling.
Deploy the additive monitoring migration before activating policies, run matching
`apid` and `meterd` versions, and verify a threshold and one retry with an isolated
account before promoting the capability. This release does not add scheduler,
gateway or VM lifecycle enforcement.

## FOCUS export roadmap

The next invoice steps are remaining price classifications and legal issuer
coverage, correction-document lineage, and conditional FX/PO fields. A
historical cost ledger with
service/resource identifiers, quantities, units, and price snapshots is then
needed for the **Cost and Usage** dataset. Current plan prices
cannot reliably reconstruct past list, contracted, or effective costs.

`make focus-contract-check` checks the pinned upstream columns, exact
reconciliation, snapshot metadata, ownership, export/refresh bounds, provider
reads, lifecycle concurrency, and CLI operations.

## Monitored preview stopping

At a fresh recorded threshold, meterd latches the decision with its evidence and
notification outbox. New warm/cold requests, mesh HTTP/TCP calls, queued request
claims and command dispatch are blocked. Existing previews drain until the
configured deadline; the owning scheduler then destroys their resident VMs via
vmmd, retaining deployment snapshots/rootfs for the next allowed cold wake.
Command VMs carry retained node/lease ownership and preparation deadlines too.
