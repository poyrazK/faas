package main

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// consoleRecorder records the order of console writes and drains.
type consoleRecorder struct {
	events []string
	text   strings.Builder
}

func (r *consoleRecorder) Write(p []byte) (int, error) {
	r.events = append(r.events, "write")
	return r.text.Write(p)
}

// spec: §4.8
// Coding-agent eval 2026-11-17 (render-examples/fastapi from source): the app
// ran `python main.py`, which exited 0, and the deploy failed as internal
// because PID 1 exited without draining the console. The console showed only
// "guest-init: app " before the kernel's panic dump. PID 1 drains the console
// before it exits on every path, and the exit status stays the one vmmd reads
// from the kernel's "exitcode=" line.
func TestFinishBootDrainsTheConsoleOnEveryPath(t *testing.T) {
	for _, tc := range []struct {
		name       string
		err        error
		wantStatus int
		wantText   string
		wantEvents []string
	}{
		{name: "clean exit", wantStatus: 0, wantEvents: []string{"drain"}},
		{
			name:       "boot error",
			err:        errors.New("app crash-looped after 3 restart(s): exit status 1"),
			wantStatus: 1,
			wantText:   "guest-init: app crash-looped after 3 restart(s): exit status 1\n",
			wantEvents: []string{"write", "drain"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			console := &consoleRecorder{}
			status := finishBoot(tc.err, console, func() { console.events = append(console.events, "drain") })
			if status != tc.wantStatus {
				t.Fatalf("status = %d, want %d", status, tc.wantStatus)
			}
			if got := console.text.String(); got != tc.wantText {
				t.Fatalf("console = %q, want %q", got, tc.wantText)
			}
			if !slices.Equal(console.events, tc.wantEvents) {
				t.Fatalf("console events = %v, want %v", console.events, tc.wantEvents)
			}
		})
	}
}
