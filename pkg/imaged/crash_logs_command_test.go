package imaged

import (
	"strings"
	"testing"
	"time"
)

// prod hunt 2026-11-21: a Flask app that could not import a module failed
// with "read why with: gregale logs APP --deployment ID". That command reads
// the build and deploy log, which never holds the app's crash output; the
// runtime logs since the release was created do.
func TestCrashLogsCommandReadsRuntimeLogsSinceTheRelease(t *testing.T) {
	created := time.Date(2026, 11, 21, 7, 49, 48, 123456789, time.FixedZone("x", 3600))
	got := crashLogsCommand("bh15-bad", created)
	if got != "gregale logs bh15-bad --since 2026-11-21T06:49:48Z" {
		t.Fatalf("command = %q", got)
	}
	if strings.Contains(got, "--deployment") {
		t.Fatal("the build log never shows the crash")
	}
	if got := crashLogsCommand("bh15-bad", time.Time{}); got != "gregale logs bh15-bad --since 30m" {
		t.Fatalf("zero time command = %q", got)
	}
}
