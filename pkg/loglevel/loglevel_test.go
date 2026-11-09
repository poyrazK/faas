package loglevel_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/loglevel"
)

func TestDetect(t *testing.T) {
	tests := []struct {
		line string
		want string
	}{
		{"[ERROR] payment declined", loglevel.Error},
		{`{"level":"error","msg":"boom"}`, loglevel.Error},
		{"time=1 level=WARN msg=slow", loglevel.Warn},
		{"[warning] retrying", loglevel.Warn},
		{"[INFO] listening on :8080", loglevel.Info},
		{"[info] retry then [error] gave up", loglevel.Error},
		{"an error occurred in prose", ""},
		{"", ""},
		// adr: 20261109153000 — the default shapes of an uncaught crash and
		// of fatal or critical lines are errors.
		{"ERROR:    Exception in ASGI application", loglevel.Error},
		{"ERROR:root:payment declined", loglevel.Error},
		{"CRITICAL:root:out of disk", loglevel.Error},
		{"WARNING:root:slow query", loglevel.Warn},
		{"INFO:     Uvicorn running on http://0.0.0.0:8080", loglevel.Info},
		{"Traceback (most recent call last):", loglevel.Error},
		{"panic: runtime error: index out of range [3] with length 3", loglevel.Error},
		{"fatal error: concurrent map writes", loglevel.Error},
		{"TypeError: Cannot read properties of undefined (reading 'id')", loglevel.Error},
		{"Error: connect ECONNREFUSED 127.0.0.1:5432", loglevel.Error},
		{"    at Object.<anonymous> (/app/index.js:3:9)", ""},
		{"[FATAL] cannot open database", loglevel.Error},
		{"time=1 level=fatal msg=exit", loglevel.Error},
		// Prose stays unclassified: markers must start the line, keep their
		// case, and an error name needs its ": ".
		{"the request failed with ERROR: code 7", ""},
		{"error: lowercase prefixes are prose", ""},
		{"no panic: all good", ""},
		{"Errors are expected during warmup", ""},
		{"see TypeError for details", ""},
	}
	for _, tt := range tests {
		if got := loglevel.Detect(tt.line); got != tt.want {
			t.Errorf("Detect(%q) = %q, want %q", tt.line, got, tt.want)
		}
	}
}

func TestRank(t *testing.T) {
	if !(loglevel.Rank(loglevel.Info) < loglevel.Rank(loglevel.Warn) && loglevel.Rank(loglevel.Warn) < loglevel.Rank(loglevel.Error)) {
		t.Fatal("levels must rank info < warn < error")
	}
	if loglevel.Rank("debug") != -1 || loglevel.Rank("") != -1 {
		t.Fatal("unknown levels must rank -1")
	}
}
