package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnvCreateUsesLinkedProject(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `{"id":"env-1","project_id":"project-1","slug":"staging","protected":false,"created_at":"2026-09-22T00:00:00Z","updated_at":"2026-09-22T00:00:00Z","cloned_from":"production","clone":{"configuration_copied":true,"variables_copied":0,"secrets_copied":0,"workloads_copied":1,"shared_resources":["domains","policies","routes"]}}`, http.StatusCreated)
	root := t.TempDir()
	if _, err := saveProjectContext(root, localProjectContext{Version: projectContextVersion, Project: "shop"}); err != nil {
		t.Fatal(err)
	}
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(filepath.Join(root, ".gregale")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })
	if code := envCreate([]string{"staging", "--from", "production"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if f.sawPath != "/v1/projects/shop/environments" || !strings.Contains(string(f.sawBody), `"from_environment":"production"`) {
		t.Fatalf("request path=%s body=%s", f.sawPath, f.sawBody)
	}
}

func TestEnvCreatePlanIsReadOnly(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `{"project_slug":"shop","from_environment":"production","to_environment":"staging","share_resources":false,"can_clone":true,"can_promote":true,"workload_count":1,"actions":[{"resource":"customer_secrets","action":"copy_sealed","count":1,"reason":"values are never shown"}],"blocking_reasons":[],"warnings":[]}`, http.StatusOK)
	root := t.TempDir()
	if _, err := saveProjectContext(root, localProjectContext{Version: projectContextVersion, Project: "shop"}); err != nil {
		t.Fatal(err)
	}
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(filepath.Join(root, ".gregale")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })
	if code := envCreate([]string{"--plan", "staging", "--from", "production"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if f.sawMethod != http.MethodGet || f.sawPath != "/v1/projects/shop/environments/production/clone-preview" || f.sawQuery != "to=staging" {
		t.Fatalf("request method=%s path=%s?%s, want read-only clone preview", f.sawMethod, f.sawPath, f.sawQuery)
	}
}
