// adr: 650 — safe timeout phases from bounded Data API request logs.
package main

import (
	"errors"
	"fmt"
	"math"
)

type dataAPIDeadlineRecord struct {
	Phase      string   `json:"phase"`
	ElapsedMS  *float64 `json:"elapsed_ms"`
	Dispatched *bool    `json:"dispatched"`
}

type dataAPIDeadlineStats struct {
	Observed   int            `json:"observed"`
	Dispatched int            `json:"dispatched"`
	ByPhase    map[string]int `json:"by_phase"`
}

func validateDataAPIDeadline(record dataAPIRequestRecord) error {
	d := record.Deadline
	if d == nil {
		return nil // Older logs have no timeout-phase coverage.
	}
	if record.Code == nil || *record.Code != "request_deadline_exceeded" || !dataAPIDiagnosticAllowed(record.Outcome, "query_timeout", "aborted") || !dataAPIDiagnosticAllowed(d.Phase, "verification", "refresh", "readiness", "signing", "upstream", "streaming") || d.ElapsedMS == nil || d.Dispatched == nil || math.IsNaN(*d.ElapsedMS) || math.IsInf(*d.ElapsedMS, 0) || *d.ElapsedMS < 0 {
		return errors.New("invalid request deadline metadata")
	}
	// Expiry can occur between entering upstream and attempting HTTP dispatch.
	if (*d.Dispatched && !dataAPIDiagnosticAllowed(d.Phase, "upstream", "streaming")) || (d.Phase == "streaming" && !*d.Dispatched) {
		return errors.New("inconsistent request deadline phase")
	}
	return nil
}

func (s *dataAPIDeadlineStats) add(d *dataAPIDeadlineRecord) {
	if d == nil {
		return
	}
	s.Observed++
	if *d.Dispatched {
		s.Dispatched++
	}
	s.ByPhase[d.Phase]++
}

func writeDataAPIDeadlines(s dataAPIDeadlineStats) error {
	if _, err := fmt.Fprintf(osStdout, "Request deadline sample: %d observed, %d dispatched upstream\n", s.Observed, s.Dispatched); err != nil {
		return err
	}
	for _, phase := range []string{"verification", "refresh", "readiness", "signing", "upstream", "streaming"} {
		if count := s.ByPhase[phase]; count > 0 {
			if _, err := fmt.Fprintf(osStdout, "  %s: %d\n", phase, count); err != nil {
				return err
			}
		}
	}
	return nil
}
