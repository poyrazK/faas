package main_test

import (
	"encoding/json"
	"github.com/onebox-faas/faas/cmd/gregale"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestDevSetupReceiptIsReadyAndSecretSafe(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"scripts":{"start":"node server.js"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	envPath := filepath.Join(dir, ".env.dev")
	if err := os.WriteFile(envPath, []byte("API_TOKEN=do-not-render\nLOG_LEVEL=debug\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAAS_TOKEN", main.TestAPIKey('a'))

	config, err := main.ResolveDevSetupSource(dir)
	if err != nil {
		t.Fatal(err)
	}
	path, keys, err := main.ResolveSetupEnvFile(dir, envPath)
	if err != nil {
		t.Fatal(err)
	}
	receipt := main.BuildDevSetupReceipt(dir, dir, "demo", config, path, keys, "", 0, false, false, false, false, false, "", 0)
	if !receipt.Ready || !receipt.Authenticated {
		t.Fatalf("receipt readiness = ready:%t authenticated:%t, want both true", receipt.Ready, receipt.Authenticated)
	}
	if receipt.Class != "app" || receipt.Framework != "node" {
		t.Fatalf("detected shape = %q/%q, want app/node", receipt.Class, receipt.Framework)
	}
	if receipt.TTL != "24h" {
		t.Fatalf("receipt ttl = %q, want the 24h default", receipt.TTL)
	}
	if receipt.EnvKeyCount != 2 || len(receipt.DependencyFiles) != 1 || receipt.DependencyFiles[0] != "package.json" {
		t.Fatalf("env/dependencies = %d/%v, want 2/[package.json]", receipt.EnvKeyCount, receipt.DependencyFiles)
	}
	body, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "do-not-render") {
		t.Fatalf("setup receipt leaked an env value: %s", body)
	}
}

func TestDevSetupValidatesManifestBeforePlanning(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gregale.yaml"), []byte("hosting:\n  port: 70000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := main.ResolveDevSetupSource(dir); err == nil || !strings.Contains(err.Error(), "port 70000") {
		t.Fatalf("resolveDevSetupSource error = %v, want manifest port validation", err)
	}
}

func TestCmdDevSetupPrintsNextCommandWithoutRemoteMutation(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "requirements.txt"), []byte("flask==3.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	t.Setenv("FAAS_TOKEN", main.TestAPIKey('b'))
	stdout, restoreOut := main.CaptureStdout(t)
	defer restoreOut()
	_, restoreErr := main.CaptureStderr(t)
	defer restoreErr()

	if code := main.CmdDevSetup(nil); code != 0 {
		t.Fatalf("cmdDevSetup() = %d, want 0; stdout=%q", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "Developer setup ready") || !strings.Contains(stdout.String(), "gregale dev") {
		t.Fatalf("setup output missing readiness/next command: %q", stdout.String())
	}
}

func TestCmdDevSetupUsesManifestDevDefaults(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"scripts":{"start":"node server.js"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env.dev"), []byte("LOG_LEVEL=debug\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env.services.local"), []byte("REDIS_URL=redis://cache.example.test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gregale.yaml"), []byte(`dev:
  env_file: .env.dev
  service_override_file: .env.services.local
  postgres: true
  postgres_region: eu-central-1
`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	t.Setenv("FAAS_TOKEN", main.TestAPIKey('c'))
	stdout, restoreOut := main.CaptureStdout(t)
	defer restoreOut()
	_, restoreErr := main.CaptureStderr(t)
	defer restoreErr()

	if code := main.CmdDevSetup([]string{"--postgres-region", "us-east-1"}); code != 0 {
		t.Fatalf("cmdDevSetup() = %d, want 0; stdout=%q", code, stdout.String())
	}
	for _, want := range []string{".env.dev", ".env.services.local", "--service-override-file", "--postgres"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("setup output missing %q: %q", want, stdout.String())
		}
	}
}

func TestDevSetupRejectsStartOnlyFlags(t *testing.T) {
	if code := main.CmdDevSetup([]string{"--once"}); code != 1 {
		t.Fatalf("cmdDevSetup(--once) = %d, want 1", code)
	}
}

func TestCmdDevSetupTTLPrecedence(t *testing.T) {
	cases := []struct {
		name        string
		manifestTTL string
		args        []string
		wantCode    int
		wantOutput  string
	}{
		{name: "default lease", wantOutput: "lease: 24h"},
		{name: "manifest lease", manifestTTL: "72h", wantOutput: "--ttl 72h"},
		{name: "flag wins over manifest", manifestTTL: "72h", args: []string{"--ttl", "168h"}, wantOutput: "--ttl 168h"},
		{name: "invalid flag", args: []string{"--ttl", "10m"}, wantCode: 1},
		{name: "invalid manifest", manifestTTL: "soon", wantCode: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"scripts":{"start":"node server.js"}}`), 0o644); err != nil {
				t.Fatal(err)
			}
			if tc.manifestTTL != "" {
				if err := os.WriteFile(filepath.Join(dir, "gregale.yaml"), []byte("dev:\n  ttl: "+tc.manifestTTL+"\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			t.Chdir(dir)
			t.Setenv("FAAS_TOKEN", main.TestAPIKey('d'))
			stdout, restoreOut := main.CaptureStdout(t)
			defer restoreOut()
			_, restoreErr := main.CaptureStderr(t)
			defer restoreErr()

			if code := main.CmdDevSetup(tc.args); code != tc.wantCode {
				t.Fatalf("cmdDevSetup(%v) = %d, want %d; stdout=%q", tc.args, code, tc.wantCode, stdout.String())
			}
			if tc.wantOutput != "" && !strings.Contains(stdout.String(), tc.wantOutput) {
				t.Fatalf("setup output missing %q: %q", tc.wantOutput, stdout.String())
			}
		})
	}
}

func TestDevSetupRecoveryPreservesEffectiveOptions(t *testing.T) {
	for _, tc := range []struct {
		name, profile              string
		nested, authenticated, bad bool
	}{
		{name: "nested source", nested: true},
		{name: "external source and profile", profile: "staging"},
		{name: "source errors", profile: "staging", authenticated: true, bad: true},
		{name: "ready", profile: "staging", authenticated: true},
		{name: "explicit default", profile: "default"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			main.SetupConnectionProfiles(t)
			if tc.profile == "default" {
				if err := main.SaveCLIConfig(main.CliConfig{ActiveProfile: "staging", Profiles: map[string]main.ConnectionProfile{"staging": {APIBase: "https://staging.example"}}}); err != nil {
					t.Fatal(err)
				}
			}
			cwd := t.TempDir()
			t.Chdir(cwd)
			source := filepath.Join(t.TempDir(), "app $demo")
			if tc.nested {
				source = filepath.Join(cwd, "app $demo")
			}
			if err := os.MkdirAll(source, 0o700); err != nil {
				t.Fatal(err)
			}
			fixtures := map[string]string{
				"package.json":  `{}`,
				".env.dev":      "API_TOKEN=do-not-render\n",
				".env.services": "REDIS_URL=redis://cache.example.test\n",
			}
			if tc.bad {
				fixtures["server.js"] = "app.listen(8080, '127.0.0.1');\n"
			}
			for name, body := range fixtures {
				if err := os.WriteFile(filepath.Join(source, name), []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.authenticated {
				t.Setenv("FAAS_TOKEN", main.TestAPIKey('a'))
			}
			prefix := []string{"gregale"}
			if tc.profile != "" {
				prefix = append(prefix, "--profile", tc.profile)
			}
			args := append(slices.Clone(prefix[1:]), "--json", "dev", "setup", "--path", source, "--name", "demo",
				"--env-file", filepath.Join(source, ".env.dev"), "--service-override-file", filepath.Join(source, ".env.services"),
				"--postgres", "--postgres-region", "eu-central-1", "--ttl", "72h", "--bucket", "uploads", "--bucket", "cache")
			ready := tc.authenticated && !tc.bad
			if !ready {
				args = append(args, "--start", "--once", "--no-logs", "--open")
			}
			stdout, stderr, restore := main.SwapIO(t)
			defer restore()
			code := main.Run(args)
			var receipt main.DevSetupReceipt
			if err := json.Unmarshal(stdout.Bytes(), &receipt); err != nil {
				t.Fatalf("invalid receipt: %v; stdout=%q stderr=%q", err, stdout.String(), stderr())
			}
			if (code == 0) != ready || receipt.Ready != ready || stderr() != "" {
				t.Fatalf("exit=%d ready=%t want=%t stderr=%q", code, receipt.Ready, ready, stderr())
			}
			displayPath := source
			if tc.nested {
				displayPath = filepath.Base(source)
			}
			want := append(slices.Clone(prefix), "dev", "--path", displayPath, "--name", "demo",
				"--env-file", filepath.Join(displayPath, ".env.dev"), "--service-override-file", filepath.Join(displayPath, ".env.services"))
			if !ready {
				want = append(want, "--once", "--no-logs", "--open")
			}
			want = append(want, "--postgres", "--postgres-region", "eu-central-1", "--ttl", "72h", "--bucket", "uploads", "--bucket", "cache")
			if !slices.Equal(receipt.StartCommand, want) {
				t.Fatalf("start_command=%q want=%q", receipt.StartCommand, want)
			}
			if !tc.authenticated && receipt.Next[0] != strings.Join(prefix, " ")+" login" {
				t.Fatalf("login lost profile: %q", receipt.Next)
			}
			next := receipt.Next[len(receipt.Next)-1]
			command := strings.Join(prefix, " ") + " dev"
			if !ready {
				command += " setup --start"
			}
			for _, fragment := range []string{command, "--path '" + displayPath + "'", "--name demo", "--env-file '", "--service-override-file '", "--postgres --postgres-region eu-central-1", "--ttl 72h", "--bucket uploads --bucket cache"} {
				if !strings.Contains(next, fragment) {
					t.Errorf("next command missing %q: %s", fragment, next)
				}
			}
			if !ready && !strings.Contains(next, "--once --no-logs --open") {
				t.Errorf("next command lost start options: %s", next)
			}
			if strings.Contains(stdout.String(), "do-not-render") {
				t.Fatal("receipt leaked an environment value")
			}
		})
	}
}

func TestDevSetupRecoveryPreservesDisabledManifestDefaults(t *testing.T) {
	main.SetupConnectionProfiles(t)
	dir := t.TempDir()
	t.Chdir(dir)
	for name, body := range map[string]string{
		"package.json": `{}`,
		"gregale.yaml": "dev:\n  postgres: true\n  env_file: missing.env\n  service_override_file: missing.services\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	args := []string{"--json", "dev", "setup", "--path", ".", "--name", "demo", "--postgres=false", "--env-file", "", "--service-override-file", ""}
	for attempt := range 2 {
		stdout, stderr, restore := main.SwapIO(t)
		code := main.Run(args)
		output, errorOutput := stdout.String(), stderr()
		restore()
		var receipt main.DevSetupReceipt
		if err := json.Unmarshal([]byte(output), &receipt); err != nil || code != 1 || errorOutput != "" {
			t.Fatalf("attempt=%d exit=%d err=%v stdout=%q stderr=%q", attempt, code, err, output, errorOutput)
		}
		for _, flag := range []string{"--postgres=false", "--env-file", "--service-override-file", "--path"} {
			if !slices.Contains(receipt.StartCommand, flag) {
				t.Errorf("attempt=%d start command lost %s: %q", attempt, flag, receipt.StartCommand)
			}
		}
		// Reapply the advertised options to the unchanged manifest. Missing
		// empty overrides would try to open the deliberately absent files.
		args = append([]string{"--json", "dev", "setup"}, receipt.StartCommand[2:]...)
	}
}

func TestDevSetupShellCommandQuotesMetacharacters(t *testing.T) {
	for _, tc := range []struct{ arg, want string }{
		{"./app;echo", "'./app;echo'"},
		{"./app$USER", "'./app$USER'"},
		{"./app'one", "'./app'\"'\"'one'"},
	} {
		if got := main.DevSetupShellCommand([]string{"gregale", "dev", "--path", tc.arg}); got != "gregale dev --path "+tc.want {
			t.Errorf("command for %q = %q", tc.arg, got)
		}
	}
}
