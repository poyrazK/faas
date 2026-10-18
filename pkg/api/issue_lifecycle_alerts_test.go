package api

import (
	"slices"
	"testing"
)

func TestNormalizeIssueLifecycleAlertPolicy(t *testing.T) {
	for _, tc := range []struct {
		names    []string
		cooldown int64
		valid    bool
	}{
		{nil, 0, false}, {[]string{}, 0, true}, {[]string{"staging", "production"}, 604800, true},
		{[]string{"production"}, -1, false}, {[]string{}, 604801, false},
		{[]string{"production", "production"}, 0, false}, {[]string{"*"}, 0, false},
		{[]string{""}, 0, false}, {[]string{"Production"}, 0, false},
		{make([]string, IssueLifecycleAlertMaxEnvironments+1), 0, false},
	} {
		_, err := NormalizeIssueLifecycleAlertPolicy(tc.names, tc.cooldown)
		if (err == nil) != tc.valid {
			t.Fatalf("names=%v cooldown=%d: %v", tc.names, tc.cooldown, err)
		}
	}
	in := []string{"staging", "production"}
	out, err := NormalizeIssueLifecycleAlertPolicy(in, 60)
	if err != nil || !slices.Equal(out, []string{"production", "staging"}) || in[0] != "staging" {
		t.Fatalf("normalization mutated input: %v %v %v", in, out, err)
	}
}
