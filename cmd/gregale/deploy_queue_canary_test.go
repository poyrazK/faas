package main_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/deploydiff"
)

func queueCanaryArchive(t *testing.T, prefix, manifest string) string {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tr := tar.NewWriter(gz)
	for name, body := range map[string]string{"gregale.yaml": manifest, "handler.js": "exports.handler=async()=>({statusCode:200})"} {
		if err := tr.WriteHeader(&tar.Header{Name: prefix + name, Mode: 0600, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tr.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tr.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "source.tgz")
	if err := os.WriteFile(archive, buf.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return archive
}

const queueCanaryManifest = "function:\n  runtime: node22\n  handler: handler.handler\nqueue_bindings:\n  - name: default\n    queue_name: default\n    mode: push\n    workload_class: http\n"

func TestDeploymentQueueCanarySelectedArchive(t *testing.T) {
	for _, prefix := range []string{"", "wrapper/", "apps/worker/"} {
		archive := queueCanaryArchive(t, prefix, queueCanaryManifest)
		root := ""
		if prefix == "apps/worker/" {
			root = "apps/worker"
		}
		names, err := main.DeploymentQueueCanaryBindings(archive, root, []string{"default"})
		if err != nil || !reflect.DeepEqual(names, []string{"default"}) {
			t.Fatalf("prefix=%q names=%v err=%v", prefix, names, err)
		}
		for _, invalid := range [][]string{{"missing"}, {"default", "default"}, {"Bad"}, {""}} {
			if _, err := main.DeploymentQueueCanaryBindings(archive, root, invalid); err == nil {
				t.Fatalf("accepted %v", invalid)
			}
		}
	}
	for _, manifest := range []string{
		queueCanaryManifest + "    enabled: false\n",
		strings.ReplaceAll(queueCanaryManifest, "mode: push", "mode: pull"),
		strings.ReplaceAll(queueCanaryManifest, "workload_class: http", "workload_class: worker"),
	} {
		archive := queueCanaryArchive(t, "", manifest)
		if _, err := main.DeploymentQueueCanaryBindings(archive, "", []string{"default"}); err == nil {
			t.Fatalf("accepted %q", manifest)
		}
	}
	if names, err := main.DeploymentQueueCanaryBindings("missing", "", nil); err != nil || names != nil {
		t.Fatalf("omitted opt-in=%v %v", names, err)
	}
}

func TestRevisionPinTTLCLI(t *testing.T) {
	for _, value := range []string{"10m", "0", "168h", "-1s", "1ns", "169h"} {
		t.Run(value, func(t *testing.T) {
			main.ResetJSONOut(t)
			calls := 0
			var got api.UpdateAppRequest
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodPatch {
					t.Errorf("unexpected %s", r.Method)
				}
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
					t.Error(err)
				}
				main.WriteJSONTest(w, api.AppResponse{Slug: "hello"})
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_test_x")
			duration, _ := time.ParseDuration(value)
			seconds, err := main.CliRevisionPinTTLSeconds(duration)
			code := main.Run([]string{"app", "hello", "--revision-pin-ttl", value, "--json"})
			if err != nil {
				if code == 0 || calls != 0 {
					t.Fatalf("invalid TTL code=%d calls=%d", code, calls)
				}
				return
			}
			if code != 0 || calls != 1 || got.RevisionPinTTLSeconds == nil || *got.RevisionPinTTLSeconds != seconds {
				t.Fatalf("code=%d calls=%d patch=%+v", code, calls, got)
			}
		})
	}
}

func TestQueueCanaryPreviewAndIdempotency(t *testing.T) {
	var d deploydiff.Diff
	main.AppendDeployPreviewAnnotationIntent(&d, main.DiffCLIOptions{QueueCanaryBindings: []string{"default"}})
	if len(d.Changes) != 1 || d.Changes[0].Field != "deployment.queue_canary_bindings" {
		t.Fatalf("preview=%+v", d)
	}
	base := main.DeployIdempotencyIntent{Slug: "hello", SourceSHA256: "same"}
	old, _ := main.DeployIdempotencyKey("", base)
	base.OverrideEnv = map[string]string{api.QueueCanaryCapabilityEnv: `["default"]`}
	next, _ := main.DeployIdempotencyKey("", base)
	if old == next {
		t.Fatal("capability missing from retry identity")
	}
}

func TestQueueCanaryCLIRedeployment(t *testing.T) {
	main.WithCwd(t, t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	main.WithResetJSONOutput(t, false)
	archive := queueCanaryArchive(t, "wrapper/", queueCanaryManifest)
	var uploaded []map[string]string
	mutations := 0
	stub := main.NewZeroConfigStubServer(t, func(w http.ResponseWriter, r *http.Request, _ *main.ZeroConfigStubServer) {
		if r.Method != http.MethodGet {
			mutations++
		}
		switch {
		case r.URL.Path == "/v1/apps" && r.Method == http.MethodPost:
			main.WriteJSONTest(w, api.AppResponse{ID: "a1", Slug: "demo"})
		case r.URL.Path == "/v1/apps/demo/queue-bindings" && r.Method == http.MethodGet:
			main.WriteJSONTest(w, []api.QueueBindingResponse{})
		case r.URL.Path == "/v1/apps/demo/queue-bindings" && r.Method == http.MethodPost:
			main.WriteJSONTest(w, api.QueueBindingResponse{ID: "q1", Name: "default"})
		case r.URL.Path == "/v1/apps/demo/deployments" && r.Method == http.MethodPost:
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Error(err)
			}
			if r.MultipartForm != nil {
				defer func() { _ = r.MultipartForm.RemoveAll() }()
			}
			var overrides map[string]string
			if raw := r.FormValue("override_env"); raw != "" {
				if err := json.Unmarshal([]byte(raw), &overrides); err != nil {
					t.Error(err)
				}
			}
			uploaded = append(uploaded, overrides)
			w.WriteHeader(http.StatusAccepted)
			main.WriteJSONTest(w, api.DeploymentResponse{ID: "d1", AppID: "a1", Status: "pending"})
		default:
			http.NotFound(w, r)
		}
	})
	t.Setenv("FAAS_API", (*stub.Srv()).URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	base := []string{"--name", "demo", "--tarball", archive, "--function", "--runtime", "node22", "--handler", "handler.handler", "--no-wait"}
	var preview bytes.Buffer
	oldOut := (*main.OsStdout)
	(*main.OsStdout) = &preview
	previewCode := main.CmdDeployTarball(append(append([]string{}, base...), "--queue-canary-binding", "default", "--dry-run", "--json"))
	(*main.OsStdout) = oldOut
	if previewCode != 0 || mutations != 0 || !strings.Contains(preview.String(), "deployment.queue_canary_bindings") {
		t.Fatalf("preview code=%d mutations=%d output=%s", previewCode, mutations, preview.String())
	}
	invalid := append(append([]string{}, base...), "--queue-canary-binding", "missing")
	if code := main.CmdDeployTarball(invalid); code == 0 || mutations != 0 {
		t.Fatalf("invalid: code=%d mutations=%d", code, mutations)
	}
	for _, opted := range []bool{true, false} {
		args := append([]string{}, base...)
		if opted {
			args = append(args, "--queue-canary-binding", "default")
		}
		if code := main.CmdDeployTarball(args); code != 0 {
			t.Fatalf("opted=%v code=%d", opted, code)
		}
	}
	if len(uploaded) != 2 || uploaded[0][api.QueueCanaryCapabilityEnv] != `["default"]` || len(uploaded[1]) != 0 {
		t.Fatalf("redeployment overrides=%v", uploaded)
	}
}

func TestQueueCanaryUnsupportedModesBeforeNetwork(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; http.Error(w, "unexpected", 500) }))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	for _, flags := range [][]string{{"--project"}, {"--image", "example.invalid/app:latest"}, {"--repo", "owner/repo"}, {"--plan"}, {"--create-only"}, {"--github"}, {"--no-triggers"}} {
		args := append([]string{"--name", "demo", "--queue-canary-binding", "default"}, flags...)
		if code := main.CmdDeployTarball(args); code == 0 {
			t.Errorf("accepted %v", flags)
		}
	}
	if calls != 0 {
		t.Fatalf("made %d requests", calls)
	}
}
