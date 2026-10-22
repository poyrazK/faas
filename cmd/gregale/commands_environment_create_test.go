package main_test

import (
	"github.com/onebox-faas/faas/cmd/gregale"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnvCreateUsesLinkedProject(t *testing.T) {
	main.ResetJSONOut(t)
	f := main.AuthedFakeAPI(t, `{"id":"env-1","project_id":"project-1","slug":"staging","protected":false,"created_at":"2026-09-22T00:00:00Z","updated_at":"2026-09-22T00:00:00Z","cloned_from":"production","clone":{"configuration_copied":true,"variables_copied":0,"secrets_copied":0,"workloads_copied":1,"shared_resources":["domains","policies","routes"]}}`, http.StatusCreated)
	root := t.TempDir()
	if _, err := main.SaveProjectContext(root, main.LocalProjectContext{Version: main.ProjectContextVersion, Project: "shop"}); err != nil {
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
	if code := main.EnvCreate([]string{"staging", "--from", "production"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if (*f.SawPath()) != "/v1/projects/shop/environments" || !strings.Contains(string((*f.SawBody())), `"from_environment":"production"`) {
		t.Fatalf("request path=%s body=%s", (*f.SawPath()), (*f.SawBody()))
	}
}
