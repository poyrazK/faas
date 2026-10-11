package main

// adr: 791
import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRegionalDirectoryCommandsEmitJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer directory-key" {
			t.Error("missing directory credential")
		}
		switch r.URL.Path {
		case "/v1/regions":
			_, _ = w.Write([]byte(`[{"id":"eu-fsn","display_name":"Falkenstein","availability":"available"}]`))
		case "/v1/global-apps":
			_, _ = w.Write([]byte(`[{"id":"global-id","slug":"global-shop","routing_policy":"all_selected_regions","targets":[]}]`))
		default:
			t.Errorf("unexpected directory path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "directory-key")
	out, _, restore := swapIO(t)
	defer restore()
	oldJSON := jsonOutput
	jsonOutput = true
	t.Cleanup(func() { jsonOutput = oldJSON })
	if code := cmdRegions(nil); code != 0 || !json.Valid(out.Bytes()) {
		t.Fatalf("region JSON: exit=%d output=%s", code, out.String())
	}
	out.Reset()
	if code := cmdGlobalApp([]string{"list"}); code != 0 || !json.Valid(out.Bytes()) {
		t.Fatalf("global app JSON: exit=%d output=%s", code, out.String())
	}
	var apps []struct {
		RoutingPolicy string `json:"routing_policy"`
	}
	if err := json.Unmarshal(out.Bytes(), &apps); err != nil || len(apps) != 1 || apps[0].RoutingPolicy != "all_selected_regions" {
		t.Fatalf("global app routing default missing from JSON: %+v %v", apps, err)
	}
}
