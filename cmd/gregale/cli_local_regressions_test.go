package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestRunHelpAndVersionWithBrokenConfig(t *testing.T) {
	setupConnectionProfiles(t)
	path, err := cliConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{broken json"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		nil, {"--help"}, {"help", "--all"}, {"help", "deploy"}, {"deploy", "--help"},
		{"deploy", "--help", "--all"}, {"help", "app", "--all"}, {"app", "my-api", "scale", "--help"},
		{"version"}, {"--version"}, {"-v"}, {"--json", "version"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			stdout, stderr, restore := swapIO(t)
			defer restore()
			if code := run(args); code != 0 || stdout.Len() == 0 || stderr() != "" {
				t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr())
			}
		})
	}
	// Operational commands must still reject the broken configuration.
	_, stderr, restore := swapIO(t)
	defer restore()
	if code := run([]string{"whoami"}); code != 1 || !strings.Contains(stderr(), "Invalid connection profile") {
		t.Fatalf("whoami exit=%d stderr=%q", code, stderr())
	}
}

func TestRunJSONErrorsWithUnavailableKeychain(t *testing.T) {
	setupConnectionProfiles(t)
	setFakeKeyring(t, withGetErr(errors.New("no D-Bus session")))
	oldStdin := osStdin
	t.Cleanup(func() { osStdin = oldStdin })
	dest := filepath.Join(t.TempDir(), "out")
	for _, tc := range []struct {
		name string
		args []string
		want int
	}{
		{"not logged in", []string{"whoami", "--json"}, 2},
		{"unknown command", []string{"--json", "deply"}, 1},
		{"unknown template", []string{"init", "--json", "--template", "not-a-template", "--path", dest}, 1},
		{"deploy help-only flag", []string{"deploy", "--all", "--json"}, 1},
		{"app help-only flag", []string{"app", "my-api", "--all", "--json"}, 1},
		{"deploy invalid duration", []string{"deploy", "--timeout", "not-a-duration", "--json"}, 1},
		{"doctor unknown flag", []string{"doctor", "--definitely-invalid", "--json"}, 1},
		{"doctor extra path", []string{"doctor", ".", "extra", "--json"}, 1},
		{"doctor bad image", []string{"doctor", "--image", "https://example.com/app", "--json"}, 1},
		{"doctor empty credential", []string{"doctor", "--image", "example.com/app", "--registry-user", "user", "--registry-password-stdin", "--json"}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			osStdin = strings.NewReader("")
			stdout, stderr, restore := swapIO(t)
			defer restore()
			code := run(tc.args)
			var problem struct {
				api.Problem
				ExitCode int `json:"exit_code"`
			}
			// Unmarshal the entire stream to reject prose and duplicate objects.
			if err := json.Unmarshal([]byte(stderr()), &problem); err != nil {
				t.Fatalf("stderr must contain one JSON object: %v; %q", err, stderr())
			}
			if code != tc.want || problem.ExitCode != code || problem.Code == "" || problem.Detail == "" || stdout.Len() != 0 {
				t.Fatalf("exit=%d want=%d problem=%+v stdout=%q", code, tc.want, problem, stdout.String())
			}
		})
	}
	if _, err := os.Stat(dest); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid template created a destination: %v", err)
	}
}

func TestDoctorRejectsInvalidSourceDirectories(t *testing.T) {
	setupConnectionProfiles(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "server.js")
	if err := os.WriteFile(file, []byte("app.listen(3000)"), 0o600); err != nil {
		t.Fatal(err)
	}
	unreadable := filepath.Join(dir, "unreadable")
	if err := os.Mkdir(unreadable, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(unreadable, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(unreadable, 0o700) })
	for _, path := range []string{filepath.Join(dir, "missing"), file, unreadable} {
		t.Run(filepath.Base(path), func(t *testing.T) {
			if path == unreadable {
				if _, err := os.ReadDir(path); err == nil {
					t.Skip("this user can read mode-000 directories")
				}
			}
			stdout, stderr, restore := swapIO(t)
			defer restore()
			code := run([]string{"doctor", path, "--strict", "--json"})
			var problem api.Problem
			if err := json.Unmarshal([]byte(stderr()), &problem); err != nil {
				t.Fatalf("invalid error JSON: %v; %q", err, stderr())
			}
			if code != 1 || stdout.Len() != 0 || problem.Title != "Could not read source directory" {
				t.Fatalf("exit=%d stdout=%q problem=%+v", code, stdout.String(), problem)
			}
		})
	}
}

func TestDoctorStrictFlagBeforeOrAfterSource(t *testing.T) {
	setupConnectionProfiles(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "server.js"), []byte("app.listen(3000, '0.0.0.0');\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		args []string
		want int
	}{
		{[]string{"doctor", "--json", dir}, 0},
		{[]string{"doctor", "--json", "--strict", dir}, 1},
		{[]string{"doctor", dir, "--strict", "--json"}, 1},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			stdout, stderr, restore := swapIO(t)
			defer restore()
			code := run(tc.args)
			var report doctorReport
			if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
				t.Fatalf("invalid report: %v; stdout=%q stderr=%q", err, stdout.String(), stderr())
			}
			if code != tc.want || !report.HasWarnings() || report.HasErrors() || report.Path != dir || stderr() != "" {
				t.Fatalf("exit=%d report=%+v stderr=%q", code, report, stderr())
			}
		})
	}
}

func TestErrorRedactionDoesNotPrintCredentialDiagnostics(t *testing.T) {
	setupConnectionProfiles(t)
	setFakeKeyring(t, withGetErr(errors.New("no D-Bus session")))
	token := testAPIKey('a')
	writeLegacyToken(t, token)
	_, stderr, restore := swapIO(t)
	defer restore()
	if got := redactErrorText("credential " + token); got != "credential [REDACTED]" {
		t.Fatalf("redaction=%q", got)
	}
	if stderr() != "" {
		t.Fatalf("redaction emitted diagnostics: %q", stderr())
	}
}
