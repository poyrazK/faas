// adr: 650 — bounded response truncation metadata never exposes returned data.
package main

import (
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
)

type dataAPIResponseLimitRecord struct {
	Source               string `json:"source"`
	LimitBytes           *int   `json:"limit_bytes"`
	ObservedAtLeastBytes *int   `json:"observed_at_least_bytes"`
}

func validateDataAPIResponseLimit(record dataAPIRequestRecord) error {
	r := record.ResponseLimit
	if r == nil {
		return nil
	}
	if record.Code == nil || *record.Code != "response_too_large" || !dataAPIDiagnosticAllowed(record.Outcome, "rejected", "aborted") || !dataAPIDiagnosticAllowed(r.Source, "content_length", "stream") || r.LimitBytes == nil || r.ObservedAtLeastBytes == nil || *r.LimitBytes != api.DataAPIMaxResponseBytes || *r.ObservedAtLeastBytes != api.DataAPIMaxResponseBytes+1 {
		return errors.New("invalid response limit metadata")
	}
	if record.Outcome == "rejected" && (record.Status == nil || *record.Status != 502) {
		return errors.New("inconsistent response limit status")
	}
	return nil
}
