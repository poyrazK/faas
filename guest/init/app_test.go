package main

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestBuildEnvMergeAndOverride(t *testing.T) {
	base := []string{"PATH=/usr/bin", "HOME=/root"}
	m := api.AppManifest{Env: map[string]string{"HOME": "/home/app", "NODE_ENV": "production"}}
	got := BuildEnv(base, m)
	want := []string{"HOME=/home/app", "NODE_ENV=production", "PATH=/usr/bin"} // sorted, HOME overridden
	if !reflect.DeepEqual(got, want) {
		t.Errorf("BuildEnv = %v, want %v", got, want)
	}
}

func TestBuildEnvDeterministic(t *testing.T) {
	m := api.AppManifest{Env: map[string]string{"B": "2", "A": "1", "C": "3"}}
	first := BuildEnv(nil, m)
	for i := 0; i < 20; i++ {
		if !reflect.DeepEqual(BuildEnv(nil, m), first) {
			t.Fatal("BuildEnv output is not deterministic across runs")
		}
	}
	if !reflect.DeepEqual(first, []string{"A=1", "B=2", "C=3"}) {
		t.Errorf("unsorted output: %v", first)
	}
}

func TestSupervisorCleanExit(t *testing.T) {
	starts := 0
	s := Supervisor{Max: 3, Start: func() error { starts++; return nil }}
	if err := s.Run(); err != nil {
		t.Fatalf("clean exit should return nil, got %v", err)
	}
	if starts != 1 {
		t.Errorf("clean exit should start once, started %d", starts)
	}
}

func TestSupervisorRestartsThenGivesUp(t *testing.T) {
	starts := 0
	crashes := 0
	s := Supervisor{
		Max:     3,
		Start:   func() error { starts++; return fmt.Errorf("boom") },
		OnCrash: func(int, error) { crashes++ },
	}
	err := s.Run()
	if err == nil {
		t.Fatal("perpetual crash should exhaust the budget and error")
	}
	// 1 initial start + 3 restarts = 4 total starts.
	if starts != 4 {
		t.Errorf("expected 4 starts (1 + %d restarts), got %d", MaxRestarts, starts)
	}
	if crashes != 3 {
		t.Errorf("expected 3 crash hooks, got %d", crashes)
	}
}

func TestSupervisorRecoversBeforeBudget(t *testing.T) {
	starts := 0
	s := Supervisor{Max: 3, Start: func() error {
		starts++
		if starts < 3 {
			return fmt.Errorf("flaky")
		}
		return nil // succeeds on the 3rd start
	}}
	if err := s.Run(); err != nil {
		t.Fatalf("should recover, got %v", err)
	}
	if starts != 3 {
		t.Errorf("expected 3 starts, got %d", starts)
	}
}

// cut splits "KEY=VALUE". It must:
//   - return ("", "", false) for the empty string
//   - return (key, "", true) for entries without '=' (treated as KEY="")
//   - return (k, v, true) for "K=V"
func TestCut(t *testing.T) {
	cases := []struct {
		in     string
		wantK  string
		wantV  string
		wantOK bool
	}{
		{"", "", "", false},
		{"NOEQUALS", "NOEQUALS", "", true},
		{"=value-only", "", "value-only", true},
		{"key=", "key", "", true},
		{"KEY=value", "KEY", "value", true},
		{"KEY=value=with=equals", "KEY", "value=with=equals", true},
	}
	for _, tc := range cases {
		gotK, gotV, gotOK := cut(tc.in)
		if gotK != tc.wantK || gotV != tc.wantV || gotOK != tc.wantOK {
			t.Errorf("cut(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tc.in, gotK, gotV, gotOK, tc.wantK, tc.wantV, tc.wantOK)
		}
	}
}

// BuildEnv must skip entries that cut() flags as invalid (only "" is invalid),
// but treat "NOEQUALS" entries as KEY="" — both pass through to the merged map.
func TestBuildEnv_HandlesEdgeEntries(t *testing.T) {
	base := []string{"", "FOO=bar", "BAZ"}
	m := api.AppManifest{Env: map[string]string{"NEW": "v"}}
	got := BuildEnv(base, m)
	want := []string{"BAZ=", "FOO=bar", "NEW=v"} // sorted; "" dropped; "BAZ" → "BAZ="
	if !reflect.DeepEqual(got, want) {
		t.Errorf("BuildEnv edge entries = %v, want %v", got, want)
	}
}
