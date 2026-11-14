package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestPipelineEvolutionApprovalUsesOnlySavedIntent(t *testing.T) {
	main.ResetJSONOut(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test-token")
	id := uuid.NewString()
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/data/pipelines/"+id+"/evolution" {
			t.Error("unexpected route", r.Method, r.URL.Path)
			http.Error(w, "bad route", 400)
			return
		}
		var req api.DataPipelineEvolutionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		if req.Action != "approve" || req.ChangeID != "frozen-change" || req.ExpectedRevision != 7 || req.Columns != nil || req.AuthOrganizations != nil || req.Relationships != nil {
			t.Error("approval replaced intent", req)
		}
		calls++
		main.WriteJSONTest(w, api.DataPipeline{ID: id, Revision: 8})
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	if code := main.Run([]string{"--json", "pipeline", "evolve", id, "--approve", "frozen-change", "--revision", "7"}); code != 0 || calls != 1 {
		t.Fatal(code, calls)
	}
	file := filepath.Join(t.TempDir(), "evolution.json")
	for _, contents := range []string{`null`, `{"kind":"projection","unknown":true}`, `{"action":"approve","change_id":"other"}`, `{} {}`} {
		if err := os.WriteFile(file, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
		if code := main.Run([]string{"--json", "pipeline", "evolve", id, "--file", file, "--revision", "8"}); code == 0 {
			t.Fatal("invalid file accepted", contents)
		}
	}
	if calls != 1 {
		t.Fatal("invalid local intent reached server", calls)
	}
}
