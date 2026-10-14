package main

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestFormatSuspectedDependency(t *testing.T) {
	for name, tc := range map[string]struct {
		suspect *api.DebugSuspectedDependency
		want    string
	}{
		"none": {want: "-"},
		"latency stored before failure attribution": {
			suspect: &api.DebugSuspectedDependency{Type: "app_dependency", Kind: "postgresql", Name: "SELECT orders", P95BaseMS: 82, P95MS: 191},
			want:    `postgresql "SELECT orders" 82→191ms`,
		},
		"failures": {
			suspect: &api.DebugSuspectedDependency{Type: "app_dependency", Kind: "http", Name: "api.stripe.com", Reason: "failures", BaselineErrorRatePct: 0, ErrorRatePct: 12.5, ErrorType: "503"},
			want:    `http "api.stripe.com" errors 0→12.5% 503`,
		},
		"failures without a type": {
			suspect: &api.DebugSuspectedDependency{Type: "managed_binding", Name: "orders-db", Reason: "failures", BaselineErrorRatePct: 1.25, ErrorRatePct: 9},
			want:    `managed_binding "orders-db" errors 1.25→9%`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			if got := formatSuspectedDependency(tc.suspect); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
