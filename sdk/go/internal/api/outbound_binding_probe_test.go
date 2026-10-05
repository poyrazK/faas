// adr: 430 — typed SDK methods preserve policy responses and validate probe metadata.
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOutboundProbePolicyClientWire(t *testing.T) {
	policy := OutboundBindingProbePolicy{Method: "HEAD", Path: "/health", ExpectedStatus: 204}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/outbound/integrations/integration/probe-policy" {
			t.Fatal(r.URL.Path)
		}
		if r.Method == "DELETE" {
			w.WriteHeader(204)
			return
		}
		if r.Method == "PUT" {
			var p OutboundBindingProbePolicy
			_ = json.NewDecoder(r.Body).Decode(&p)
			if p != policy {
				t.Fatalf("policy=%+v", p)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(policy)
	}))
	defer server.Close()
	client := NewClient(server.URL, "test")
	if got, err := client.SetOutboundBindingProbePolicy(context.Background(), "integration", policy); err != nil || got != policy {
		t.Fatalf("put=%+v %v", got, err)
	}
	if got, err := client.GetOutboundBindingProbePolicy(context.Background(), "integration"); err != nil || got != policy {
		t.Fatalf("get=%+v %v", got, err)
	}
	if err := client.DeleteOutboundBindingProbePolicy(context.Background(), "integration"); err != nil {
		t.Fatal(err)
	}
}
