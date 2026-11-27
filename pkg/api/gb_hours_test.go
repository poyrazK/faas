package api

import "testing"

func TestGBHoursFromMBSecondsUsesBilledGiB(t *testing.T) {
	// One 1 GiB instance for one hour is one billed GB-hour, not 1.024.
	if got := GBHoursFromMBSeconds(1024 * 3600); got != 1 {
		t.Fatalf("GBHoursFromMBSeconds(1 GiB-hour) = %v, want 1", got)
	}
}
