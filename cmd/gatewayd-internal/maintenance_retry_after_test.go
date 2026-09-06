package main

import (
	"strconv"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// maintenanceRuleFixture builds one enabled kind=maintenance rule
// carrying retryAfter (0 = "no per-rule value", the case that
// inherits the platform default). Mirrors sampleRouteRule in
// edge_rules_test.go.
func maintenanceRuleFixture(retryAfter int) []state.EdgeRule {
	return []state.EdgeRule{{
		ID:        "er_maint",
		AccountID: "acc_test",
		AppID:     "app_test",
		MatchHost: "example.com",
		MatchPath: "/*",
		Priority:  1,
		Enabled:   true,
		Kind:      state.EdgeRuleKindMaintenance,
		Action: state.EdgeRuleAction{
			Kind:        state.EdgeRuleKindMaintenance,
			Maintenance: &state.EdgeRuleMaintenanceAction{RetryAfterSeconds: retryAfter},
		},
	}}
}

// TestParseMaintenanceRetryAfter pins the issue #899 finding-3
// override. Unset yields the platform default; anything malformed is
// an error the caller turns into a failed boot, per the operator-
// override convention cmd/schedd/main.go set for the rebalancer
// tunables ("operator typo must surface at boot").
func TestParseMaintenanceRetryAfter(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		want    int
		wantErr bool
	}{
		{name: "unset", raw: "", want: api.EdgeRuleMaintenanceRetryAfterSeconds},
		{name: "valid-min", raw: "1", want: 1},
		{name: "valid-typical", raw: "300", want: 300},
		{
			name: "valid-at-cap",
			raw:  strconv.Itoa(api.MaxEdgeRuleMaintenanceRetryAfterSeconds),
			want: api.MaxEdgeRuleMaintenanceRetryAfterSeconds,
		},
		// RFC 7231 forbids Retry-After: 0, so 0 is an error rather
		// than a silent clamp — the operator meant something the
		// platform cannot express.
		{name: "zero-rejected", raw: "0", wantErr: true},
		{name: "negative-rejected", raw: "-5", wantErr: true},
		{
			name:    "over-cap-rejected",
			raw:     strconv.Itoa(api.MaxEdgeRuleMaintenanceRetryAfterSeconds + 1),
			wantErr: true,
		},
		{name: "non-integer-rejected", raw: "60s", wantErr: true},
		{name: "whitespace-rejected", raw: " 60", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseMaintenanceRetryAfter(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got %d", got)
				}
				if !strings.Contains(err.Error(), envMaintenanceRetryAfterSeconds) {
					t.Errorf("error %q does not name the env var", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("value = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestCompileMaintenanceRulesDefaultRetry pins that the compile-time
// default tracks the override: an unset override (0) resolves to the
// 60 s platform constant, and a tuned value flows through to a rule
// that carries no per-rule Retry-After.
func TestCompileMaintenanceRulesDefaultRetry(t *testing.T) {
	for _, tc := range []struct {
		name         string
		defaultRetry int
		want         int
	}{
		{name: "unset-falls-back", defaultRetry: 0, want: api.EdgeRuleMaintenanceRetryAfterSeconds},
		{name: "override-applies", defaultRetry: 300, want: 300},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rules := maintenanceRuleFixture(0)
			out, errs := compileMaintenanceRules(rules, tc.defaultRetry)
			if len(errs) != 0 {
				t.Fatalf("unexpected compile errors: %v", errs)
			}
			if len(out) != 1 {
				t.Fatalf("compiled %d rules, want 1", len(out))
			}
			if out[0].RetryAfterSeconds != tc.want {
				t.Errorf("RetryAfterSeconds = %d, want %d", out[0].RetryAfterSeconds, tc.want)
			}
		})
	}
}

// TestCompileMaintenanceRulesPerRuleWins asserts the override is only
// a default: a rule carrying its own Retry-After keeps it.
func TestCompileMaintenanceRulesPerRuleWins(t *testing.T) {
	out, errs := compileMaintenanceRules(maintenanceRuleFixture(45), 300)
	if len(errs) != 0 {
		t.Fatalf("unexpected compile errors: %v", errs)
	}
	if len(out) != 1 || out[0].RetryAfterSeconds != 45 {
		t.Fatalf("per-rule value must win, got %+v", out)
	}
}
