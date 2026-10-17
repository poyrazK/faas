// adr: 650 — rate-limit diagnostics expose policy and fixed reasons, never users.
package main

import (
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
)

type dataAPIRateLimitRecord struct {
	Reason            string `json:"reason"`
	PerSecond         *int   `json:"per_second"`
	Burst             *int   `json:"burst"`
	RetryAfterSeconds *int   `json:"retry_after_seconds"`
}

func validateDataAPIRateLimit(record dataAPIRequestRecord) error {
	r := record.RateLimit
	if r == nil {
		if record.Code != nil && *record.Code == "data_api_rate_limited" {
			return errors.New("missing rate-limit metadata")
		}
		return nil
	}
	if record.Code == nil || *record.Code != "data_api_rate_limited" || !dataAPIDiagnosticAllowed(record.Outcome, "rejected", "aborted") || (record.Status != nil && *record.Status != 429) || !dataAPIDiagnosticAllowed(record.Route, "rest", "rpc", "schema", "openapi") || !dataAPIDiagnosticAllowed(r.Reason, "subject", "tracking_capacity") {
		return errors.New("inconsistent rate-limit metadata")
	}
	if r.PerSecond == nil || *r.PerSecond < 1 || *r.PerSecond > api.DataAPIRateLimitMaxPerSecond || r.Burst == nil || *r.Burst < 1 || *r.Burst > api.DataAPIRateLimitMaxBurst || r.RetryAfterSeconds == nil || *r.RetryAfterSeconds != 1 {
		return errors.New("invalid rate-limit policy metadata")
	}
	return nil
}
