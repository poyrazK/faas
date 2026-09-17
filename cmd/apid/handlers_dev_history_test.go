package main

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestSummarizeDevSyncHistory(t *testing.T) {
	now := time.Now().UTC()
	rows := []state.DevSyncHistory{
		{DeploymentID: "new", Status: "live", EditToLiveMS: 21000, SLOTargetMS: 15000, WithinSLO: false, CreatedAt: now, Phases: json.RawMessage(`[{"phase":"build","status":"completed","duration_ms":16000}]`)},
		{DeploymentID: "old", Status: "live", EditToLiveMS: 4000, SLOTargetMS: 15000, WithinSLO: true, CreatedAt: now.Add(-time.Minute), Phases: json.RawMessage(`[]`)},
	}
	got := summarizeDevSyncHistory(rows)
	if got.Count != 2 || got.WithinSLOCount != 1 {
		t.Fatalf("summary counts = %+v", got)
	}
	if got.P50EditToLiveMS != 4000 || got.P95EditToLiveMS != 21000 {
		t.Fatalf("summary percentiles = %+v", got)
	}
	if got.SlowestPhase != "build" || got.Guidance == "" {
		t.Fatalf("summary regression guidance = %+v", got)
	}
}
