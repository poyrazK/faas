package main_test

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// fakeTemplateAPI answers the template routes and the build app's deploy,
// keeping what the CLI sent.
type fakeTemplateAPI struct {
	mu       sync.Mutex
	put      api.SandboxTemplateRequest
	putPath  string
	fields   map[string]string
	archive  map[string]string
	polls    int
	finalErr string
}

func (f *fakeTemplateAPI) serve(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		tpl := api.SandboxTemplate{Name: "web-dev", App: "sbt-0123456789ab", ReadyTimeoutS: 60}
		switch {
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/v1/sandbox-templates/"):
			f.putPath = r.URL.Path
			_ = json.NewDecoder(r.Body).Decode(&f.put)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(tpl)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/sbt-0123456789ab/deployments":
			f.readUpload(t, r)
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "dep-1", Status: "pending"})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/sandbox-templates/web-dev":
			f.polls++
			tpl.Build = &api.SandboxTemplateBuild{ID: "dep-1", Status: "building"}
			if f.polls >= 2 {
				if f.finalErr != "" {
					tpl.Build.Status, tpl.Build.Error = "failed", f.finalErr
				} else {
					tpl.Build.Status, tpl.Ready = "live", true
				}
			}
			_ = json.NewEncoder(w).Encode(tpl)
		default:
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (f *fakeTemplateAPI) readUpload(t *testing.T, r *http.Request) {
	t.Helper()
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		t.Errorf("multipart: %v", err)
		return
	}
	f.fields = map[string]string{}
	for k, v := range r.MultipartForm.Value {
		f.fields[k] = v[0]
	}
	src, _, err := r.FormFile("source")
	if err != nil {
		t.Errorf("source: %v", err)
		return
	}
	gz, err := gzip.NewReader(src)
	if err != nil {
		t.Errorf("gzip: %v", err)
		return
	}
	f.archive = map[string]string{}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return
		}
		if err != nil {
			t.Errorf("tar: %v", err)
			return
		}
		data, _ := io.ReadAll(tr)
		f.archive[hdr.Name] = string(data)
	}
}

func templateDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func runTemplateCLI(t *testing.T, srv *httptest.Server, args ...string) (int, string) {
	t.Helper()
	main.ResetJSONOut(t)
	old := *main.SandboxTemplatePoll
	*main.SandboxTemplatePoll = time.Millisecond
	t.Cleanup(func() { *main.SandboxTemplatePoll = old })
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	out, restore := main.CaptureStdout(t)
	code := main.Run(args)
	restore()
	return code, out.String()
}

// `gregale sandbox template build` reads e2b.toml, saves the template's
// commands, deploys the directory to its build app with e2b.Dockerfile as
// the Dockerfile, and waits for the build.
func TestSandboxTemplateBuildFromAnE2BDirectory(t *testing.T) {
	dir := templateDir(t, map[string]string{
		"e2b.toml":       "team_id = \"ignored\"\ntemplate_name = \"web-dev\"\ndockerfile = \"e2b.Dockerfile\"\nstart_cmd = \"npm run dev\"\nready_cmd = \"curl -fs localhost:3000\"\ncpu_count = 2\n",
		"e2b.Dockerfile": "FROM node:22\nCOPY app.js /app/\n",
		"Dockerfile":     "FROM scratch\n",
		"app.js":         "console.log(1)\n",
	})
	f := &fakeTemplateAPI{}
	srv := f.serve(t)
	code, out := runTemplateCLI(t, srv, "sandbox", "template", "build", dir, "--ready-timeout", "90")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if f.putPath != "/v1/sandbox-templates/web-dev" || f.put != (api.SandboxTemplateRequest{StartCmd: "npm run dev", ReadyCmd: "curl -fs localhost:3000", ReadyTimeoutS: 90}) {
		t.Fatalf("PUT %s %+v", f.putPath, f.put)
	}
	if f.fields["dockerfile"] != "true" || f.fields["slug"] != "sbt-0123456789ab" {
		t.Fatalf("deploy fields %v", f.fields)
	}
	if f.archive["Dockerfile"] != "FROM node:22\nCOPY app.js /app/\n" || f.archive["app.js"] == "" {
		t.Fatalf("archive %v", f.archive)
	}
	if !strings.Contains(out, "web-dev is ready") {
		t.Fatalf("output:\n%s", out)
	}
}

// A failed build exits 1 with the build's error.
func TestSandboxTemplateBuildFailure(t *testing.T) {
	dir := templateDir(t, map[string]string{"Dockerfile": "FROM alpine\n"})
	f := &fakeTemplateAPI{finalErr: "step 2 exited 1"}
	srv := f.serve(t)
	if code, out := runTemplateCLI(t, srv, "sandbox", "template", "build", dir, "--name", "web-dev"); code != 1 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if f.archive["Dockerfile"] != "FROM alpine\n" {
		t.Fatalf("archive %v", f.archive)
	}
}

// Bad names and Dockerfiles outside the directory fail before any request.
func TestSandboxTemplateBuildRejectsBadInput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
	}))
	t.Cleanup(srv.Close)
	dir := templateDir(t, map[string]string{"Dockerfile": "FROM alpine\n"})
	for _, args := range [][]string{
		{"sandbox", "template", "build", dir, "--name", "Web_Dev"},
		{"sandbox", "template", "build", dir, "--name", "web", "--dockerfile", "../Dockerfile"},
		{"sandbox", "template", "build", t.TempDir(), "--name", "web"},
	} {
		if code, out := runTemplateCLI(t, srv, args...); code != 1 {
			t.Errorf("%v: exit %d:\n%s", args, code, out)
		}
	}
}

// `--json` prints the template as the API returned it.
func TestSandboxTemplateGetJSON(t *testing.T) {
	srv := (&fakeTemplateAPI{}).serve(t)
	main.ResetJSONOut(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	out, restore := main.CaptureStdout(t)
	code := main.Run([]string{"sandbox-templates", "get", "web-dev", "--json"})
	restore()
	var got api.SandboxTemplate
	if code != 0 || json.Unmarshal(out.Bytes(), &got) != nil || got.Name != "web-dev" || got.Build == nil {
		t.Fatalf("exit %d, output:\n%s", code, out)
	}
}
