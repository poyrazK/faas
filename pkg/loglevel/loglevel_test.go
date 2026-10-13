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
