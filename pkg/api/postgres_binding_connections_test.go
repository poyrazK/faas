package api

import (
	"strings"
	"testing"
)

func TestParsePostgresPoolMax(t *testing.T) {
	for value, want := range map[string]int{"": 0, "abc": 0, "0": 0, "-3": 0, "1001": 0, " 7 ": 7, "1": 1, "1000": 1000} {
		if got := ParsePostgresPoolMax(value); got != want {
			t.Errorf("ParsePostgresPoolMax(%q) = %d, want %d", value, got, want)
		}
	}
}

func TestAssessPostgresBindingConnections(t *testing.T) {
	usable := func(n int64) *int64 { return &n }
	for _, tc := range []struct {
		name                    string
		poolMax, maxConcurrency int
		usable                  *int64
		status                  string
		total                   int
		detail                  string
	}{
		{name: "pool size unset", poolMax: 0, maxConcurrency: 3, usable: usable(50), status: "not_checked", detail: PostgresPoolMaxEnv},
		{name: "instance limit unknown", poolMax: 5, maxConcurrency: 0, status: "not_checked", detail: "instance limit"},
		{name: "no qualified allocation", poolMax: 5, maxConcurrency: 3, status: "not_checked", total: 20, detail: "no qualified connection allocation"},
		{name: "fits", poolMax: 5, maxConcurrency: 3, usable: usable(20), status: "passed", total: 20, detail: "up to 20 connections (4 instances × pool of 5)"},
		{name: "over the allocation", poolMax: 5, maxConcurrency: 3, usable: usable(19), status: "warning", total: 20, detail: "more than the 19"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := AssessPostgresBindingConnections(tc.poolMax, tc.maxConcurrency, tc.usable)
			if got.Status != tc.status || got.Total != tc.total || !strings.Contains(got.Detail, tc.detail) {
				t.Fatalf("got %+v, want status %s total %d detail containing %q", got, tc.status, tc.total, tc.detail)
			}
			if tc.total > 0 && got.Instances != tc.maxConcurrency+RolloutConcurrencyGrant {
				t.Fatalf("instances = %d, want max_concurrency + rollout grant", got.Instances)
			}
		})
	}
}
