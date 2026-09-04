package main

import "testing"

func TestJobsDispatchEnabledRequiresExactOne(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{"", false},
		{"0", false},
		{"false", false},
		{"yes", false},
		{" 1 ", true},
		{"1", true},
	} {
		if got := jobsDispatchEnabled(tc.value); got != tc.want {
			t.Errorf("jobsDispatchEnabled(%q) = %v, want %v", tc.value, got, tc.want)
		}
	}
}
