package main

import (
	"strings"
	"testing"
)

func TestRootHealthPathHint(t *testing.T) {
	for _, tc := range []struct {
		path string
		want bool
	}{{"/", true}, {" / ", true}, {"/healthz", false}, {"", false}, {"/ready", false}} {
		got := rootHealthPathHint(tc.path)
		if (got != "") != tc.want {
			t.Fatalf("rootHealthPathHint(%q) = %q, want hint %v", tc.path, got, tc.want)
		}
		if tc.want && !strings.Contains(got, "/healthz") {
			t.Fatalf("hint %q does not name a replacement route", got)
		}
	}
}
