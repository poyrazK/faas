package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestRunExitCodes(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want int
	}{
		{"no args prints help", nil, 0},
		{"version", []string{"version"}, 0},
		{"version flag", []string{"--version"}, 0},
		{"help", []string{"help"}, 0},
		{"unknown command", []string{"frobnicate"}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := run(tt.args); got != tt.want {
				t.Errorf("run(%v) = %d, want %d", tt.args, got, tt.want)
			}
		})
	}
}

func TestRunJSONLocalErrorsAreProblemObjects(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "unknown command", args: []string{"--json", "frobnicate"}},
		{name: "missing positional", args: []string{"--json", "app"}},
		{name: "unknown flag", args: []string{"--json", "deployments", "--definitely-invalid"}},
		{name: "unknown flag with custom usage", args: []string{"--json", "login", "--definitely-invalid"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetJSONOutput()
			stderr, restore := captureStderr(t)
			code := run(tc.args)
			restore()
			if code == 0 {
				t.Fatalf("run(%v) returned success", tc.args)
			}
			lines := strings.Split(strings.TrimSpace(stderr.String()), "\n")
			if len(lines) != 1 {
				t.Fatalf("stderr has %d lines, want one JSON object: %q", len(lines), stderr.String())
			}
			var p api.Problem
			if err := json.Unmarshal([]byte(lines[0]), &p); err != nil {
				t.Fatalf("stderr is not a Problem object: %v; output=%q", err, stderr.String())
			}
			if p.Status != 400 || p.Code == "" || p.Title == "" || p.Detail == "" {
				t.Fatalf("incomplete local Problem: %+v", p)
			}
			resetJSONOutput()
		})
	}
}

func TestRunParentHelpNeverRequiresAuthentication(t *testing.T) {
	t.Setenv("FAAS_API", "http://127.0.0.1:1")
	t.Setenv("FAAS_TOKEN", "")
	for _, command := range []string{"app", "secrets", "env", "inspect"} {
		t.Run(command, func(t *testing.T) {
			stdout, restore := captureStdout(t)
			code := run([]string{command, "--help"})
			restore()
			if code != 0 {
				t.Fatalf("%s --help exit = %d, want 0", command, code)
			}
			if !strings.Contains(stdout.String(), "gregale "+command) {
				t.Fatalf("%s --help did not render local help: %q", command, stdout.String())
			}
		})
	}
}
