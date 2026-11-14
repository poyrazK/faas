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

func TestPipelineOperationsUsesRevisionAndRejectsMalformedFilesLocally(t *testing.T) {
	main.ResetJSONOut(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "token")
	id, calls := uuid.NewString(), 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/data/pipelines/"+id+"/operations" {
			t.Error("unexpected route", r.Method, r.URL.Path)
		}
		var req api.DataPipelineOperationsRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		if req.ExpectedRevision != 7 || req.Action != "run_snapshot" || req.Config != nil {
			t.Error("changed intent", req)
		}
		calls++
		main.WriteJSONTest(w, api.DataPipeline{ID: id, Revision: 8})
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	if code := main.Run([]string{"--json", "pipeline", "operations", id, "--revision", "7", "--run-snapshot"}); code != 0 || calls != 1 {
		t.Fatal(code, calls)
	}
	file := filepath.Join(t.TempDir(), "work.json")
	for _, body := range []string{`null`, `{"version":1,"unknown":true}`, `{} {}`} {
		if err := os.WriteFile(file, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if code := main.Run([]string{"pipeline", "operations", id, "--revision", "7", "--file", file}); code == 0 {
			t.Fatal("malformed file accepted")
		}
	}
	if calls != 1 {
		t.Fatal("invalid local request reached API", calls)
	}
}
