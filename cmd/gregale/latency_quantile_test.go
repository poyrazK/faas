package main_test

import (
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

// A quantile at the request histogram's 10 s top bucket is a lower bound.
func TestRequestLatencyMS(t *testing.T) {
	for in, want := range map[float64]string{
		33.3:    "33.3ms",
		9999.9:  "9999.9ms",
		10000.0: "≥10000ms",
		12000.0: "≥10000ms",
	} {
		if got := main.RequestLatencyMS(in); got != want {
			t.Errorf("RequestLatencyMS(%v) = %q, want %q", in, got, want)
		}
	}
}
