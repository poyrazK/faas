package journeys

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
)

// Private response evidence must identify the exact release and application
// version. A successful control-plane envelope is insufficient.
func TestHarnessFirstHourEvidence(t *testing.T) {
	for _, mutation := range []string{"valid", "wrong-release", "wrong-app", "wrong-scope", "nil-instance", "invalid-instance", "app-401", "unverified", "public-proof", "missing-public-proof", "old-source", "oversized-body"} {
		t.Run(mutation, func(t *testing.T) {
			doc := map[string]any{
				"deployment_id": "release-1", "app_id": "app-1", "scope": "default",
				"instance_id": "11111111-1111-4111-8111-111111111111", "http_status": 200,
				"verified": true, "public_access_verified": false, "body": `{"version":"v2"}`,
			}
			switch mutation {
			case "wrong-release":
				doc["deployment_id"] = "release-2"
			case "wrong-app":
				doc["app_id"] = "app-2"
			case "wrong-scope":
				doc["scope"] = "preview"
			case "nil-instance":
				doc["instance_id"] = "00000000-0000-0000-0000-000000000000"
			case "invalid-instance":
				doc["instance_id"] = "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"
			case "app-401":
				doc["http_status"] = 401
			case "unverified":
				doc["verified"] = false
			case "public-proof":
				doc["public_access_verified"] = true
			case "missing-public-proof":
				delete(doc, "public_access_verified")
			case "old-source":
				doc["body"] = `{"version":"v1"}`
			case "oversized-body":
				doc["body"] = string(make([]byte, 4097))
			}
			raw, err := json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			err = validateFirstHourEvidence(raw, "release-1", "app-1", "v2")
			if (err == nil) != (mutation == "valid") {
				t.Fatalf("evidence accepted=%v for %s", err == nil, mutation)
			}
		})
	}
}

func TestHarnessFirstHourInterrupt(t *testing.T) {
	cli := os.Getenv("GREGALE_JOURNEY_TEST_CLI")
	if cli == "" {
		t.Skip("set GREGALE_JOURNEY_TEST_CLI to exercise interruption with the real CLI")
	}
	const id = "11111111111141118111111111111111"
	var reads, unexpected atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/capabilities":
			_, _ = w.Write([]byte(`{"hidden_capabilities":[]}`))
		case "GET /v1/deployments/" + id:
			reads.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]string{"id": id, "app_id": "22222222222242228222222222222222", "status": "building", "scope": "default"})
		default:
			unexpected.Add(1)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	tg := &target{cli: cli, api: server.URL, home: t.TempDir(), token: "fp_test_first-hour-interrupt"}
	if err := interruptFirstHourWait(tg, id); err != nil {
		t.Fatal(err)
	}
	if reads.Load() == 0 || unexpected.Load() != 0 {
		t.Fatalf("interruption must observe only the exact read-only target: reads=%d unexpected=%d", reads.Load(), unexpected.Load())
	}
}
