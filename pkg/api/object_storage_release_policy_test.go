package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestObjectStorageReleaseRequirementsValidation(t *testing.T) {
	for _, mode := range []string{"read", "write", "read_write"} {
		if err := ValidateObjectStorageRequirements(map[string]string{"GREGALE_S3_ASSETS": mode}); err != nil {
			t.Fatal(err)
		}
	}
	for _, invalid := range []map[string]string{{"bad": "write"}, {"ASSETS": "delete"}, {"ASSETS": ""}, {"": "read"}} {
		if ValidateObjectStorageRequirements(invalid) == nil {
			t.Fatal("invalid requirement accepted")
		}
	}
	tooMany := map[string]string{}
	for i := 0; i <= ObjectStorageRequirementsMax; i++ {
		tooMany[fmt.Sprintf("ASSET_%d", i)] = "read"
	}
	if ValidateObjectStorageRequirements(tooMany) == nil {
		t.Fatal("unbounded policy accepted")
	}
}

func TestObjectStorageReleasePolicyClientNeverFallsBack(t *testing.T) {
	for _, scenario := range []string{"set", "clear", "old server"} {
		t.Run(scenario, func(t *testing.T) {
			calls := 0
			requirements := map[string]string{"ASSETS": "read_write"}
			if scenario == "clear" {
				requirements = map[string]string{}
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "PUT" || r.URL.Path != "/v1/apps/demo/bindings/release-policy-with-object-storage" {
					t.Errorf("unsafe route: %s", r.URL)
				}
				if scenario == "old server" {
					http.NotFound(w, r)
					return
				}
				var request SetBindingReleasePolicyRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.ObjectStorageRequirements == nil {
					t.Errorf("explicit storage policy lost: %+v %v", request, err)
				}
				_ = json.NewEncoder(w).Encode(BindingReleasePolicy{ObjectStorageRequirements: request.ObjectStorageRequirements})
			}))
			defer server.Close()
			_, err := NewClient(server.URL, "test").SetBindingReleasePolicy(context.Background(), "demo", "production", SetBindingReleasePolicyRequest{ObjectStorageRequirements: requirements})
			if calls != 1 || (err != nil) != (scenario == "old server") {
				t.Fatalf("fallback/qualification: calls=%d err=%v", calls, err)
			}
		})
	}
}
