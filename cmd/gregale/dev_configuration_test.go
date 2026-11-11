package main_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func configurationProject(t *testing.T, refs string) string {
	t.Helper()
	return writeDevLocalProject(t, map[string]string{
		"package.json": `{"scripts":{"start":"node server.js"}}`,
		"server.js":    "require('http').createServer((q,s)=>s.end('ok')).listen(process.env.PORT);\n" + refs,
	})
}

func TestDevConfigurationPlansActualInputs(t *testing.T) {
	for _, tc := range []struct {
		name, refs, file, override           string
		postgres                             bool
		buckets                              []api.DevBucketRequest
		provided, planned, empty, unverified []string
	}{
		{name: "selected nonempty file", refs: "console.log(process.env.APP_TEST_ALPHA);", file: "APP_TEST_ALPHA=selected-secret\n", provided: []string{"APP_TEST_ALPHA"}},
		{name: "empty selected value", refs: "console.log(process.env.APP_TEST_ALPHA);", file: "APP_TEST_ALPHA=\n", empty: []string{"APP_TEST_ALPHA"}},
		{name: "unselected dotenv is not delivery", refs: "console.log(process.env.APP_TEST_ALPHA);", unverified: []string{"APP_TEST_ALPHA"}},
		{name: "planned postgres", refs: "console.log(process.env.DATABASE_URL);", postgres: true, planned: []string{"DATABASE_URL"}},
		{name: "custom bucket prefix", refs: "console.log(process.env.FILES_ACCESS_KEY_ID);", buckets: []api.DevBucketRequest{{Name: "assets", Prefix: "FILES"}}, planned: []string{"FILES_ACCESS_KEY_ID"}},
		{name: "optional and platform references", refs: "console.log(process.env.APP_TEST_OPTIONAL || 'fallback', process.env.PORT, process.env.GREGALE_SERVICE_URL);"},
		{name: "service override supplies key", refs: "console.log(process.env.DATABASE_URL);", override: "DATABASE_URL=postgresql://example.test/dev\n", provided: []string{"DATABASE_URL"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := configurationProject(t, tc.refs)
			main.WriteFile(t, root, ".env", "APP_TEST_ALPHA=unselected-secret\n")
			file, override := "", ""
			if tc.file != "" {
				main.WriteFile(t, root, ".env.dev", tc.file)
				file = filepath.Join(root, ".env.dev")
			}
			if tc.override != "" {
				main.WriteFile(t, root, "services.env", tc.override)
				override = filepath.Join(root, "services.env")
			}
			input, err := main.LoadDevConfigurationForTest(root, file, override, tc.postgres, tc.buckets)
			if err != nil {
				t.Fatal(err)
			}
			r := main.EvaluateDevConfigurationForTest(input, nil)
			for _, pair := range []struct{ got, want []string }{{r.Provided, tc.provided}, {r.Planned, tc.planned}, {r.Empty, tc.empty}, {r.Unverified, tc.unverified}} {
				if strings.Join(pair.got, ",") != strings.Join(pair.want, ",") {
					t.Fatalf("coverage=%+v", r)
				}
			}
			encoded, _ := json.Marshal(r)
			if strings.Contains(string(encoded), "selected-secret") || strings.Contains(string(encoded), "example.test") {
				t.Fatal("configuration report exposed a value")
			}
		})
	}
}

func TestDevConfigurationVerifiesOnlyDeveloperScope(t *testing.T) {
	for _, tc := range []struct {
		name, secretScope           string
		secret, env, sealed, denied bool
		wantMissing, wantExisting   bool
	}{
		{name: "existing secret", secret: true, secretScope: "default", wantExisting: true},
		{name: "existing env name", env: true, wantExisting: true},
		{name: "known absent", wantMissing: true},
		{name: "other scope cannot satisfy", secret: true, secretScope: "production", wantMissing: true},
		{name: "secret requirement needs sealed key", env: true, sealed: true, wantMissing: true},
		{name: "read failure remains unverified", denied: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := configurationProject(t, "console.log(process.env.APP_TEST_ALPHA);")
			if tc.sealed {
				main.WriteFile(t, root, ".gregale/required-secrets.json", `["APP_TEST_ALPHA"]`)
			}
			input, err := main.LoadDevConfigurationForTest(root, "", "", false, nil)
			if err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != "GET" || !strings.HasPrefix(r.URL.Path, "/v1/apps/dev-config-app/") || r.URL.Query().Get("scope") != "default" {
					t.Errorf("wrong metadata target: %s %s", r.Method, r.URL)
				}
				if tc.denied {
					api.WriteProblem(w, api.NewProblem(403, api.CodeForbidden, "Denied", "sensitive-server-detail"))
					return
				}
				if strings.HasSuffix(r.URL.Path, "/secrets") {
					metadata := api.AppSecretListResponse{}
					if tc.secret {
						metadata.Secrets = []api.AppSecretResponse{{Key: "APP_TEST_ALPHA", Scope: tc.secretScope}}
					}
					_ = json.NewEncoder(w).Encode(metadata)
				} else {
					metadata := api.AppEnvListResponse{}
					if tc.env {
						metadata.Env = []api.AppEnvResponse{{Key: "APP_TEST_ALPHA"}}
					}
					_ = json.NewEncoder(w).Encode(metadata)
				}
			}))
			defer server.Close()
			r, err := main.VerifyDevConfigurationForTest(context.Background(), api.NewClient(server.URL, "token"), api.DevSessionResponse{App: api.AppResponse{Slug: "dev-config-app"}}, input)
			if (err != nil) != tc.denied || (len(r.Missing) > 0) != tc.wantMissing || (len(r.ExistingValuesUnverified) > 0) != tc.wantExisting {
				t.Fatalf("coverage=%+v err=%v", r, err)
			}
			if tc.denied && (len(r.Unverified) != 1 || strings.Contains(err.Error(), "sensitive-server-detail") || calls.Load() != 1) {
				t.Fatalf("failed verification was not preserved safely: %+v %v", r, err)
			}
		})
	}
}

func TestDevConfigurationUsesAcceptedResourcePlan(t *testing.T) {
	root := configurationProject(t, "console.log(process.env.DATABASE_URL);")
	input, err := main.LoadDevConfigurationForTest(root, "", "", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("accepted binding plan triggered an unnecessary metadata request: %s", r.URL)
		w.WriteHeader(500)
	}))
	defer server.Close()
	session := api.DevSessionResponse{App: api.AppResponse{Slug: "dev-config-app"}, Postgres: &api.DevPostgresResponse{BindingID: "binding", BindingState: "pending", EnvironmentKey: "DATABASE_URL"}}
	r, err := main.VerifyDevConfigurationForTest(context.Background(), api.NewClient(server.URL, "token"), session, input)
	if err != nil || !reflect.DeepEqual(r.Planned, []string{"DATABASE_URL"}) || len(r.Provided) != 0 {
		t.Fatalf("coverage=%+v err=%v", r, err)
	}
}

func TestDevSetupConfigurationIsOfflineAndHonest(t *testing.T) {
	for _, tc := range []struct {
		name, refs, file, status string
		args                     []string
		wantCode                 int
		ready                    bool
	}{
		{name: "selected file", refs: "console.log(process.env.APP_TEST_ALPHA);", file: "APP_TEST_ALPHA=selected-secret\n", args: []string{"--env-file", ".env.dev"}, status: "provided", ready: true},
		{name: "unselected dotenv", refs: "console.log(process.env.APP_TEST_ALPHA);", status: "unverified", wantCode: 1},
		{name: "empty value", refs: "console.log(process.env.APP_TEST_ALPHA);", file: "APP_TEST_ALPHA=\n", args: []string{"--env-file", ".env.dev"}, status: "needs_attention", wantCode: 1},
		{name: "postgres plan", refs: "console.log(process.env.DATABASE_URL);", args: []string{"--postgres"}, status: "planned", ready: true},
		{name: "optional reference", refs: "console.log(process.env.APP_TEST_OPTIONAL || 'fallback');", status: "no_requirements", ready: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			main.SetupCLIRegression(t)
			root := configurationProject(t, tc.refs)
			main.WriteFile(t, root, ".env", "APP_TEST_ALPHA=unselected-secret\n")
			if tc.file != "" {
				main.WriteFile(t, root, ".env.dev", tc.file)
			}
			t.Chdir(root)
			t.Setenv("FAAS_TOKEN", main.TestAPIKey('a'))
			t.Setenv("FAAS_CAPABILITY_FLAGS_RELEASE_ALL", "1")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Errorf("offline setup called %s", r.URL)
				w.WriteHeader(500)
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			out, restore := main.CaptureStdout(t)
			defer restore()
			args := append([]string{"dev", "setup", "--name", "config-app", "--json"}, tc.args...)
			if code := main.Run(args); code != tc.wantCode {
				t.Fatalf("exit=%d output=%s", code, out.String())
			}
			var receipt struct {
				Ready         bool                  `json:"ready"`
				Configuration main.DevConfiguration `json:"configuration"`
			}
			if err := json.Unmarshal(out.Bytes(), &receipt); err != nil {
				t.Fatal(err)
			}
			if receipt.Ready != tc.ready || receipt.Configuration.Status != tc.status {
				t.Fatalf("receipt=%+v", receipt)
			}
			if strings.Contains(out.String(), "selected-secret") {
				t.Fatal("setup exposed a value")
			}
		})
	}
}

func TestCmdDevMissingConfigurationStopsBeforeUpload(t *testing.T) {
	main.SetupCLIRegression(t)
	root := configurationProject(t, "console.log(process.env.APP_TEST_ALPHA);")
	main.WriteFile(t, root, ".env", "APP_TEST_ALPHA=unselected-secret\n")
	t.Chdir(root)
	oldRoot := *main.TestOnlyProjectContextRoot
	*main.TestOnlyProjectContextRoot = root
	defer func() { *main.TestOnlyProjectContextRoot = oldRoot }()
	if _, err := main.SaveProjectContext(root, main.LocalProjectContext{Version: 1, Project: "production-project", App: "production-app", Environment: "production"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAAS_TOKEN", main.TestAPIKey('a'))
	t.Setenv("FAAS_DEVELOPER_ID", strings.Repeat("a", 32))
	t.Setenv("FAAS_CAPABILITY_FLAGS_RELEASE_ALL", "1")
	var upserts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if scope := r.URL.Query().Get("scope"); scope != "" && scope != "default" {
			t.Errorf("developer check used linked production scope: %s", r.URL)
		}
		switch {
		case r.Method == "PUT" && r.URL.Path == "/v1/dev/sessions/config-app":
			upserts.Add(1)
			_ = json.NewEncoder(w).Encode(api.DevSessionResponse{App: api.AppResponse{Slug: "dev-config-app"}, ExpiresAt: time.Now().Add(time.Hour)})
		case r.Method == "GET" && r.URL.Path == "/v1/apps/dev-config-app/secrets":
			_ = json.NewEncoder(w).Encode(api.AppSecretListResponse{})
		case r.Method == "GET" && r.URL.Path == "/v1/apps/dev-config-app/env":
			_ = json.NewEncoder(w).Encode(api.AppEnvListResponse{})
		default:
			t.Errorf("configuration failure reached an upload/build or wrong target: %s %s", r.Method, r.URL)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	out, restore := main.CaptureStdout(t)
	defer restore()
	stderr, restoreErr := main.CaptureStderr(t)
	defer restoreErr()
	if code := main.Run([]string{"dev", "--path", root, "--name", "config-app", "--once", "--no-logs", "--json"}); code == 0 {
		t.Fatal("missing required key allowed a sync")
	}
	if upserts.Load() != 1 {
		t.Fatalf("upserts=%d stderr=%s", upserts.Load(), stderr.String())
	}
	if bytes.Count(out.Bytes(), []byte("\n")) != 1 {
		t.Fatalf("configuration event is not one NDJSON line: %s", out.String())
	}
	var event struct {
		Type    string   `json:"type"`
		Missing []string `json:"missing"`
	}
	if err := json.Unmarshal(out.Bytes(), &event); err != nil {
		t.Fatal(err, out.String())
	}
	if event.Type != "developer_configuration" || !reflect.DeepEqual(event.Missing, []string{"APP_TEST_ALPHA"}) {
		t.Fatalf("event=%+v", event)
	}
}

func TestDevSetupStartCanVerifyUnknownKeys(t *testing.T) {
	main.SetupCLIRegression(t)
	root := configurationProject(t, "console.log(process.env.APP_TEST_ALPHA);")
	t.Setenv("FAAS_TOKEN", main.TestAPIKey('a'))
	t.Setenv("FAAS_DEVELOPER_ID", strings.Repeat("a", 32))
	t.Setenv("FAAS_CAPABILITY_FLAGS_RELEASE_ALL", "1")
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "PUT" || r.URL.Path != "/v1/dev/sessions/config-app" {
			t.Errorf("unexpected start request: %s %s", r.Method, r.URL)
		}
		// Stop before provisioning; this regression pins setup's handoff.
		api.WriteProblem(w, api.NewProblem(503, "fixture_stop", "Fixture", "Stop at the session request"))
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	out, restore := main.CaptureStdout(t)
	defer restore()
	_, restoreErr := main.CaptureStderr(t)
	defer restoreErr()
	code := main.Run([]string{"dev", "setup", "--path", root, "--name", "config-app", "--start", "--once", "--no-logs", "--json"})
	if code == 0 || calls.Load() != 1 {
		t.Fatalf("setup did not hand unknown keys to start for verification: code=%d calls=%d", code, calls.Load())
	}
	var receipt struct {
		Ready         bool                  `json:"ready"`
		Configuration main.DevConfiguration `json:"configuration"`
	}
	if err := json.Unmarshal(out.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.Ready || receipt.Configuration.Status != "unverified" {
		t.Fatalf("offline receipt overstated readiness: %+v", receipt)
	}
}

func TestDevConfigurationSyncUsesValidatedSnapshot(t *testing.T) {
	main.ResetJSONOut(t)
	*main.JsonOutput = true
	out, restore := main.CaptureStdout(t)
	defer restore()
	root := configurationProject(t, "console.log(process.env.APP_TEST_ALPHA, process.env.APP_TEST_BETA);")
	path := filepath.Join(root, ".env.dev")
	main.WriteFile(t, root, ".env.dev", "APP_TEST_ALPHA=validated-secret\n")
	uploaded := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/secrets"):
			if err := os.WriteFile(path, []byte("APP_TEST_ALPHA=changed-secret\n"), 0o600); err != nil {
				t.Error(err)
			}
			_ = json.NewEncoder(w).Encode(api.AppSecretListResponse{Secrets: []api.AppSecretResponse{{Key: "APP_TEST_BETA"}}})
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/env"):
			_ = json.NewEncoder(w).Encode(api.AppEnvListResponse{})
		case r.Method == "PUT" && strings.HasSuffix(r.URL.Path, "/secrets/APP_TEST_ALPHA"):
			var request api.PutAppSecretRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
			uploaded <- request.Value
			w.WriteHeader(204)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	client := api.NewClient(server.URL, "token")
	input, code := main.CheckDevConfigurationForTest(context.Background(), client, api.DevSessionResponse{App: api.AppResponse{Slug: "dev-config-app"}}, root, path, "")
	if code != 0 {
		t.Fatalf("preflight=%d output=%s", code, out.String())
	}
	if err := main.SyncValidatedDevConfigurationForTest(context.Background(), client, "dev-config-app", path, input); err != nil {
		t.Fatal(err)
	}
	if <-uploaded != "validated-secret" {
		t.Fatal("sync uploaded a different file version than the one validated")
	}
	if strings.Contains(out.String(), "validated-secret") || strings.Contains(out.String(), "changed-secret") {
		t.Fatal("preflight exposed values")
	}
}
