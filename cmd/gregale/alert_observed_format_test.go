package main_test

import (
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// prod hunt 2026-11-21: `alert deliveries` printed an error rate of 25/26
// as 96.15384615384616.
func TestAlertObservedValueIsRounded(t *testing.T) {
	for in, want := range map[float64]string{
		96.15384615384616: "96.15",
		1500:              "1500",
		0.5:               "0.5",
		0:                 "0",
	} {
		if got := main.FormatObserved(in); got != want {
			t.Errorf("FormatObserved(%v) = %q, want %q", in, got, want)
		}
	}
}
