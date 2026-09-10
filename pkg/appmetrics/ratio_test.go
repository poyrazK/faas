package appmetrics

import (
	"strings"
	"testing"
)

func TestPercentRatioQueryPreservesZeroAndNoTelemetryStates(t *testing.T) {
	query := PercentRatioQuery("errors", "requests")
	for _, want := range []string{"errors", "requests", "or vector(0)", "requests) > 0"} {
		if !strings.Contains(query, want) {
			t.Errorf("query %q missing %q", query, want)
		}
	}
}
