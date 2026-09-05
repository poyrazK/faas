package main

import (
	"context"
	"errors"
	"testing"
)

func TestValidateSafeDeployTokenPair(t *testing.T) {
	tests := []struct {
		name        string
		canary      string
		safedeploy  string
		wantErrText string
	}{
		{name: "disabled", canary: "", safedeploy: ""},
		{name: "enabled", canary: "canary-token", safedeploy: "safedeploy-token"},
		{
			name:        "missing safedeploy token",
			canary:      "canary-token",
			wantErrText: "FAAS_CANARY_PROGRESSION_TOKEN is set but FAAS_SAFEDEPLOY_TOKEN is empty",
		},
		{
			name:        "missing canary token",
			safedeploy:  "safedeploy-token",
			wantErrText: "FAAS_SAFEDEPLOY_TOKEN is set but FAAS_CANARY_PROGRESSION_TOKEN is empty",
		},
		{
			name:        "whitespace is unset",
			canary:      " \t\n",
			safedeploy:  "safedeploy-token",
			wantErrText: "FAAS_SAFEDEPLOY_TOKEN is set but FAAS_CANARY_PROGRESSION_TOKEN is empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSafeDeployTokenPair(tt.canary, tt.safedeploy)
			if tt.wantErrText == "" {
				if err != nil {
					t.Fatalf("validateSafeDeployTokenPair() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatal("validateSafeDeployTokenPair() = nil, want error")
			}
			if !errors.Is(err, ErrSafeDeployTokenPairIncomplete) {
				t.Fatalf("error = %v, want ErrSafeDeployTokenPairIncomplete", err)
			}
			if got := err.Error(); got != "meterd: safe-deploy token pair incomplete: "+tt.wantErrText {
				t.Errorf("error = %q, want %q", got, "meterd: safe-deploy token pair incomplete: "+tt.wantErrText)
			}
		})
	}
}

func TestSafeDeployTokenNilEnvIsUnset(t *testing.T) {
	if got := safeDeployToken(nil, "FAAS_SAFEDEPLOY_TOKEN"); got != "" {
		t.Fatalf("safeDeployToken(nil, ...) = %q, want empty", got)
	}
	if got := safeDeployToken(func(string) string { return "  token \n" }, "FAAS_SAFEDEPLOY_TOKEN"); got != "token" {
		t.Fatalf("safeDeployToken() = %q, want trimmed token", got)
	}
}

func TestRunWithDepsRejectsHalfEnabledSafeDeploy(t *testing.T) {
	getenv := func(name string) string {
		if name == "FAAS_CANARY_PROGRESSION_TOKEN" {
			return "canary-token"
		}
		return ""
	}
	deps := runDeps{
		configPath: "/this/path/does/not/exist/meterd-" + t.Name(),
		capCheck:   func() error { return nil },
		getenv:     getenv,
	}

	err := runWithDeps(context.Background(), discardLog(), deps)
	if !errors.Is(err, ErrSafeDeployTokenPairIncomplete) {
		t.Fatalf("runWithDeps() = %v, want ErrSafeDeployTokenPairIncomplete", err)
	}
}
