package main_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const (
	rollbackStable = "6f1c1b7e-2f0a-4f43-9d2b-0c8a1e5d9a01"
	rollbackNext   = "6f1c1b7e-2f0a-4f43-9d2b-0c8a1e5d9a02"
)

// rollbackServer serves a 75/25 split and records each traffic PATCH body.
// oldAPID answers expected_traffic as an apid released before it would.
func rollbackServer(t *testing.T, oldAPID bool, patches *[]map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/shop/deployments":
			_, _ = io.WriteString(w, `{"items":[`+
				`{"id":"`+rollbackStable+`","status":"live","scope":"production","traffic_percent":75,"revision":1},`+
				`{"id":"`+rollbackNext+`","status":"live","scope":"production","traffic_percent":25,"revision":2}]}`)
		case r.Method == http.MethodPatch && r.URL.Path == "/v1/deployments/"+rollbackStable+"/traffic":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			*patches = append(*patches, body)
			if _, fenced := body["expected_traffic"]; fenced && oldAPID {
				w.Header().Set("Content-Type", "application/problem+json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, `{"status":400,"code":"validation","title":"Bad request","detail":"json: unknown field \"expected_traffic\""}`)
				return
			}
			_, _ = io.WriteString(w, `{"id":"`+rollbackStable+`","status":"live","scope":"production","traffic_percent":100,"revision":1}`)
		default:
			http.NotFound(w, r)
		}
	}))
}

// Traffic rollback sends the split it planned from, so apid refuses the
// write if the split changed in between (storage review finding #2); against
// an older apid it falls back to an unconditional write after a re-read.
func TestTrafficRollbackFencesTheSplit(t *testing.T) {
	for _, oldAPID := range []bool{false, true} {
		var patches []map[string]any
		srv := rollbackServer(t, oldAPID, &patches)
		t.Setenv("FAAS_API", srv.URL)
		t.Setenv("FAAS_TOKEN", "fp_live_test")
		code, stdout, stderr := runCLI(t, "release", "traffic", "rollback", "shop")
		srv.Close()
		if code != 0 || !strings.Contains(stdout, "All traffic of shop") {
			t.Fatalf("oldAPID=%v: exit %d stdout %q stderr %q", oldAPID, code, stdout, stderr)
		}
		expected, _ := patches[0]["expected_traffic"].(map[string]any)
		if expected[rollbackStable] != float64(75) || expected[rollbackNext] != float64(25) || len(expected) != 2 {
			t.Fatalf("oldAPID=%v: first PATCH = %v, want the planned 75/25 split as expected_traffic", oldAPID, patches[0])
		}
		if want := map[bool]int{false: 1, true: 2}[oldAPID]; len(patches) != want {
			t.Fatalf("oldAPID=%v: %d PATCHes, want %d", oldAPID, len(patches), want)
		}
		if oldAPID {
			if _, fenced := patches[1]["expected_traffic"]; fenced {
				t.Fatal("fallback write still sent expected_traffic")
			}
		}
	}
}
