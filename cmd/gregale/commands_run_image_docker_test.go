package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 1790
func TestRunImageRequestedByPositionalImage(t *testing.T) {
	cases := map[string]struct {
		args []string
		want bool
	}{
		"positional image":             {[]string{"alpine", "echo", "hi"}, true},
		"flags then positional":        {[]string{"-it", "-u", "app", "alpine", "sh"}, true},
		"disposable run flag wins":     {[]string{"--runtime", "node24", "--source", "x"}, false},
		"disposable run with a value":  {[]string{"--dir", "src", "--entrypoint", "main.js"}, false},
		"flags only":                   {[]string{"--help"}, false},
		"nothing":                      {nil, false},
		"positional after separator":   {[]string{"--", "alpine"}, false},
		"--image beats positional":     {[]string{"--image", "alpine", "echo"}, true},
		"disposable flag after --wait": {[]string{"--wait", "x"}, false},
	}
	for name, tc := range cases {
		if got := runImageRequested(tc.args); got != tc.want {
			t.Errorf("%s: runImageRequested(%q) = %v, want %v", name, tc.args, got, tc.want)
		}
	}
}

// adr: 1790
func TestParseImageRunArgsDockerFlags(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, "app.env")
	if err := os.WriteFile(envFile, []byte("# comment\n\n  FROM_FILE=one\r\nSHARED=file\nPASSED\nUNSET_LOCAL\nSPACED=a b \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	second := filepath.Join(dir, "second.env")
	if err := os.WriteFile(second, []byte("SHARED=second\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PASSED", "local-value")
	t.Setenv("FROM_FLAG", "local-flag")
	_ = os.Unsetenv("UNSET_LOCAL")
	_ = os.Unsetenv("ALSO_UNSET")
	strs := func(v ...string) *[]string { return &v }

	cases := map[string]struct {
		args          []string
		image         string
		cmd           *[]string
		user, workdir string
		env           map[string]string
	}{
		"positional image and command": {args: []string{"alpine", "echo", "hi"}, image: "alpine", cmd: strs("echo", "hi")},
		"short user and workdir": {args: []string{"-u", "app:staff", "-w", "/srv", "alpine", "pwd"},
			image: "alpine", cmd: strs("pwd"), user: "app:staff", workdir: "/srv"},
		"long user and workdir": {args: []string{"--user", "1000", "--workdir", "/data", "--image", "alpine"},
			image: "alpine", user: "1000", workdir: "/data"},
		"env files then flags": {args: []string{"--env-file", envFile, "--env-file", second, "-e", "SHARED=flag", "-e", "FROM_FLAG", "-e", "ALSO_UNSET", "alpine", "env"},
			image: "alpine", cmd: strs("env"),
			env: map[string]string{"FROM_FILE": "one", "SHARED": "flag", "PASSED": "local-value", "FROM_FLAG": "local-flag", "SPACED": "a b "}},
		"later env file wins": {args: []string{"--env-file", envFile, "--env-file", second, "alpine"}, image: "alpine",
			env: map[string]string{"FROM_FILE": "one", "SHARED": "second", "PASSED": "local-value", "SPACED": "a b "}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			opts, ok := parseImageRunArgs(tc.args)
			req := opts.request
			if !ok || req.Image != tc.image || !reflect.DeepEqual(req.Args, tc.cmd) || req.User != tc.user ||
				req.Workdir != tc.workdir || !reflect.DeepEqual(req.Env, tc.env) {
				t.Fatalf("parse(%q) = %+v, ok=%v", tc.args, req, ok)
			}
		})
	}
}

// adr: 1790
func TestParseImageRunArgsRejectsBadDockerFlags(t *testing.T) {
	dir := t.TempDir()
	spaced := filepath.Join(dir, "spaced.env")
	if err := os.WriteFile(spaced, []byte("BAD KEY=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	good := filepath.Join(dir, "good.env")
	if err := os.WriteFile(good, []byte("KEY=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(dir, "linked.env")
	if err := os.Symlink(good, linked); err != nil {
		t.Fatal(err)
	}
	cases := map[string][]string{
		"password stdin without username": {"--registry-password-stdin", "alpine"},
		"password stdin with interactive": {"-i", "--registry-username", "ci", "--registry-password-stdin", "alpine"},
		"env file key with whitespace":    {"--env-file", spaced, "alpine"},
		"missing env file":                {"--env-file", filepath.Join(dir, "missing.env"), "alpine"},
		"symlinked env file":              {"--env-file", linked, "alpine"},
		"env file is a directory":         {"--env-file", dir, "alpine"},
		"env flag without a name":         {"-e", "=1", "alpine"},
	}
	_, readErr, restore := swapIO(t)
	defer restore()
	for name, args := range cases {
		if _, ok := parseImageRunArgs(args); ok {
			t.Errorf("%s: parse(%q) accepted", name, args)
		}
	}
	if stderr := readErr(); !strings.Contains(stderr, "contains whitespace") || !strings.Contains(stderr, "stdin is the session") {
		t.Fatalf("stderr = %q", stderr)
	}
}

// adr: 1790
func TestImageRunRegistryAuthSources(t *testing.T) {
	cases := map[string]struct {
		opts    imageRunOptions
		stdin   string
		envPass string
		want    *api.ImageRunRegistryAuth
		wantErr string
	}{
		"no username": {opts: imageRunOptions{}},
		"stdin trims newline": {opts: imageRunOptions{registryUsername: "ci", passwordStdin: true}, stdin: "tok\r\n",
			want: &api.ImageRunRegistryAuth{Username: "ci", Password: "tok"}},
		"environment variable": {opts: imageRunOptions{registryUsername: "ci"}, envPass: "env-tok",
			want: &api.ImageRunRegistryAuth{Username: "ci", Password: "env-tok"}},
		"stdin beats environment": {opts: imageRunOptions{registryUsername: "ci", passwordStdin: true}, stdin: "stdin-tok", envPass: "env-tok",
			want: &api.ImageRunRegistryAuth{Username: "ci", Password: "stdin-tok"}},
		"no password source": {opts: imageRunOptions{registryUsername: "ci"}, wantErr: "needs a password"},
		"empty stdin":        {opts: imageRunOptions{registryUsername: "ci", passwordStdin: true}, wantErr: "needs a password"},
		"password too long": {opts: imageRunOptions{registryUsername: "ci", passwordStdin: true},
			stdin: strings.Repeat("p", api.MaxRegistryPasswordBytes+1), wantErr: "exceeds"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Setenv(imageRunEnvPasswordVar, tc.envPass)
			oldIn := osStdin
			osStdin = strings.NewReader(tc.stdin)
			defer func() { osStdin = oldIn }()
			got, err := imageRunRegistryAuth(tc.opts)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("auth = %+v, %v; want %+v", got, err, tc.want)
			}
		})
	}
}

// adr: 1790
func TestCmdRunImageSendsRegistryAuthOnlyOnFirstRequest(t *testing.T) {
	var mu sync.Mutex
	var auths []*api.ImageRunRegistryAuth
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "POST /v1/image-runs":
			var req api.CreateImageRunRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			mu.Lock()
			auths = append(auths, req.RegistryAuth)
			first := len(auths) == 1
			mu.Unlock()
			if req.User != "app" || req.Workdir != "/srv" {
				t.Errorf("user/workdir = %q %q", req.User, req.Workdir)
			}
			if first {
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(`{"status":"pulling","job":"ir-0123","image":"ghcr.io/acme/tool:1","image_status":"pending"}`))
				return
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"status":"queued","job":"ir-0123","image":"ghcr.io/acme/tool:1","image_status":"ready","run_id":"run-1"}`))
		case "GET /v1/jobs/ir-0123":
			_, _ = w.Write([]byte(`{"id":"job-1","name":"ir-0123","kind":"image_run","image_materialization_status":"ready"}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	t.Setenv(imageRunEnvPasswordVar, "s3cret")
	out, readErr, restore := swapIO(t)
	defer restore()
	if got := cmdRunImage([]string{"-d", "-u", "app", "-w", "/srv", "--registry-username", "ci", "ghcr.io/acme/tool:1"}); got != 0 {
		t.Fatalf("exit = %d stderr = %s", got, readErr())
	}
	if strings.Contains(out.String(), "s3cret") || strings.Contains(readErr(), "s3cret") {
		t.Fatal("the password was printed")
	}
	if len(auths) != 2 || auths[0] == nil || *auths[0] != (api.ImageRunRegistryAuth{Username: "ci", Password: "s3cret"}) || auths[1] != nil {
		t.Fatalf("registry auth per request = %+v; want it on the first request only", auths)
	}
}
