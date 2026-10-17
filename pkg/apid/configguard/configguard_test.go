package configguard

import (
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestJudge(t *testing.T) {
	for _, tc := range []struct {
		name     string
		sample   state.ConfigGuardSample
		failed   bool
		wantRule string
	}{
		{"no traffic yet", state.ConfigGuardSample{}, false, RuleClean},
		{"crashes at boot, quiet app", state.ConfigGuardSample{NewRequests: 6, NewServerErrors: 6}, true, RuleFirstWake5xx},
		{"four failures are not decisive", state.ConfigGuardSample{NewRequests: 4, NewServerErrors: 4}, false, RuleClean},
		{"mostly healthy below the bar", state.ConfigGuardSample{NewRequests: 12, NewServerErrors: 5, OldRequests: 5}, false, RuleClean},
		{"fails against a healthy old config", state.ConfigGuardSample{NewRequests: 10, NewServerErrors: 9, OldRequests: 400, OldServerErrors: 2}, true, RuleFirstWake5xx},
		{"a fix for a broken app is kept", state.ConfigGuardSample{NewRequests: 10, NewServerErrors: 6, OldRequests: 50, OldServerErrors: 50}, false, RuleClean},
		{"regression on full samples", state.ConfigGuardSample{NewRequests: 100, NewServerErrors: 20, OldRequests: 100, OldServerErrors: 1}, true, Rule5xxRegression},
		{"same error rate as before", state.ConfigGuardSample{NewRequests: 100, NewServerErrors: 3, OldRequests: 100, OldServerErrors: 3}, false, RuleClean},
	} {
		t.Run(tc.name, func(t *testing.T) {
			decision, failed := Judge(tc.sample)
			if failed != tc.failed || decision.Rule != tc.wantRule {
				t.Fatalf("Judge(%+v) = %q failed=%v, want %q failed=%v (%s)", tc.sample, decision.Rule, failed, tc.wantRule, tc.failed, decision.Summary)
			}
			if decision.NewRequests != tc.sample.NewRequests || decision.OldServerErrors != tc.sample.OldServerErrors || decision.Summary == "" {
				t.Fatalf("decision %+v does not carry both samples and a summary", decision)
			}
		})
	}
}

func TestVariablesToward(t *testing.T) {
	got := VariablesToward(map[string]string{"A": "1", "B": "2"}, map[string]string{"B": "3", "C": "4"})
	want := []VariableWrite{{Key: "A", Value: "1"}, {Key: "B", Value: "2"}, {Key: "C", Delete: true}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("VariablesToward = %+v, want %+v", got, want)
	}
	if got := VariablesToward(map[string]string{"A": "1"}, map[string]string{"A": "1"}); len(got) != 0 {
		t.Fatalf("no-op rollback writes %+v", got)
	}
}

func TestGuardVariableRestore(t *testing.T) {
	base := map[string]string{"DATABASE_URL": "postgres://good", "MODE": "a", "GONE": "x"}
	head := map[string]string{"DATABASE_URL": "postgres://bad", "MODE": "a", "NEW": "y"}
	for _, tc := range []struct {
		name    string
		current map[string]string
		want    []VariableWrite
		ok      bool
	}{
		{"undoes only the guard's keys", map[string]string{"DATABASE_URL": "postgres://bad", "MODE": "a", "NEW": "y", "OTHER": "z"},
			[]VariableWrite{{Key: "DATABASE_URL", Value: "postgres://good"}, {Key: "GONE", Value: "x"}, {Key: "NEW", Delete: true}}, true},
		{"refuses when a guarded key changed outside the guard", map[string]string{"DATABASE_URL": "postgres://other", "MODE": "a", "NEW": "y"}, nil, false},
		{"refuses when a deleted key came back", map[string]string{"DATABASE_URL": "postgres://bad", "MODE": "a", "NEW": "y", "GONE": "x"}, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := GuardVariableRestore(base, head, tc.current)
			if ok != tc.ok || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("GuardVariableRestore = %+v ok=%v, want %+v ok=%v", got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestGuardSecretsUnchangedAndDiffering(t *testing.T) {
	head := map[string]state.ConfigSecretFingerprint{"TOKEN": {Version: 3, ValueHash: "h3"}}
	retained := []state.ConfigGuardSecret{{Key: "TOKEN", Existed: true}, {Key: "ADDED"}}
	if !GuardSecretsUnchanged(retained, head, map[string]state.ConfigSecretFingerprint{"TOKEN": {Version: 3, ValueHash: "h3"}}) {
		t.Fatal("unchanged secrets reported as changed")
	}
	if GuardSecretsUnchanged(retained, head, map[string]state.ConfigSecretFingerprint{"TOKEN": {Version: 4, ValueHash: "h4"}}) {
		t.Fatal("a secret rotated outside the guard was not noticed")
	}
	got := SecretsDiffering(map[string]state.ConfigSecretFingerprint{"A": {Version: 1}, "B": {Version: 2}},
		map[string]state.ConfigSecretFingerprint{"B": {Version: 3}, "C": {Version: 1}})
	if want := []string{"A", "B", "C"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("SecretsDiffering = %v, want %v", got, want)
	}
}
