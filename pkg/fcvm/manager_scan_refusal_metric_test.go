package fcvm

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/wire"
)

// A boot refused by the base scan gate is counted per base and reason, so a
// runtime base that refuses every boot can alert (Hunt 12 H12-1: the
// python313 runner base refused all boots on prod with no metric moving).
func TestBringUpScanCheckCountsRefusals(t *testing.T) {
	const (
		baseKey = "base/releases/runner-python313-amd64-1f08.ext4"
		scanKey = "scans/releases/runner-python313-amd64-1f08.ext4.scan.json"
	)
	sidecar := func(findings, fixable map[string]int) func(*memStorage) {
		return func(s *memStorage) {
			blob, _ := json.Marshal(map[string]any{
				"image":                  "ghcr.io/example/runner-python313@sha256:abc",
				"findings":               findings,
				"fix_available_findings": fixable,
			})
			_ = s.Put(context.Background(), scanKey, bytes.NewReader(blob))
		}
	}
	tests := []struct {
		name       string
		setup      func(*memStorage)
		wantReason string
		wantDetail string
	}{
		{"missing sidecar", func(*memStorage) {}, "sidecar_missing", ""},
		{"no CRITICAL total", sidecar(map[string]int{"HIGH": 1}, nil), "incomplete", ""},
		{"fix-available CRITICAL", sidecar(map[string]int{"CRITICAL": 2}, map[string]int{"CRITICAL": 2}), "critical", "platform problem, not your code"},
		// #5709: imaged's failed-scan placeholder is not 9999 vulnerabilities.
		{"failed scan placeholder", sidecar(map[string]int{"CRITICAL": wire.BaseScanFailedCount}, map[string]int{"CRITICAL": wire.BaseScanFailedCount}), "scan_failed", "could not finish the vulnerability scan"},
		{"clean", sidecar(map[string]int{"CRITICAL": 0}, map[string]int{"CRITICAL": 0}), "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &memStorage{}
			tt.setup(s)
			ops := wire.NewOpsMetrics("vmmd")
			m := &Manager{}
			m.WithStorage(s)
			m.SetImageScanMetrics(ops)
			err := m.bringUpScanCheck(context.Background(), baseKey)
			if (err != nil) != (tt.wantReason != "") {
				t.Fatalf("bringUpScanCheck err = %v, want refusal %q", err, tt.wantReason)
			}
			if tt.wantDetail != "" && !strings.Contains(err.Error(), tt.wantDetail) {
				t.Errorf("refusal %q does not say %q", err, tt.wantDetail)
			}
			body := scrapeRefusalMetrics(t, ops)
			for _, reason := range wire.BaseScanRefusalReasons {
				series := `vmmd_base_scan_refusals_total{base="` + baseKey + `",reason="` + reason + `"} 1`
				if got, want := strings.Contains(body, series), reason == tt.wantReason; got != want {
					t.Errorf("series %s present = %t, want %t", series, got, want)
				}
			}
		})
	}
}

func scrapeRefusalMetrics(t *testing.T, ops *wire.OpsMetrics) string {
	t.Helper()
	rec := httptest.NewRecorder()
	ops.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	b, err := io.ReadAll(rec.Body)
	if err != nil {
		t.Fatalf("read metrics: %v", err)
	}
	return string(b)
}
