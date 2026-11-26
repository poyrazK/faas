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
		// prod 2026-11-20: console.error("WARN slow query took 1200ms")
		// was missed by --level warn.
		{"WARN slow query took 1200ms", loglevel.Warn},
		// Prod hunt r32: a Node app that called process.exit(3) left only
		// guest-init's restart line, and --level error found nothing.
		{"guest-init: app restart (restart 1/3 policy=on-failure): run [/bin/bash -c npm run start]: exit status 3", loglevel.Error},
		{"guest-init: main restart (restart 2/3 policy=always): exit status 1", loglevel.Error},
		{"guest-init: app restart (restart 1/3 policy=always): <nil>", ""},
		{"guest-init: app out of memory: the kernel killed a workload process (peak 512 MB)", loglevel.Error},
		{"guest-init: build failed: exit status 1", loglevel.Error},
		{"guest-init: app crash-looped after 3 restart(s): exit status 3", loglevel.Error},
		{"vmmd: app restart budget exhausted after 3 restart(s)", loglevel.Error},
		{"guest-init: app exited cleanly (status 0, policy=on-failure); a clean exit is not restarted", ""},
		{"WARNING disk 91% full", loglevel.Warn},
		{"ERROR payment declined", loglevel.Error},
		{"INFO listening on :8080", loglevel.Info},
		{"WARNINGS are counted per minute", ""},
		{"Warn users before expiry", ""},
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
