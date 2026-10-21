// adr: 1663 — bounded per-user HTTP concurrency, never SQL-session counts.
package main

import (
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
)

type dataAPIConcurrencyLimitRecord struct {
	Reason            string `json:"reason"`
	MaxConcurrent     *int   `json:"max_concurrent"`
	Active            *int   `json:"active"`
	CoolingDown       *int   `json:"cooling_down"`
	RetryAfterSeconds *int   `json:"retry_after_seconds"`
}

func validateDataAPIConcurrencyLimit(record dataAPIRequestRecord) error {
	r := record.ConcurrencyLimit
	if r == nil {
		if record.Code != nil && *record.Code == "data_api_concurrency_limited" {
			return errors.New("missing concurrency-limit metadata")
		}
		return nil
	}
	if record.Code == nil || *record.Code != "data_api_concurrency_limited" || !dataAPIDiagnosticAllowed(record.Outcome, "rejected", "aborted") || (record.Status != nil && *record.Status != 429) || !dataAPIDiagnosticAllowed(record.Route, "rest", "rpc", "schema", "openapi") || !dataAPIDiagnosticAllowed(r.Reason, "subject", "tracking_capacity", "server_key") {
		return errors.New("inconsistent concurrency-limit metadata")
	}
	if r.MaxConcurrent == nil || *r.MaxConcurrent < 1 || *r.MaxConcurrent > api.DataAPIMaxPoolConnections || r.Active == nil || *r.Active < 0 || *r.Active > *r.MaxConcurrent || r.CoolingDown == nil || *r.CoolingDown < 0 || *r.CoolingDown > *r.MaxConcurrent || r.RetryAfterSeconds == nil || *r.RetryAfterSeconds < api.DataAPIOverloadRetrySeconds || *r.RetryAfterSeconds > (api.DataAPIUserConcurrencyCooldownMS+999)/1000 {
		return errors.New("invalid concurrency-limit policy metadata")
	}
	// Server keys (ADR-20261021180534) are refused when their slots are full
	// and never cool down.
	if (r.Reason == "subject" && *r.Active+*r.CoolingDown != *r.MaxConcurrent) || (r.Reason == "tracking_capacity" && (*r.Active != 0 || *r.CoolingDown != 0)) ||
		(r.Reason == "server_key" && (*r.Active != *r.MaxConcurrent || *r.CoolingDown != 0)) || ((*r.Active > 0 || r.Reason == "tracking_capacity") && *r.RetryAfterSeconds != api.DataAPIOverloadRetrySeconds) {
		return errors.New("invalid concurrency-limit accounting metadata")
	}
	return nil
}
