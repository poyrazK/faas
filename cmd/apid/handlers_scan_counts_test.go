package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestScanResponseReconcilesLegacySeverityCounts(t *testing.T) {
	server := &server{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	payload, err := json.Marshal(map[string]any{
		"Critical": 0,
		"High":     0,
		"vulnerabilities": []api.Vulnerability{
			{ID: "CVE-1", Severity: "HIGH"},
			{ID: "CVE-2", Severity: "high"},
			{ID: "CVE-3", Severity: "Negligible"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	out := server.scanResponse(state.Deployment{ScanStatus: "complete", ScanResult: payload})
	if out.SeverityCounts.High != 2 || out.SeverityCounts.Low != 1 {
		t.Fatalf("severity_counts = %+v, want high=2 low=1", out.SeverityCounts)
	}
}
