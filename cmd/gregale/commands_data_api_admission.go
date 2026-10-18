// adr: 650 — bounded Data API admission and retained-log diagnostics.
package main

import (
	"errors"
	"fmt"
	"math"

	"github.com/onebox-faas/faas/pkg/api"
)

type dataAPIAdmissionRecord struct {
	Outcome string   `json:"outcome"`
	WaitMS  *float64 `json:"wait_ms"`
	Active  *int     `json:"active"`
	Queued  *int     `json:"queued"`
}

type dataAPIAdmissionStats struct {
	Observed     int            `json:"observed"`
	Admitted     int            `json:"admitted"`
	QueueFull    int            `json:"queue_full"`
	QueueTimeout int            `json:"queue_timeout"`
	Cancelled    int            `json:"cancelled"`
	PeakActive   int            `json:"peak_active"`
	PeakQueued   int            `json:"peak_queued"`
	WaitMS       dataAPILatency `json:"wait_ms"`
	waits        dataAPIRequestStats
}

func validateDataAPIAdmission(record *dataAPIAdmissionRecord) error {
	if record == nil {
		return nil // Older runtimes and routes which bypass admission.
	}
	if !dataAPIDiagnosticAllowed(record.Outcome, "admitted", "queue_full", "queue_timeout", "cancelled") || record.WaitMS == nil || record.Active == nil || record.Queued == nil || math.IsNaN(*record.WaitMS) || math.IsInf(*record.WaitMS, 0) || *record.WaitMS < 0 || *record.Active < 0 || *record.Active > api.DataAPIMaxPoolConnections || *record.Queued < 0 || *record.Queued > api.DataAPIMaxQueuedRequests {
		return errors.New("invalid admission metadata")
	}
	return nil
}

func (s *dataAPIAdmissionStats) add(record *dataAPIAdmissionRecord) {
	if record == nil {
		return
	}
	s.Observed++
	s.PeakActive = max(s.PeakActive, *record.Active)
	s.PeakQueued = max(s.PeakQueued, *record.Queued)
	s.waits.add(dataAPIRequestRecord{DurationMS: *record.WaitMS})
	switch record.Outcome {
	case "admitted":
		s.Admitted++
	case "queue_full":
		s.QueueFull++
	case "queue_timeout":
		s.QueueTimeout++
	case "cancelled":
		s.Cancelled++
	}
}

func (s *dataAPIAdmissionStats) finish() {
	s.waits.finish()
	s.WaitMS = s.waits.LatencyMS
}

func writeDataAPIAdmission(stats dataAPIAdmissionStats) error {
	if _, err := fmt.Fprintf(osStdout, "Admission sample: %d observed, %d admitted, %d queue full, %d queue timeouts, %d cancelled; observed peaks: %d active, %d queued\n", stats.Observed, stats.Admitted, stats.QueueFull, stats.QueueTimeout, stats.Cancelled, stats.PeakActive, stats.PeakQueued); err != nil {
		return err
	}
	latency := "n/a"
	if stats.WaitMS.P50 != nil && stats.WaitMS.P95 != nil && stats.WaitMS.P99 != nil {
		latency = fmt.Sprintf("%.3f / %.3f / %.3f ms", *stats.WaitMS.P50, *stats.WaitMS.P95, *stats.WaitMS.P99)
	}
	_, err := fmt.Fprintf(osStdout, "Admission wait p50 / p95 / p99: %s\n", latency)
	return err
}
