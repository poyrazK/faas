package state

import "github.com/google/uuid"

// SpansSummaryTarget names the request_telemetry rows one spans flush may
// merge into (ADR-127 PR-D; tracing tenancy, 2026-10-31 logs/traces audit).
//
// AccountID is the storage namespace the rows carry (request_telemetry.
// account_id), which is the app creator's, not the tenant's. On its own it
// matches every app that creator made, company apps included, so producers
// that know more narrow it:
//
//   - AppID: only that app's rows. The in-guest bridge, service bindings,
//     outbound integrations and app-bound credentials know the app.
//   - OrgID: a customer key bound to a company organization merges into that
//     organization's apps, whoever created them, instead of AccountID's.
//   - CustomerCredential: AccountID holds the customer key that sent the
//     spans. A company app's rows are merged only while AccountID may still
//     deploy to that company (owner, admin or developer).
//
// The zero AppID and OrgID and a false CustomerCredential keep the
// account-wide merge older producers rely on.
type SpansSummaryTarget struct {
	TraceID            string
	AccountID          uuid.UUID
	AppID              uuid.UUID
	OrgID              uuid.UUID
	CustomerCredential bool
}
