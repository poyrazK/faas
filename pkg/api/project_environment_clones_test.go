// adr: 585
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFullProjectEnvironmentCloneUsesDedicatedRoute(t *testing.T) {
	for _, response := range []string{"operation", "partial"} {
		t.Run(response, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodPost || r.URL.Path != "/v1/projects/shop/environment-clones" {
					t.Errorf("request = %s %s", r.Method, r.URL.Path)
				}
				var req CreateProjectEnvironmentRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil || !req.Full || req.ShareResources {
					t.Errorf("body = %+v, %v", req, err)
				}
				out := ProjectEnvironmentResponse{Slug: "stage"}
				if response == "operation" {
					out.CloneOperation = &ProjectEnvironmentCloneOperationResponse{OperationID: "op-1", ProjectSlug: "shop", SourceEnvironment: "production", TargetEnvironment: "stage", Status: "pending"}
				}
				w.WriteHeader(http.StatusAccepted)
				_ = json.NewEncoder(w).Encode(out)
			}))
			defer srv.Close()
			_, err := NewClient(srv.URL, "test").CreateProjectEnvironment(context.Background(), "shop", CreateProjectEnvironmentRequest{
				Slug: "stage", FromEnvironment: "production", Full: true,
			})
			if (err == nil) != (response == "operation") || calls != 1 {
				t.Fatalf("error = %v, calls = %d", err, calls)
			}
		})
	}
}

func TestFullProjectEnvironmentCloneNeverFallsBack(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/projects/shop/environment-clones" {
			t.Errorf("unexpected partial create: %s", r.URL.Path)
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	_, err := NewClient(srv.URL, "test").CreateProjectEnvironment(context.Background(), "shop", CreateProjectEnvironmentRequest{Slug: "stage", FromEnvironment: "production", Full: true})
	if err == nil || calls != 1 {
		t.Fatalf("error = %v, calls = %d", err, calls)
	}
}
