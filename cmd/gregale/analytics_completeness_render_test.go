package main_test

import (
	"bytes"
	gregale "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"strings"
	"testing"
)

func TestAnalyticsRendererShowsMissingEvidenceForEmptyResults(t *testing.T) {
	incomplete := false
	for _, tc := range []struct {
		complete *bool
		want     string
	}{{nil, "completeness unknown"}, {&incomplete, "incomplete"}} {
		var out bytes.Buffer
		gregale.RenderRequestAnalytics(&out, api.RequestAnalyticsResponse{TelemetryComplete: tc.complete, TelemetryGaps: []string{"telemetry_window_incomplete"}, Omitted: []string{"dependencies"}})
		for _, want := range []string{tc.want, "telemetry_window_incomplete", "dependencies", "Groups:     (none)"} {
			if !strings.Contains(out.String(), want) {
				t.Fatalf("missing %q: %s", want, out.String())
			}
		}
	}
}
