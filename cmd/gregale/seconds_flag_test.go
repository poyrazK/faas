package main

import (
	"flag"
	"io"
	"testing"
)

// production-us hunt #4 (H4-43): `deployment wait --timeout 60s` was a parse
// error while `rollback --timeout 5m` worked. Every seconds-valued --timeout
// accepts both forms.
func TestSecondsOrDurationFlag(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want int
		ok   bool
	}{
		{raw: "600", want: 600, ok: true},
		{raw: "60s", want: 60, ok: true},
		{raw: "10m", want: 600, ok: true},
		{raw: "1500ms", want: 2, ok: true},
		{raw: "soon", ok: false},
	} {
		fs := flag.NewFlagSet("t", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		got := secondsOrDurationFlag(fs, "timeout", 1200, "")
		err := fs.Parse([]string{"--timeout", tc.raw})
		if (err == nil) != tc.ok || (tc.ok && *got != tc.want) {
			t.Errorf("--timeout %s = (%d, %v), want (%d, ok=%v)", tc.raw, *got, err, tc.want, tc.ok)
		}
	}
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	if got := secondsOrDurationFlag(fs, "timeout", 1200, ""); *got != 1200 {
		t.Fatalf("default = %d, want 1200", *got)
	}
}
