package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/google/uuid"
	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestPipelineDiscoverySelectionCLI(t *testing.T) {
	main.ResetJSONOut(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test-token")
	id := uuid.NewString()
	var seen []api.RequestDataConnectionCheck
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/data/connections":
			main.WriteJSONTest(w, api.DataConnectionList{Items: []api.DataConnection{{ID: id, Revision: 2}}})
		case "POST /v1/data/connections/" + id + "/check":
			var request api.RequestDataConnectionCheck
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
			seen = append(seen, request)
			main.WriteJSONTest(w, api.DataConnectionCheck{ID: uuid.NewString(), ConnectionID: id, State: "passed", Fresh: true})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	for _, args := range [][]string{{"connection", "tables", id, "--search", "orders"}, {"connection", "check", id, "--tables", "zz_orders, customers"}} {
		if code := main.CmdPipeline(args); code != 0 {
			t.Fatal("discovery command failed", args, code)
		}
	}
	if len(seen) != 2 || seen[0].Search != "orders" || !slices.Equal(seen[1].Tables, []string{"zz_orders", "customers"}) || seen[1].ExpectedRevision != 2 {
		t.Fatal("selection lost", seen)
	}
	if code := main.CmdPipeline([]string{"connection", "check", id, "--tables", "orders", "--search", "orders"}); code == 0 || len(seen) != 2 {
		t.Fatal("conflicting options reached API")
	}
}
