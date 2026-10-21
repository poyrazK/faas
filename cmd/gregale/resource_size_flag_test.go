package main_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// --size is the RAM/CPU size; --profile after the command still works as
// its deprecated spelling.
func TestAppScaleAcceptsSizeAndDeprecatedProfile(t *testing.T) {
	for _, tc := range []struct {
		args    []string
		want    string
		wantErr string
	}{
		{args: []string{"--size", "small"}, want: "small"},
		{args: []string{"--size=medium"}, want: "medium"},
		{args: []string{"--profile", "large"}, want: "large"},
		{args: []string{"--size", "small", "--profile", "small"}, wantErr: "use --size or --profile, not both"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			var patched *api.UpdateAppRequest
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPatch || r.URL.Path != "/v1/apps/size-app" {
					http.NotFound(w, r)
					return
				}
				body, _ := io.ReadAll(r.Body)
				patched = &api.UpdateAppRequest{}
				if err := json.Unmarshal(body, patched); err != nil {
					t.Errorf("decode PATCH body: %v", err)
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(api.AppResponse{Slug: "size-app"})
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			t.Setenv("FAAS_TOKEN", "fp_live_size")
			_, stderr, restore := main.SwapIO(t)
			code := main.CmdAppScale("size-app", tc.args)
			restore()
			if tc.wantErr != "" {
				if code == 0 || patched != nil || !strings.Contains(stderr(), tc.wantErr) {
					t.Fatalf("exit=%d patched=%v stderr=%q, want %q before any request", code, patched != nil, stderr(), tc.wantErr)
				}
				return
			}
			if code != 0 {
				t.Fatalf("exit = %d: %s", code, stderr())
			}
			if patched == nil || patched.ResourceProfile == nil || *patched.ResourceProfile != tc.want {
				t.Fatalf("resource_profile = %+v, want %q", patched, tc.want)
			}
		})
	}
}

// deploy --preview is the clearer spelling of deploy --plan (`gregale plan`
// changes the billing plan); both run the same local preview.
func TestDeployPreviewMatchesPlan(t *testing.T) {
	repo := main.InitZeroConfigRepo(t)
	for name, body := range map[string]string{
		"package.json": `{"scripts":{"start":"node server.js"}}`,
		"server.js":    "require('http').createServer((q, s) => s.end('ok')).listen(process.env.PORT || 3000)\n",
	} {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-q", "-m", "simple app"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	main.WithCwd(t, repo)
	t.Setenv("FAAS_TOKEN", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", os.Getenv("XDG_CONFIG_HOME"))

	deploy := func(args ...string) (int, string, string) {
		stdout, stderr, restore := main.SwapIO(t)
		defer restore()
		code := main.CmdDeployTarball(args)
		return code, stdout.String(), stderr()
	}
	planCode, plan, planErr := deploy("--plan", "--name", "demo", "--profile", "small")
	previewCode, preview, previewErr := deploy("--preview", "--name", "demo", "--size", "small")
	if planCode != 0 || previewCode != 0 {
		t.Fatalf("plan exit=%d %s; preview exit=%d %s", planCode, planErr, previewCode, previewErr)
	}
	if plan != preview || !strings.Contains(preview, "small") {
		t.Fatalf("--preview output differs from --plan:\n%s\n---\n%s", preview, plan)
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--preview", "--dry-run", "--name", "demo"}, "--preview cannot be combined"},
		{[]string{"--plan", "--dry-run", "--name", "demo"}, "--plan cannot be combined"},
		{[]string{"--preview", "--name", "demo", "--size", "huge"}, "Invalid --size"},
		{[]string{"--preview", "--name", "demo", "--profile", "huge"}, "Invalid --profile"},
		{[]string{"--preview", "--name", "demo", "--size", "small", "--profile", "large"}, "use --size or --profile, not both"},
	} {
		if code, _, stderr := deploy(tc.args...); code == 0 || !strings.Contains(stderr, tc.want) {
			t.Errorf("%v: exit=%d stderr=%q, want %q", tc.args, code, stderr, tc.want)
		}
	}
}
