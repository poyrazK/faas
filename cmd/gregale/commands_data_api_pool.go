// adr: 650 — safe, per-engine pool observations, separate from SQL readiness.
package main

import (
	"encoding/json"
	"fmt"
	"time"
)

type dataAPIHealthPool struct {
	State         string  `json:"state"`
	CheckedAt     string  `json:"checked_at,omitempty"`
	Reason        string  `json:"reason,omitempty"`
	Capacity      *uint64 `json:"capacity,omitempty"`
	Available     *uint64 `json:"available,omitempty"`
	Waiting       *uint64 `json:"waiting,omitempty"`
	TimeoutsTotal *uint64 `json:"timeouts_total,omitempty"`
}

func parseDataAPIHealthPool(raw json.RawMessage, capacity int) *dataAPIHealthPool {
	if len(raw) == 0 {
		return &dataAPIHealthPool{State: "unsupported"}
	}
	invalid := &dataAPIHealthPool{State: "invalid"}
	var pool dataAPIHealthPool
	if json.Unmarshal(raw, &pool) != nil {
		return invalid
	}
	checked, err := time.Parse(time.RFC3339Nano, pool.CheckedAt)
	if err != nil {
		return invalid
	}
	pool.CheckedAt = checked.UTC().Format(time.RFC3339Nano)
	counts := []*uint64{pool.Capacity, pool.Available, pool.Waiting, pool.TimeoutsTotal}
	if pool.State == "unknown" {
		if !dataAPIDiagnosticAllowed(pool.Reason, "unavailable", "timeout", "unsupported", "too_large", "invalid_metrics") {
			return invalid
		}
		for _, count := range counts {
			if count != nil {
				return invalid
			}
		}
		return &pool
	}
	if pool.State != "observed" || pool.Reason != "" {
		return invalid
	}
	for _, count := range counts {
		// Match the runtime's Number.isSafeInteger JSON representation contract.
		if count == nil || *count > (1<<53)-1 {
			return invalid
		}
	}
	if *pool.Capacity != uint64(capacity) || *pool.Available > *pool.Capacity {
		return invalid
	}
	return &pool
}

func describeDataAPIPool(report *dataAPIHealthReport, pool *dataAPIHealthPool) {
	if pool.State != "observed" {
		report.Partial = true
		report.PartialReasons = append(report.PartialReasons, "pool_"+pool.State)
		return
	}
	if *pool.Waiting > 0 {
		report.CurrentFindings = append(report.CurrentFindings, "pool_waiting")
	}
	// Available connections are idle connections already created by a lazy pool.
	// Zero alone does not prove exhaustion, and cumulative timeouts are not recent.
}

func writeDataAPIPool(pool *dataAPIHealthPool) error {
	if pool == nil {
		return nil
	}
	if pool.State != "observed" {
		if pool.CheckedAt == "" {
			_, err := fmt.Fprintf(osStdout, "Pool: %s\n", pool.State)
			return err
		}
		_, err := fmt.Fprintf(osStdout, "Pool: %s; reason: %s; checked at: %s\n", pool.State, pool.Reason, pool.CheckedAt)
		return err
	}
	_, err := fmt.Fprintf(osStdout, "Pool: capacity %d; available %d; waiting %d; cumulative acquisition timeouts %d; checked at %s\n", *pool.Capacity, *pool.Available, *pool.Waiting, *pool.TimeoutsTotal, pool.CheckedAt)
	return err
}
