package api

import (
	"strings"
	"testing"
)

func TestRecoveryCatalogRequestRejectsMutableRefsAndMalformedCursors(t *testing.T) {
	for _, tc := range []struct {
		sha, before string
		valid       bool
	}{
		{strings.Repeat("a", 40), "", true}, {strings.Repeat("b", 40), "2ba0aa40-2362-4c0e-aeb1-6381666bb514", true},
		{"main", "", false}, {strings.Repeat("A", 40), "", false}, {strings.Repeat("g", 40), "", false}, {strings.Repeat("a", 40), "foreign/cursor", false},
	} {
		if got := (ListEnvironmentGitReleasesRequest{TargetCommitSHA: tc.sha, Before: tc.before}).Validate() == nil; got != tc.valid {
			t.Fatalf("%+v accepted=%v", tc, got)
		}
	}
}
