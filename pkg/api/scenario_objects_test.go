package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/rehearsalobj"
)

func TestScenarioObjectClientRequiresExactFixtureAcknowledgement(t *testing.T) {
	for _, bad := range []bool{false, true} {
		t.Run(map[bool]string{false: "exact", true: "wrong fixture"}[bad], func(t *testing.T) {
			objects := []rehearsalobj.Data{{Key: "binary", Body: []byte{0, 255, 10}, ContentType: "application/octet-stream", Metadata: map[string]string{}}}
			initial, _ := rehearsalobj.Snapshot(objects)
			spec := rehearsalobj.Spec{Version: 1, Bucket: "imports", FixtureSHA256: rehearsalobj.Hash(initial)}
			run := strings.Repeat("a", 32)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req rehearsalobj.CreateRequest
				if json.NewDecoder(r.Body).Decode(&req) != nil || req.Validate() != nil || string(req.Objects[0].Body) != string(objects[0].Body) {
					t.Error("fixture bytes changed on wire")
				}
				ack := rehearsalobj.Receipt{Version: 1, RunID: run, AppID: "app", ResourceID: "resource", CollectorID: "collector", FixtureSHA256: spec.FixtureSHA256, ContractSHA256: spec.ContractSHA256(), InitialSHA256: spec.FixtureSHA256}
				if bad {
					ack.InitialSHA256 = strings.Repeat("1", 64)
				}
				w.WriteHeader(201)
				_ = json.NewEncoder(w).Encode(ack)
			}))
			defer server.Close()
			_, e := NewClient(server.URL, "synthetic").CreateScenarioObjects(t.Context(), run, rehearsalobj.CreateRequest{AppSlug: "app", Spec: spec, Objects: objects})
			if (e != nil) != bad {
				t.Fatalf("ack validation: %v", e)
			}
		})
	}
}
