// Package stripex is the thin Stripe wrapper for the one-box FaaS
// platform (spec §4.7, ADR-010). The Client exposes four primitives the
// rest of the platform uses:
//
//   - EnsurePlanProducts: idempotent product/price setup for the four
//     plans + the metered `gb_ram_hour` price (one line per account).
//   - CreateCustomer: maps a state.Account to a Stripe `cus_…` and writes
//     the customer ID back via state.Store.UpdateAccountStripeCustomerID.
//   - PushUsageRecord: hourly metered usage record with our own
//     (account, hour) dedupe table on top of the Stripe SDK idempotency
//     key. Idempotent — a redelivered hour is a no-op.
//   - VerifySignature: HMAC-SHA256 over the Stripe-Signature header,
//     constant-time compare, 5-minute default tolerance.
//
// The package keeps the dependency on stripe-go isolated. Production
// wires the SDK inside pushUsageRecordSDK (usage.go); tests exercise
// the dedupe gate + webhook signature paths without the SDK.
package stripe
