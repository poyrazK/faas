// adr: 20261026195952
package mcpmanagement

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestHostedCLIRunsWithNothingOfTheHost(t *testing.T) {
	t.Setenv("HOST_ONLY_MARKER", "leaked")
	t.Setenv("FAAS_PROFILE", "host-profile")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	script := filepath.Join(t.TempDir(), "probe.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf 'ARGS %s\\n' \"$*\"\nprintf 'PWD %s\\n' \"$(pwd)\"\nenv\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	run := HostedCLIRunner(script, "https://api.gregale.test", "/uploads")("fp_grant_token")
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
	if env["FAAS_TOKEN"] != "fp_grant_token" || env["FAAS_API"] != "https://api.gregale.test" || env["GIT_CONFIG_GLOBAL"] != os.DevNull || env["GIT_CONFIG_NOSYSTEM"] != "1" || !strings.HasSuffix(env["GIT_CEILING_DIRECTORIES"], ":/uploads") {
		t.Fatalf("env: %v", env)
	}
	for _, leaked := range []string{"HOST_ONLY_MARKER", "FAAS_PROFILE", "XDG_STATE_HOME"} {
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

func TestHostedUploadDeployReusesCheckpointAfterApproval(t *testing.T) {
	f := newLocalFixture(t, false, "")
	store, err := newUploadStore(filepath.Join(t.TempDir(), "uploads"), "https://api.gregale.test/mcp")
	if err != nil {
		t.Fatal(err)
	}
	// Model the CLI's durable checkpoint and the server's approval of one
	// specific commit URL. A fresh state directory would request approval again.
	script := filepath.Join(t.TempDir(), "deploy.sh")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
set -eu
state="${XDG_STATE_HOME:-$HOME/.config}"
checkpoint="$state/gregale/uploads/checkpoint"
if [ -f "$checkpoint" ]; then
    read -r upload old_home < "$checkpoint"
    [ "$upload" = original-upload ] && [ "$HOME" != "$old_home" ] && [ ! -d "$old_home" ]
    printf '%s\n' "$upload"
    exit
fi
umask 077
mkdir -p "$(dirname "$checkpoint")"
printf 'original-upload %s\n' "$HOME" > "$checkpoint"
printf '%s\n' '{"status":428,"code":"approval_required","title":"Approval required","agent_approval":{"id":"approval-1","review_url":"https://gregale.test/approve","category":"deploy"}}' >&2
exit 1
`), 0o700); err != nil {
		t.Fatal(err)
	}
	run := HostedCLIRunner(script, "https://api.gregale.test", store.root)(testToken)
	cli := func(ctx context.Context, args []string, stdin []byte) (LocalCLIResult, error) {
		if slices.Contains(args, "--plan") {
			return f.cli(ctx, args, stdin)
		}
		out, err := run(ctx, args, stdin)
		if err != nil || out.ExitCode != 0 {
			return out, err
		}
		if strings.TrimSpace(string(out.Stdout)) != "original-upload" {
			t.Errorf("retry used a different upload: %q", out.Stdout)
		}
		return f.cli(ctx, args, stdin)
	}
	cs, ctx := f.connect(t, true, func(o *Options) {
		o.LocalCLI, o.uploads, o.grantID = cli, store, "grant-one"
		o.Environments = &EnvironmentConsent{DefaultWrite: true}
	})
	plan := callTool(t, ctx, cs, "gregale_plan_upload_deploy", map[string]any{
		"app": "alpha", "reason": "first deploy",
		"files": []inlineFile{{Path: "server.js", Content: "require('http')"}},
	}, false)
	args := map[string]any{"plan_id": planID(t, plan)}
	first := callTool(t, ctx, cs, "gregale_apply_upload_deploy", args, true)
	if toolCode(t, first) != "approval_required" || !strings.Contains(string(first["approval"]), "approval-1") {
		t.Fatalf("first apply = %v", first)
	}
	for range 2 {
		applied := callTool(t, ctx, cs, "gregale_apply_upload_deploy", args, false)
		if !strings.Contains(string(applied["deployment"]), "release-1") {
			t.Fatalf("apply = %v", applied)
		}
	}
	calls := f.deployCalls()
	if len(calls) != 1 {
		t.Fatalf("accepted deploy calls = %v", calls)
	}
	source := calls[0][slices.Index(calls[0], "--path")+1]
	stateDir := uploadPlanCLIStateDir(source)
	if _, err := os.Stat(filepath.Join(stateDir, "gregale", "uploads", "checkpoint")); err != nil {
		t.Fatalf("checkpoint missing: %v", err)
	}
	store.removeDir(source)
	if _, err := os.Stat(stateDir); !os.IsNotExist(err) {
		t.Fatalf("checkpoint state survived plan cleanup: %v", err)
	}
}
