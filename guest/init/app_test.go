package main

import (
	"fmt"
	"reflect"
	"strings"
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

func TestSupervisorRestartPolicyNoDoesNotRestart(t *testing.T) {
	starts := 0
	s := Supervisor{
		Max:    3,
		Policy: api.RestartPolicyNo,
		Start: func() error {
			starts++
			return fmt.Errorf("boom")
		},
	}
	if err := s.Run(); err == nil {
		t.Fatal("restart policy no should return the workload error")
	}
	if starts != 1 {
		t.Fatalf("restart policy no started %d times, want 1", starts)
	}
	if got := s.lastErr(); got == nil {
		t.Fatal("terminal workload error was not retained")
	}
}

func TestSupervisorRestartPolicyAlwaysRestartsCleanExit(t *testing.T) {
	starts := 0
	s := Supervisor{
		Max:    2,
		Policy: api.RestartPolicyAlways,
		Start: func() error {
			starts++
			return nil
		},
	}
	if err := s.Run(); err == nil {
		t.Fatal("always policy should report exhausted restart budget")
	}
	if starts != 3 {
		t.Fatalf("always policy started %d times, want 3", starts)
	}
}

func TestSupervisorExplicitStopWinsOverAlwaysPolicy(t *testing.T) {
	starts := 0
	s := Supervisor{
		Max:    3,
		Policy: api.RestartPolicyAlways,
	}
	s.Start = func() error {
		starts++
		s.RequestStop()
		return fmt.Errorf("terminated")
	}
	if err := s.Run(); err != nil {
		t.Fatalf("explicit stop should be clean, got %v", err)
	}
	if starts != 1 {
		t.Fatalf("explicit stop restarted %d times, want 1", starts)
	}
}

func TestSupervisorPolicyFromManifestUsesConfiguredRetryBudget(t *testing.T) {
	policy, max := supervisorPolicyFromManifest(api.AppManifest{
		ExecutionMode: api.ExecutionModeService,
		MaxRetries:    7,
	})
	if policy != api.RestartPolicyAlways {
		t.Fatalf("policy = %q, want %q", policy, api.RestartPolicyAlways)
	}
	if max != 7 {
		t.Fatalf("max retries = %d, want 7", max)
	}
}

func TestSupervisorPolicyFromManifestPreservesLegacyDefault(t *testing.T) {
	policy, max := supervisorPolicyFromManifest(api.AppManifest{})
	if policy != api.RestartPolicyOnFailure {
		t.Fatalf("policy = %q, want %q", policy, api.RestartPolicyOnFailure)
	}
	if max != MaxRestarts {
		t.Fatalf("max retries = %d, want %d", max, MaxRestarts)
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

// TestBuildEnv_FourLayerPrecedence (issue #395 / ADR-045 acceptance #4):
// pins the 4-layer merge order "OS environ < manifest env < api_env <
// secrets" in a single fixture. Each layer contributes a different value
// for the same key, and the test asserts the winner. A future refactor
// that reorders the layers (e.g. applies secrets before api_env, or
// drops the apiEnv merge) trips here.
//
// Key shape:
//   - FOO appears in all four layers → secrets wins ("s")
//   - BAR appears in os + manifest + api_env → api_env wins ("a")
//   - BAZ appears in manifest only → manifest wins ("m")
//   - QUX appears in api_env only → api_env wins ("a")
//   - ONLYOS appears in os only → os wins ("o")
//
// The fixture keeps each layer's contribution distinct so a reordering
// bug shows up as a wrong value, not as a missing-key failure.
func TestBuildEnv_FourLayerPrecedence(t *testing.T) {
	base := []string{"FOO=o", "BAR=o", "ONLYOS=o"}
	m := api.AppManifest{
		Env: map[string]string{
			"FOO": "m",
			"BAR": "m",
			"BAZ": "m",
		},
	}
	apiEnv := map[string]string{
		"FOO": "a",
		"BAR": "a",
		"QUX": "a",
	}
	secrets := map[string]string{
		"FOO": "s",
	}
	got := BuildEnvWithSecrets(base, m, secrets, apiEnv)
	want := []string{
		"BAR=a",    // api_env beats manifest+os
		"BAZ=m",    // manifest only
		"FOO=s",    // secrets beats all
		"ONLYOS=o", // os only
		"QUX=a",    // api_env only
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("BuildEnv 4-layer precedence = %v, want %v", got, want)
	}
}

// TestStampOverridePortEnv_AppendsLast pins issue #460 / ADR-053
// (PR-C): the platform contract for the per-deployment override port
// must reach the runner as PORT=<port>, appended AFTER BuildEnv so
// a customer-set PORT in manifest env, apiEnv, or sealed secrets
// cannot accidentally override the contract. The helper is the
// thin shell around the production runAppWithEnv's append — both
// paths share the same line, so this test pins the production
// behavior without launching the customer's process.
func TestStampOverridePortEnv_AppendsLast(t *testing.T) {
	tests := []struct {
		name     string
		env      []string
		port     int
		wantLast string
	}{
		{
			name:     "zero port → PORT=0 (the 0→8080 resolution lives in m.EffectivePort(), not here)",
			env:      []string{"PATH=/usr/bin", "HOME=/root"},
			port:     0,
			wantLast: "PORT=0",
		},
		{
			name:     "explicit 9090",
			env:      []string{"PATH=/usr/bin"},
			port:     9090,
			wantLast: "PORT=9090",
		},
		{
			name:     "explicit 3000",
			env:      nil,
			port:     3000,
			wantLast: "PORT=3000",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StampOverridePortEnv(tt.env, tt.port)
			if len(got) != len(tt.env)+1 {
				t.Fatalf("StampOverridePortEnv len = %d, want %d", len(got), len(tt.env)+1)
			}
			// Appended LAST (defense against any layer that earlier
			// injected a PORT key — this helper is the platform
			// contract).
			if got[len(got)-1] != tt.wantLast {
				t.Errorf("last env = %q, want %q", got[len(got)-1], tt.wantLast)
			}
			// Earlier entries preserved.
			for i, e := range tt.env {
				if got[i] != e {
					t.Errorf("entry %d = %q, want %q", i, got[i], e)
				}
			}
		})
	}
}

// TestStampOverridePortEnv_AppendsAfterManifestPort pins the
// precedence invariant: even when BuildEnv put PORT=9090 (from a
// customer image's manifest env), StampOverridePortEnv appends the
// resolved platform port LAST, so the platform contract is the
// final entry on the wire. Execve semantics treat the slice as a
// bag of KEY=VALUE pairs (last-write-wins per key); the appended
// position is the strongest guarantee the platform has against a
// later layer accidentally re-introducing a PORT key.
func TestStampOverridePortEnv_AppendsAfterManifestPort(t *testing.T) {
	m := api.AppManifest{Env: map[string]string{"PORT": "9090"}}
	merged := BuildEnv([]string{"PATH=/usr/bin"}, m)
	// Customer wrote 9090 via manifest env. Platform contract is
	// EffectivePort()=8080 here (Port=0 falls back). The helper
	// appends "PORT=8080" AFTER the merged slice, so the last
	// entry on the wire is the platform value.
	out := StampOverridePortEnv(merged, 8080)
	if out[len(out)-1] != "PORT=8080" {
		t.Errorf("last PORT = %q, want PORT=8080 (platform contract)", out[len(out)-1])
	}
}

// TestStampTraceparentEnv_AppendsWhenNonEmpty pins issue #555 PR-4:
// the W3C trace context the host stashed via the vsock resume hook
// is appended to the runner env. Empty traceparent is a no-op so
// legacy single-box without OTel is unchanged.
func TestStampTraceparentEnv_AppendsWhenNonEmpty(t *testing.T) {
	out := StampTraceparentEnv([]string{"PATH=/usr/bin"}, "0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01")
	want := "TRACEPARENT=0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"
	if out[len(out)-1] != want {
		t.Errorf("last env = %q, want %q", out[len(out)-1], want)
	}
}

// TestStampTraceparentEnv_EmptyIsNoOp pins the empty-input contract:
// no TRACEPARENT row is added when the host hasn't shipped a
// traceparent (legacy single-box without OTel, or a test fixture).
func TestStampTraceparentEnv_EmptyIsNoOp(t *testing.T) {
	in := []string{"PATH=/usr/bin"}
	out := StampTraceparentEnv(in, "")
	if len(out) != len(in) {
		t.Errorf("empty traceparent mutated env: in=%v out=%v", in, out)
	}
	for _, e := range out {
		if strings.HasPrefix(e, "TRACEPARENT=") {
			t.Errorf("empty traceparent added a TRACEPARENT row: %v", e)
		}
	}
}
