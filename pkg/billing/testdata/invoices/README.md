# Synthetic provider invoice fixtures

These contain no customer records. They exercise documented provider fields,
Gregale's generated Paddle price descriptions, discounts, tax, and a mixed
plan/consumption invoice. Fields unrelated to normalization are omitted.

Sources (checked 2026-09-30):

- Stripe [invoice line items](https://docs.stripe.com/api/invoice-line-item/object)
  and [invoices](https://docs.stripe.com/api/invoices/object). The fixture uses
  the legacy expanded `price` and `tax_amounts` shapes. Tests also exercise
  modern `pricing.price_details.price` IDs and `taxes`/`total_taxes`; opaque IDs
  retain an unknown category instead of inventing a classification.
- Paddle [transactions](https://developer.paddle.com/api-reference/transactions/get-transaction/):
  calculated `details.line_items[].totals`, `billing_details.payment_terms`,
  `billed_at`, and Gregale's own `faas-plan-*-monthly|overage` price descriptions.
- Polar [order.paid](https://polar.sh/docs/api-reference/2026-04/order_paid):
  order items and totals. The fixture additionally exercises the older expanded
  `product.prices[].amount_type` shape used by Gregale's catalog adapter.
  Current order payloads can omit prices; tests require unknown classification
  in that case. Order creation and buyer billing names never fill invoice date
  or issuer fields. Unallocated order discounts fail exact line reconciliation.
