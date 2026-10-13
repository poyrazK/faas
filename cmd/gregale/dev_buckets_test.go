package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

func TestCmdDevBucketsDispatchAndSetup(t *testing.T) {
	for _, tc := range []struct {
		name                                 string
		setup                                bool
		flags                                []string
		wantName, wantPrefix, wantPermission string
	}{
		{"manifest", false, nil, "archive", "FILES", "read"},
		{"flags replace manifest", false, []string{"--bucket", "assets"}, "assets", "GREGALE_S3_ASSETS", "read_write"},
		{"setup preserves custom manifest", true, nil, "archive", "FILES", "read"},
		{"setup forwards flags", true, []string{"--bucket", "assets"}, "assets", "GREGALE_S3_ASSETS", "read_write"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, data := range map[string]string{"package.json": `{"scripts":{"start":"node server.js"}}`, "server.js": "console.log('fixture')", "gregale.yaml": "dev:\n  buckets:\n    - name: archive\n      prefix: FILES\n      permission: read\n      triggers:\n        - id: incoming\n          prefix: uploads/\n"} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			t.Chdir(dir)
			var request api.UpsertDevSessionRequest
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodPut || r.URL.Path != "/v1/dev/sessions/files-app" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				// Stop before build/deploy: this test qualifies request dispatch.
				api.WriteProblem(w, api.NewProblem(503, "object_storage_unavailable", "Fixture", "Stop before deploying"))
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			t.Setenv("FAAS_TOKEN", testAPIKey('a'))
			t.Setenv("FAAS_DEVELOPER_ID", strings.Repeat("a", 32))
			_, restoreOut := captureStdout(t)
			defer restoreOut()
			_, restoreErr := captureStderr(t)
			defer restoreErr()
			args := append([]string{"--name", "files-app", "--once", "--no-logs"}, tc.flags...)
			if tc.setup {
				args = append([]string{"setup", "--start"}, args...)
			}
			if code := cmdDev(args); code != 3 {
				t.Fatalf("fixture dispatch exit = %d", code)
			}
			if len(tc.flags) == 0 && (request.Buckets[0].Triggers == nil || len(*request.Buckets[0].Triggers) != 1 || (*request.Buckets[0].Triggers)[0].ID != "incoming") {
				t.Fatal("manifest triggers not forwarded", request.Buckets)
			}
			if calls != 1 || len(request.Buckets) != 1 {
				t.Fatalf("dispatch calls=%d buckets=%+v", calls, request.Buckets)
			}
			bucket := request.Buckets[0]
			if bucket.Name != tc.wantName || bucket.Prefix != tc.wantPrefix || bucket.Permission != tc.wantPermission {
				t.Fatalf("bucket = %+v", bucket)
			}
		})
	}
}

func TestCmdDevSetupBucketPlanDoesNotProvision(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"scripts":{"start":"node server.js"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	t.Setenv("FAAS_TOKEN", testAPIKey('a'))
	out, restoreOut := captureStdout(t)
	defer restoreOut()
	_, restoreErr := captureStderr(t)
	defer restoreErr()
	previous := jsonOutput
	jsonOutput = true
	defer func() { jsonOutput = previous }()
	if code := cmdDevSetup([]string{"--bucket", "assets"}); code != 0 {
		t.Fatalf("setup = %d: %s", code, out.String())
	}
	var receipt devSetupReceipt
	if err := json.Unmarshal(out.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	if len(receipt.Buckets) != 1 || receipt.Buckets[0].Prefix != "GREGALE_S3_ASSETS" || !strings.Contains(strings.Join(receipt.StartCommand, " "), "--bucket assets") {
		t.Fatalf("plan = %+v", receipt)
	}
}

func TestDevBucketsManifestAndFlagPrecedence(t *testing.T) {
	manifest := &gregalemanifest.Manifest{Dev: &gregalemanifest.DevConfig{Buckets: []api.DevBucketRequest{{Name: "archive", Prefix: "FILES", Permission: "read", Region: "us-east-1"}}}}
	fromManifest, err := resolveDevBuckets(manifest, nil, false)
	if err != nil || fromManifest[0].Prefix != "FILES" || fromManifest[0].Permission != "read" {
		t.Fatalf("manifest = %+v, %v", fromManifest, err)
	}
	fromFlags, err := resolveDevBuckets(manifest, devBucketNames{"assets", "uploads"}, true)
	if err != nil || len(fromFlags) != 2 || fromFlags[0].Name != "assets" || fromFlags[0].Permission != "read_write" {
		t.Fatalf("flags = %+v, %v", fromFlags, err)
	}
	request := (devSourceConfig{}).sessionRequest("workspace", false, "", 0, nil, fromFlags...)
	if !reflect.DeepEqual(request.Buckets, fromFlags) {
		t.Fatalf("request = %+v", request)
	}
	if _, err = resolveDevBuckets(nil, devBucketNames{"assets", "assets"}, true); err == nil {
		t.Fatal("duplicate accepted")
	}
	var names devBucketNames
	if err = names.Set("../assets"); err == nil {
		t.Fatal("invalid name accepted")
	}
	args := (devAllOptions{buckets: devBucketNames{"assets", "uploads"}}).childArgs(devAllApp{SourceDir: "/source", Project: "app"}, true)
	if !strings.Contains(strings.Join(args, " "), "--bucket assets --bucket uploads") {
		t.Fatalf("child args = %v", args)
	}
}

func TestDevBucketStopFailsBeforeAuthentication(t *testing.T) {
	_, restore := captureStderr(t)
	defer restore()
	if got := cmdDev([]string{"--stop", "--bucket", "assets"}); got != 1 {
		t.Fatalf("exit = %d", got)
	}
}

func TestCmdDevBucketStopIgnoresInvalidManifest(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gregale.yaml"), []byte("dev:\n  buckets:\n    - name: ../invalid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodDelete || r.URL.Path != "/v1/dev/sessions/files-app" {
			t.Errorf("unexpected cleanup request %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", testAPIKey('a'))
	t.Setenv("FAAS_DEVELOPER_ID", strings.Repeat("a", 32))
	_, restoreOut := captureStdout(t)
	defer restoreOut()
	_, restoreErr := captureStderr(t)
	defer restoreErr()
	if code := cmdDev([]string{"--stop", "--name", "files-app"}); code != 0 || calls != 1 {
		t.Fatalf("cleanup exit=%d calls=%d", code, calls)
	}
}

func TestDevBucketsReloadManifestBeforeSourceSync(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gregale.yaml")
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("dev:\n  buckets:\n    - name: assets\n")
	first, err := resolveDevBucketsForSync(dir, nil, false)
	if err != nil || len(first) != 1 || first[0].Name != "assets" {
		t.Fatalf("first = %+v %v", first, err)
	}
	write("dev:\n  buckets:\n    - name: archive\n      prefix: FILES\n")
	updated, err := resolveDevBucketsForSync(dir, nil, false)
	if err != nil || len(updated) != 1 || updated[0].Prefix != "FILES" {
		t.Fatalf("updated = %+v %v", updated, err)
	}
	overridden, err := resolveDevBucketsForSync(dir, devBucketNames{"assets"}, true)
	if err != nil || overridden[0].Name != "assets" {
		t.Fatalf("override = %+v %v", overridden, err)
	}
	write("dev:\n  buckets:\n    - name: ../invalid\n")
	if _, err := resolveDevBucketsForSync(dir, nil, false); err == nil {
		t.Fatal("invalid changed manifest accepted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	omitted, err := resolveDevBucketsForSync(dir, nil, false)
	if err != nil || len(omitted) != 0 {
		t.Fatalf("omitted = %+v %v", omitted, err)
	}
}
