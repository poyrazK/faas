package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestOwnedConfigClaimsRemovedAndChangedKeysBeforeWrite(t *testing.T) {
	var sequence []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sequence = append(sequence, r.Method+" "+r.URL.Path)
		if r.URL.Path == "/v1/environment-field-ownership" {
			var in fieldOwnershipRequest
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				t.Fatal(err)
			}
			want := []string{"configuration/NEW", "configuration/OLD"}
			if r.Method == http.MethodDelete {
				want = []string{"configuration/OLD"}
			}
			if in.Project != "shop" || in.Environment != "production" || in.Manager != "terraform" || !reflect.DeepEqual(in.Paths, want) {
				t.Errorf("ownership: %+v", in)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"claimed"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"values":{"OLD":true}}`))
		} else {
			_, _ = w.Write([]byte(`{"values":{"NEW":true}}`))
		}
	}))
	defer server.Close()
	client := &client{baseURL: server.URL, token: "test", http: server.Client()}
	if _, err := client.updateOwnedConfig(context.Background(), "shop", "production", json.RawMessage(`{"NEW":true}`)); err != nil {
		t.Fatal(err)
	}
	want := []string{"GET /v1/projects/shop/environments/production/config", "PUT /v1/environment-field-ownership", "PUT /v1/projects/shop/environments/production/config", "DELETE /v1/environment-field-ownership"}
	if !reflect.DeepEqual(sequence, want) {
		t.Fatalf("order: %v", sequence)
	}
}
func TestTerraformOwnershipConflictStopsConfigWrite(t *testing.T) {
	writes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/environment-field-ownership" {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"code":"environment_gitops_stale_plan"}`))
			return
		}
		if r.Method == http.MethodPut {
			writes++
		}
		_, _ = w.Write([]byte(`{"values":{}}`))
	}))
	defer server.Close()
	client := &client{baseURL: server.URL, token: "test", http: server.Client()}
	if _, err := client.updateOwnedConfig(context.Background(), "shop", "production", json.RawMessage(`{"MODE":true}`)); err == nil || writes != 0 {
		t.Fatalf("conflict failed to fence write: %d %v", writes, err)
	}
}
