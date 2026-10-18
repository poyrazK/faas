package main

import "testing"

// TestFlagSetCommandName pins H10-123: `gregale workflows list h5-wf`
// printed "usage: gregale workflows-list [flags]", a name nobody can type.
func TestFlagSetCommandName(t *testing.T) {
	cases := map[string]string{
		"workflows-list":   "workflows list",
		"wake-timeline":    "wake-timeline",
		"app health":       "app health",
		"apps-rm":          "apps rm",
		"no-such-command":  "no-such-command",
		"workflows-nosuch": "workflows-nosuch",
	}
	for in, want := range cases {
		if got := flagSetCommandName(in); got != want {
			t.Errorf("flagSetCommandName(%q) = %q, want %q", in, got, want)
		}
	}
}
