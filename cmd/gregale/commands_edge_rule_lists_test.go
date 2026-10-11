package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// edgeRuleListServer records the last request body and answers every call
// with one list.
func edgeRuleListServer(t *testing.T, gotMethod, gotPath *string, gotBody *map[string]any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*gotMethod, *gotPath = r.Method, r.URL.Path
		*gotBody = nil
		_ = json.NewDecoder(r.Body).Decode(gotBody)
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		l := api.EdgeRuleListResponse{Name: "office", Kind: "ip", ItemCount: 1, Items: []string{"203.0.113.0/24"}, ReferencedBy: []string{}}
		if r.URL.Path == "/v1/edge-rule-lists" && r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(api.ListEdgeRuleListsResponse{Lists: []api.EdgeRuleListResponse{l}})
			return
		}
		_ = json.NewEncoder(w).Encode(l)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	return srv
}

func TestCmdEdgeRuleListsCreateAndUpdate(t *testing.T) {
	resetJSONEnv(t)
	var method, path string
	var body map[string]any
	edgeRuleListServer(t, &method, &path, &body)
	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	defer func() { osStdout = oldOut }()

	itemsFile := filepath.Join(t.TempDir(), "items.txt")
	if err := os.WriteFile(itemsFile, []byte("# office\n198.51.100.1\n\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := cmdEdgeRuleLists([]string{"create", "--kind", "ip", "--item", "203.0.113.0/24", "--items-file", itemsFile, "office"}); code != 0 {
		t.Fatalf("create = %d", code)
	}
	if method != http.MethodPost || path != "/v1/edge-rule-lists" || body["name"] != "office" ||
		!reflect.DeepEqual(body["items"], []any{"203.0.113.0/24", "198.51.100.1"}) {
		t.Fatalf("create sent %s %s %v", method, path, body)
	}

	if code := cmdEdgeRuleLists([]string{"update", "--add", "10.0.0.1", "--remove", "203.0.113.0/24", "office"}); code != 0 {
		t.Fatalf("update = %d", code)
	}
	if method != http.MethodPatch || path != "/v1/edge-rule-lists/office" || body["items"] != nil ||
		!reflect.DeepEqual(body["add"], []any{"10.0.0.1"}) || body["description"] != nil {
		t.Fatalf("update sent %s %s %v", method, path, body)
	}

	if code := cmdEdgeRuleLists([]string{"rm", "office"}); code != 0 || method != http.MethodDelete {
		t.Fatalf("rm = %d (%s)", code, method)
	}
	if code := cmdEdgeRuleLists([]string{"create", "office"}); code != 1 {
		t.Fatalf("create without --kind = %d, want 1", code)
	}
}

func TestCmdEdgeRuleListsList_JSON(t *testing.T) {
	resetJSONEnv(t)
	jsonOutput = true
	defer func() { jsonOutput = false }()
	var method, path string
	var body map[string]any
	edgeRuleListServer(t, &method, &path, &body)
	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	defer func() { osStdout = oldOut }()

	if code := cmdEdgeRuleLists([]string{"list"}); code != 0 {
		t.Fatalf("list --json = %d", code)
	}
	if !strings.Contains(stdout.String(), `"office"`) {
		t.Fatalf("list --json output = %q", stdout.String())
	}
}
