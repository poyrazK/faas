package main_test

import (
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// `logs --grep` on retained logs matched the JSON frame, so field names and
// punctuation matched every line (prod hunt r81).
func TestRetainedLineMatchesTheLogLine(t *testing.T) {
	t.Parallel()
	frame := `{"seq":1,"instance":"i-1","stream":"stdout","line":"Listening on :3000","written_at":"2026-11-28T10:00:00Z"}`
	for grep, want := range map[string]bool{
		"":          true,
		"listening": true,
		"3000":      true,
		"instance":  false,
		"stream":    false,
		`":"`:       false,
		"_":         false,
	} {
		if got := main.RetainedLineMatches(frame, grep); got != want {
			t.Errorf("RetainedLineMatches(frame, %q) = %v, want %v", grep, got, want)
		}
	}
	if !main.RetainedLineMatches("plain text line", "text") {
		t.Error("a frame that is not JSON must keep the raw match")
	}
}
