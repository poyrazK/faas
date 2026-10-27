// adr: 20261026195952
package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

func TestMCPHostedCLIRunsWithNothingOfTheHost(t *testing.T) {
	t.Setenv("HOST_ONLY_MARKER", "leaked")
	t.Setenv("FAAS_PROFILE", "host-profile")
	script := filepath.Join(t.TempDir(), "probe.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf 'ARGS %s\\n' \"$*\"\nprintf 'PWD %s\\n' \"$(pwd)\"\nenv\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	run := main.MCPHostedCLI(t, script, "https://api.gregale.test", "fp_grant_token")
	result, err := run(t.Context(), []string{"crons", "list", "--app", "alpha"}, nil)
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("run: %v %+v", err, result)
	}
	out := string(result.Stdout)
	env := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if name, value, ok := strings.Cut(line, "="); ok && !strings.HasPrefix(line, "ARGS ") && !strings.HasPrefix(line, "PWD ") {
			env[name] = value
		}
	}
	if !strings.Contains(out, "ARGS --non-interactive --json crons list --app alpha") {
		t.Fatalf("args: %s", out)
	}
	if env["FAAS_TOKEN"] != "fp_grant_token" || env["FAAS_API"] != "https://api.gregale.test" || env["GIT_CONFIG_GLOBAL"] != os.DevNull || env["GIT_CONFIG_NOSYSTEM"] != "1" {
		t.Fatalf("env: %v", env)
	}
	for _, leaked := range []string{"HOST_ONLY_MARKER", "FAAS_PROFILE"} {
		if _, ok := env[leaked]; ok {
			t.Fatalf("%s reached the command", leaked)
		}
	}
	home := env["HOME"]
	if home == "" || home == os.Getenv("HOME") || !strings.Contains(out, "PWD "+home) && !strings.Contains(out, "PWD /private"+home) {
		t.Fatalf("home %q, output %s", home, out)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("private home %s survived the command: %v", home, err)
	}
}
