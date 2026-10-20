package main_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/cmd/gregale/templates"
)

func TestDurableEntityScaffoldAndDoctorRequirePreparation(t *testing.T) {
	root := t.TempDir()
	if err := templates.Materialize("durable-entity-node", root); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"gregale.entities.json", "handlers.mjs", "server.mjs", "dev.mjs", "validator.mjs", ".gregaleignore", ".gitignore"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatalf("missing scaffold file %s: %v", name, err)
		}
	}
	check, present := main.DoctorCheckDurableEntities(root)
	if !present || check.Status != "error" || check.Code != "durable_entity_project_invalid" {
		t.Fatalf("unprepared scaffold should fail source checks: %+v", check)
	}
	if check, present := main.DoctorCheckDurableEntities(t.TempDir()); present {
		t.Fatalf("ordinary source received entity checks: %+v", check)
	}
}

func TestDurableEntityInitDoesNotDeployAnUnpreparedSDK(t *testing.T) {
	var stdout, stderr bytes.Buffer
	destination := filepath.Join(t.TempDir(), "feature")
	if code := main.RunCmdInit("durable-entity-node", destination, true, "example", &stdout, &stderr); code != 1 {
		t.Fatalf("unprepared --deploy accepted: %d", code)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("failed direct deployment should not create source: %v", err)
	}
}

func TestDurableEntityDeploymentCannotBypassPreparation(t *testing.T) {
	root := t.TempDir()
	if err := templates.Materialize("durable-entity-node", root); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_entity_source_guard")
	if code := main.Run([]string{"deploy", "--name", "entities", "--path", root, "--source", "worktree", "--no-doctor"}); code == 0 {
		t.Fatal("unprepared project deployed")
	}
	if requests.Load() != 0 {
		t.Fatalf("unprepared source made %d API requests", requests.Load())
	}
}
