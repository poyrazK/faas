package main_test

import (
	"testing"
	"time"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// TestActivityDisplayTimeIsUTC pins Hunt 32 H32-5: org activity printed the
// Mac's local time with no zone while audit list printed UTC.
func TestActivityDisplayTimeIsUTC(t *testing.T) {
	local := time.Local
	time.Local = time.FixedZone("+03", 3*60*60)
	t.Cleanup(func() { time.Local = local })
	if got, want := main.ActivityDisplayTime("2026-11-09T20:30:12.5+00:00"), "2026-11-09 20:30 UTC"; got != want {
		t.Fatalf("ActivityDisplayTime = %q, want %q", got, want)
	}
	if got := main.ActivityDisplayTime("not-a-time"); got != "not-a-time" {
		t.Fatalf("unparseable time = %q, want it unchanged", got)
	}
}
