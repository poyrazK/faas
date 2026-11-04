package main_test

import (
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// TestStageTickerShowsACompletedStageNote: the builder-slot wait booked to
// source_download read as a 192 s download in the live ticker (Hunt 12
// H12-5).
func TestStageTickerShowsACompletedStageNote(t *testing.T) {
	if got := main.FormatStageDuration(192300, "completed", "waited 3m12s for a builder slot"); got != "192.3s (waited 3m12s for a builder slot)" {
		t.Fatalf("completed with note = %q", got)
	}
	if got := main.FormatStageDuration(1200, "completed", ""); got != "1.2s" {
		t.Fatalf("completed = %q", got)
	}
}
