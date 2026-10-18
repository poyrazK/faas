// adr: 1662 — engine transaction time includes pool acquisition and SQL.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
)

const dataAPIDatabaseTimingSource = "postgrest_server_timing"

var dataAPIDatabaseTimingPhases = []string{"jwt", "parse", "plan", "transaction", "response"}

type dataAPIDatabaseTimingRecord struct {
	Version      int                            `json:"version"`
	Source       string                         `json:"source"`
	State        string                         `json:"state"`
	Reason       string                         `json:"reason,omitempty"`
	SQLExecution string                         `json:"sql_execution"`
	PoolWait     string                         `json:"pool_wait"`
	Phases       map[string]dataAPITimingRecord `json:"phases,omitempty"`
}

type dataAPIDatabaseTimingStats struct {
	dataAPITimingStats
	Version         int            `json:"version"`
	Source          string         `json:"source"`
	SQLExecution    string         `json:"sql_execution"`
	PoolWait        string         `json:"pool_wait"`
	UnknownRequests int            `json:"unknown_requests"`
	UnknownByReason map[string]int `json:"unknown_by_reason"`
}

func parseDataAPIDatabaseTimings(raw json.RawMessage) (*dataAPIDatabaseTimingRecord, error) {
	if len(raw) == 0 {
		return nil, nil // Older runtimes and requests never dispatched to the engine.
	}
	var record *dataAPIDatabaseTimingRecord
	if json.Unmarshal(raw, &record) != nil || record == nil || record.Version != 1 || record.Source != dataAPIDatabaseTimingSource || record.SQLExecution != "unknown" || record.PoolWait != "unknown" {
		return nil, errors.New("invalid database timing metadata")
	}
	if record.State == "unknown" && record.Phases == nil && dataAPIDiagnosticAllowed(record.Reason, "no_response", "missing_header", "invalid_header") {
		return record, nil
	}
	if record.State != "observed" || record.Reason != "" || len(record.Phases) != len(dataAPIDatabaseTimingPhases) {
		return nil, errors.New("invalid database timing coverage")
	}
	for phase, timing := range record.Phases {
		if !dataAPIDiagnosticAllowed(phase, dataAPIDatabaseTimingPhases...) || timing.DurationMS == nil || timing.Complete == nil || !*timing.Complete || math.IsNaN(*timing.DurationMS) || math.IsInf(*timing.DurationMS, 0) || *timing.DurationMS < 0 {
			return nil, errors.New("invalid database timing phase")
		}
	}
	return record, nil
}

func (s *dataAPIDatabaseTimingStats) add(record *dataAPIDatabaseTimingRecord) {
	if record == nil {
		s.WithoutTimings++
	} else if record.State == "unknown" {
		s.UnknownRequests++
		if s.UnknownByReason == nil {
			s.UnknownByReason = map[string]int{}
		}
		s.UnknownByReason[record.Reason]++
	} else {
		s.dataAPITimingStats.add(record.Phases)
	}
}

func (s *dataAPIDatabaseTimingStats) finish() {
	s.Version, s.Source = 1, dataAPIDatabaseTimingSource
	s.SQLExecution, s.PoolWait = "unknown", "unknown"
	if s.UnknownByReason == nil {
		s.UnknownByReason = map[string]int{}
	}
	s.dataAPITimingStats.finish()
}

func writeDataAPIDatabaseTimings(stats dataAPIDatabaseTimingStats) error {
	if _, err := fmt.Fprintf(osStdout, "Database timing coverage (request sample; %s): %d observed, %d unknown, %d without timings\n", stats.Source, stats.ObservedRequests, stats.UnknownRequests, stats.WithoutTimings); err != nil {
		return err
	}
	latency := "n/a"
	if tx := stats.ByPhase["transaction"]; tx != nil && tx.LatencyMS.P50 != nil && tx.LatencyMS.P95 != nil && tx.LatencyMS.P99 != nil {
		latency = fmt.Sprintf("%.3f / %.3f / %.3f ms", *tx.LatencyMS.P50, *tx.LatencyMS.P95, *tx.LatencyMS.P99)
	}
	_, err := fmt.Fprintf(osStdout, "  Transaction (pool acquisition + SQL): completed p50/p95/p99: %s\n  SQL execution: unknown; pool wait: unknown (engine does not isolate these)\n", latency)
	return err
}
