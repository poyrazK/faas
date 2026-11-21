package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestRunArtifactDeliveryClientKeepsStorageIntentAcrossReplay(t *testing.T) {
	var requests []DeliverRunArtifactRequest
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("missing authority")
		}
		paths = append(paths, r.Method+" "+r.URL.EscapedPath())
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && r.URL.Path == "/v1/apps/reports/run-artifact-deliveries/reports" {
			var body DeliverRunArtifactRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			requests = append(requests, body)
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"id":"delivery-one","status":"pending"}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"delivery-one","status":"completed","url":"https://objects.invalid/report","method":"GET"}`))
	}))
	defer server.Close()
	client := NewClient(server.URL, "test-key")
	intent := DeliverRunArtifactRequest{IdempotencyKey: "completion-one", ExecutionID: "run", ArtifactName: "report.html", SizeBytes: 20, SHA256: "sha256:expected", ContentType: "text/html"}
	for range 2 {
		if out, err := client.DeliverRunArtifact(t.Context(), "reports", "reports", intent); err != nil || out.Status != "pending" {
			t.Fatal(out, err)
		}
	}
	if _, err := client.GetRunArtifactDelivery(t.Context(), "reports", "reports", "delivery-one"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.SignRunArtifactDelivery(t.Context(), "reports", "reports", "delivery-one"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(requests, []DeliverRunArtifactRequest{intent, intent}) {
		t.Fatal("delivery identity changed", requests)
	}
	want := []string{"POST /v1/apps/reports/run-artifact-deliveries/reports", "POST /v1/apps/reports/run-artifact-deliveries/reports", "GET /v1/apps/reports/run-artifact-deliveries/reports/delivery-one", "POST /v1/apps/reports/run-artifact-deliveries/reports/delivery-one/download-url"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatal(paths)
	}
}
