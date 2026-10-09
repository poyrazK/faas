package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestProjectsListJSONUsesAccountEndpointAndNDJSON(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `[{"id":"0123456789abcdef0123456789abcdef","slug":"shop","scan_source":"compose","workload_count":2,"created_at":"2026-09-15T00:00:00Z","updated_at":"2026-09-15T00:00:00Z"}]`, http.StatusOK)
	previousOut := osStdout
	var out bytes.Buffer
	osStdout = &out
	jsonOutput = true
	t.Cleanup(func() { osStdout = previousOut; jsonOutput = false })

	if code := cmdProjectsList(nil); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if f.sawMethod != http.MethodGet || f.sawPath != "/v1/projects" {
		t.Fatalf("route = %s %s", f.sawMethod, f.sawPath)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("NDJSON lines = %d, want 1; output=%q", len(lines), out.String())
	}
	var got api.ProjectSummaryResponse
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil || got.Slug != "shop" {
		t.Fatalf("NDJSON project = %+v, err=%v", got, err)
	}
}

func TestProjectsEnvironmentPromotionPreviewUsesTargetRoute(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `{"project_slug":"shop","from_environment":"staging","to_environment":"production","to_environment_protected":true,"approval_required":true,"can_promote":true,"config_diff":{"project_slug":"shop","from_environment":"staging","to_environment":"production","from_version":1,"to_version":1,"from_hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","to_hash":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","changes":[]},"changes":[],"promotion_hash":"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","promotion_token":"token"}`, http.StatusOK)
	if code := cmdProjectsEnvironmentPromotionPreview([]string{"shop", "--from", "staging", "--to", "production"}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if f.sawMethod != http.MethodGet || f.sawPath != "/v1/projects/shop/environments/production/promotion-preview" {
		t.Fatalf("route = %s %s", f.sawMethod, f.sawPath)
	}
	if f.sawQuery != "from=staging" {
		t.Fatalf("default promotion preview query = %q, want from=staging", f.sawQuery)
	}
}

func TestProjectsEnvironmentPromotionPreviewCanIncludeConfigSync(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `{"project_slug":"shop","from_environment":"staging","to_environment":"production","sync_config":true,"to_environment_protected":false,"approval_required":false,"can_promote":true,"config_diff":{"project_slug":"shop","from_environment":"staging","to_environment":"production","from_version":1,"to_version":1,"from_hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","to_hash":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","changes":[{"key":"REGION","kind":"changed","before":"us","after":"eu"}]},"changes":[],"promotion_hash":"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","promotion_token":"token"}`, http.StatusOK)
	previousOut := osStdout
	var out bytes.Buffer
	osStdout = &out
	t.Cleanup(func() { osStdout = previousOut })

	if code := cmdProjectsEnvironmentPromotionPreview([]string{"shop", "--from", "staging", "--to", "production", "--sync-config"}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if f.sawMethod != http.MethodGet || f.sawPath != "/v1/projects/shop/environments/production/promotion-preview" ||
		f.sawQuery != "from=staging&sync_config=true" {
		t.Fatalf("route = %s %s?%s", f.sawMethod, f.sawPath, f.sawQuery)
	}
	if !strings.Contains(out.String(), "Non-secret configuration to copy:") ||
		!strings.Contains(out.String(), "REGION") || !strings.Contains(out.String(), "after=\"eu\"") {
		t.Fatalf("preview did not show requested config changes: %s", out.String())
	}
}

func TestProjectsEnvironmentPromotionPreviewShowsReleaseGraphSnapshot(t *testing.T) {
	resetJSONOut(t)
	authedFakeAPI(t, `{"project_slug":"shop","from_environment":"staging","to_environment":"production","to_environment_protected":false,"approval_required":false,"can_promote":false,"blocking_reasons":["source environment has an active release set"],"config_diff":{"project_slug":"shop","from_environment":"staging","to_environment":"production","from_version":1,"to_version":1,"from_hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","to_hash":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","changes":[]},"changes":[],"from_release_set":{"id":"release-123","active":true,"ttl_seconds":1800,"created_at":"2026-09-27T00:00:00Z","members":[{"app_id":"app-123","deployment_id":"dep-456"}]},"promotion_hash":"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","promotion_token":"token"}`, http.StatusOK)
	previousOut := osStdout
	var out bytes.Buffer
	osStdout = &out
	t.Cleanup(func() { osStdout = previousOut })

	if code := cmdProjectsEnvironmentPromotionPreview([]string{"shop", "--from", "staging", "--to", "production"}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	for _, want := range []string{"source release set: release-123 (1 workloads)", "app-123 -> dep-456"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("preview output missing %q: %s", want, out.String())
		}
	}
}

func TestProjectsEnvironmentReleasesUsesEnvironmentRoute(t *testing.T) {
	resetJSONOut(t)
	const environmentURL = "https://env-stable.gregale.dev"
	f := authedFakeAPI(t, `{"project_slug":"shop","environment":"staging","workloads":[{"workload_slug":"api","workload_name":"api","status":"live","url":"https://env-stable.gregale.dev","deployment_id":"dep-1","build_id":"build-1","commit_sha":"abc123"}]}`, http.StatusOK)
	previousOut := osStdout
	var out bytes.Buffer
	osStdout = &out
	t.Cleanup(func() { osStdout = previousOut })
	if code := cmdProjectsEnvironmentReleases([]string{"shop", "staging"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if f.sawMethod != http.MethodGet || f.sawPath != "/v1/projects/shop/environments/staging/releases" {
		t.Fatalf("route = %s %s", f.sawMethod, f.sawPath)
	}
	if !strings.Contains(out.String(), environmentURL) {
		t.Fatalf("release output did not show stable URL: %q", out.String())
	}
}

func TestProjectsEnvironmentCreateSendsCloneSource(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `{"id":"env-1","project_id":"project-1","slug":"staging","protected":false,"created_at":"2026-09-22T00:00:00Z","updated_at":"2026-09-22T00:00:00Z","cloned_from":"production","clone":{"configuration_copied":true,"variables_copied":2,"secrets_copied":1,"secret_references_copied":3,"workloads_copied":1,"shared_resources":["domains","policies","routes"]}}`, http.StatusCreated)
	previousOut := osStdout
	var out bytes.Buffer
	osStdout = &out
	t.Cleanup(func() { osStdout = previousOut })
	if code := cmdProjectsEnvironmentCreate([]string{"shop", "staging", "--from", "production"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if f.sawMethod != http.MethodPost || f.sawPath != "/v1/projects/shop/environments" || !strings.Contains(string(f.sawBody), `"from_environment":"production"`) {
		t.Fatalf("request = %s %s body=%s", f.sawMethod, f.sawPath, f.sawBody)
	}
	if !strings.Contains(out.String(), "secrets=1 secret references=3") {
		t.Fatalf("clone output omitted reference count: %q", out.String())
	}
}

func TestProjectsEnvironmentHistoryUsesFilters(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `{"items":[{"promotion_id":"prom-1","project_slug":"shop","from_environment":"staging","to_environment":"production","status":"succeeded","created_at":"2026-09-17T00:00:00Z","updated_at":"2026-09-17T00:00:00Z"}],"next_before":"next-cursor"}`, http.StatusOK)
	if code := cmdProjectsEnvironmentHistory([]string{"shop", "production", "--from", "staging", "--status", "succeeded", "--limit", "10", "--before", "cursor"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if f.sawMethod != http.MethodGet || f.sawPath != "/v1/projects/shop/environments/production/promotions" || f.sawQuery != "before=cursor&from=staging&limit=10&status=succeeded" {
		t.Fatalf("route = %s %s", f.sawMethod, f.sawPath)
	}
}

func TestProjectsEnvironmentHistoryShowsConfigSync(t *testing.T) {
	resetJSONOut(t)
	authedFakeAPI(t, `{"items":[{"promotion_id":"prom-1","project_slug":"shop","from_environment":"staging","to_environment":"production","sync_config":true,"status":"succeeded","created_at":"2026-09-27T00:00:00Z","updated_at":"2026-09-27T00:00:00Z"}]}`, http.StatusOK)
	previousOut := osStdout
	var out bytes.Buffer
	osStdout = &out
	t.Cleanup(func() { osStdout = previousOut })

	if code := cmdProjectsEnvironmentHistory([]string{"shop", "production"}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(out.String(), "CONFIG") || !strings.Contains(out.String(), "synced") {
		t.Fatalf("history did not report opt-in config sync: %s", out.String())
	}
}

func TestProjectsEnvironmentConfigAndDiffUseEnvironmentRoutes(t *testing.T) {
	t.Run("config", func(t *testing.T) {
		resetJSONOut(t)
		f := authedFakeAPI(t, `{"project_slug":"shop","environment":"staging","version":2,"config_hash":"hash","values":{"MODE":"staging"}}`, http.StatusOK)
		if code := cmdProjectsEnvironmentConfig([]string{"shop", "staging"}); code != 0 {
			t.Fatalf("exit = %d, want 0", code)
		}
		if f.sawMethod != http.MethodGet || f.sawPath != "/v1/projects/shop/environments/staging/config" {
			t.Fatalf("route = %s %s", f.sawMethod, f.sawPath)
		}
	})

	t.Run("diff", func(t *testing.T) {
		resetJSONOut(t)
		f := authedFakeAPI(t, `{"project_slug":"shop","from_environment":"staging","to_environment":"production","configuration":{"project_slug":"shop","from_environment":"staging","to_environment":"production","from_version":2,"to_version":3,"from_hash":"from","to_hash":"to","changes":[{"key":"MODE","kind":"changed","before":"staging","after":"production"}]},"workloads":[{"workload_slug":"api","workload_name":"api","release":{"kind":"changed","before":{"workload_slug":"api","workload_name":"api","status":"live","deployment_id":"old"},"after":{"workload_slug":"api","workload_name":"api","status":"live","deployment_id":"new"}},"variables":[],"secrets":[],"bindings":[]}],"shared_resources":[],"generated_at":"2026-09-22T00:00:00Z"}`, http.StatusOK)
		if code := cmdProjectsEnvironmentConfigDiff([]string{"shop", "--from", "staging", "--to", "production"}); code != 0 {
			t.Fatalf("exit = %d, want 0", code)
		}
		if f.sawMethod != http.MethodGet || f.sawPath != "/v1/projects/shop/environments/production/diff" || f.sawQuery != "from=staging" {
			t.Fatalf("route = %s %s", f.sawMethod, f.sawPath)
		}
	})
}

func TestProjectsEnvironmentConfigSetPreviewsAndWrites(t *testing.T) {
	resetJSONOut(t)
	_, currentHash, err := api.NormalizeProjectEnvironmentConfig([]byte(`{"MODE":"staging"}`))
	if err != nil {
		t.Fatal(err)
	}
	nextValues, nextHash, err := api.NormalizeProjectEnvironmentConfig([]byte(`{"MODE":"production","REGION":"eu"}`))
	if err != nil {
		t.Fatal(err)
	}
	var methods []string
	var putBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		if r.Method == http.MethodPut {
			putBody, _ = io.ReadAll(r.Body)
			_, _ = w.Write([]byte(`{"project_slug":"shop","environment":"staging","version":2,"config_hash":"` + nextHash + `","values":{"MODE":"production","REGION":"eu"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"project_slug":"shop","environment":"staging","version":1,"config_hash":"` + currentHash + `","values":{"MODE":"staging"}}`))
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	oldStdin, oldStdout := osStdin, osStdout
	t.Cleanup(func() { osStdin, osStdout = oldStdin, oldStdout })

	var preview bytes.Buffer
	osStdin = strings.NewReader(string(nextValues))
	osStdout = &preview
	if code := cmdProjectsEnvironmentConfig([]string{"set", "shop", "staging", "--stdin", "--dry-run"}); code != 0 {
		t.Fatalf("dry-run exit = %d", code)
	}
	if len(methods) != 1 || methods[0] != http.MethodGet {
		t.Fatalf("dry-run methods = %v, want one GET", methods)
	}
	if !strings.Contains(preview.String(), nextHash) {
		t.Fatalf("preview = %q, missing next hash", preview.String())
	}

	var applied bytes.Buffer
	osStdin = strings.NewReader(string(nextValues))
	osStdout = &applied
	if code := cmdProjectsEnvironmentConfig([]string{"set", "shop", "staging", "--stdin", "--yes", "--if-hash", currentHash}); code != 0 {
		t.Fatalf("apply exit = %d", code)
	}
	if len(methods) != 3 || methods[1] != http.MethodGet || methods[2] != http.MethodPut {
		t.Fatalf("apply methods = %v, want GET, GET, PUT", methods)
	}
	if !strings.Contains(string(putBody), `"values":{"MODE":"production","REGION":"eu"}`) {
		t.Fatalf("PUT body = %s", putBody)
	}
}

func TestProjectsEnvironmentConfigSetRejectsStaleHash(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `{"project_slug":"shop","environment":"staging","version":1,"config_hash":"current","values":{"MODE":"staging"}}`, http.StatusOK)
	oldStdin := osStdin
	t.Cleanup(func() { osStdin = oldStdin })
	osStdin = strings.NewReader(`{"MODE":"production"}`)
	if code := cmdProjectsEnvironmentConfig([]string{"set", "shop", "staging", "--stdin", "--yes", "--if-hash", "stale"}); code != 1 {
		t.Fatalf("stale hash exit = %d, want 1", code)
	}
	if f.sawMethod != http.MethodGet {
		t.Fatalf("stale hash route = %s %s, want GET only", f.sawMethod, f.sawPath)
	}
}

func TestProjectsUpdateCarriesExplicitFields(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `{"id":"0123456789abcdef0123456789abcdef","slug":"shop","production_branch":"release","scan_source":"compose","workload_count":0,"created_at":"2026-09-15T00:00:00Z","updated_at":"2026-09-15T00:00:00Z","workloads":[],"exclusions":[]}`, http.StatusOK)
	if code := cmdProjectsUpdate([]string{"shop", "--branch", "release"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if f.sawMethod != http.MethodPatch || f.sawPath != "/v1/projects/shop" {
		t.Fatalf("route = %s %s", f.sawMethod, f.sawPath)
	}
	var body map[string]any
	if err := json.Unmarshal(f.sawBody, &body); err != nil {
		t.Fatal(err)
	}
	if body["production_branch"] != "release" {
		t.Fatalf("body = %s", f.sawBody)
	}
	if _, present := body["repo_full_name"]; present {
		t.Fatalf("unset repo leaked into patch: %s", f.sawBody)
	}
}

func TestProjectsRemoveRequiresAnExplicitMode(t *testing.T) {
	resetJSONOut(t)
	code, output := runWithStderr(t, func() int { return cmdProjectsRemove([]string{"shop"}) })
	if code != 1 || !strings.Contains(output, "--dry-run | --yes") {
		t.Fatalf("exit=%d stderr=%q", code, output)
	}
}

func TestProjectsRemoveConfirmedPreviewsThenDeletes(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `{"project":{"id":"0123456789abcdef0123456789abcdef","slug":"shop","scan_source":"compose","workload_count":1,"created_at":"2026-09-15T00:00:00Z","updated_at":"2026-09-15T00:00:00Z"},"workloads":[{"slug":"api","workload_name":"api","status":"active"}],"domain_count":1,"env_count":2,"cron_count":3}`, http.StatusOK)
	if code := cmdProjectsRemove([]string{"shop", "--yes"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if f.sawMethod != http.MethodDelete || f.sawPath != "/v1/projects/shop" {
		t.Fatalf("final route = %s %s", f.sawMethod, f.sawPath)
	}
}
