package faas_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	faas "github.com/poyrazK/faas/sdk/go"
)

func TestSecretReferenceNamesAndEnvironmentTransport(t *testing.T) {
	methods := []string{http.MethodPut, http.MethodGet, http.MethodDelete}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls >= len(methods) {
			t.Error("unexpected request")
			w.WriteHeader(500)
			return
		}
		if r.Method != methods[calls] || r.Header.Get("Authorization") != "Bearer token" || r.URL.Query().Get("environment") != "production" {
			t.Errorf("request: %s %s", r.Method, r.URL.String())
		}
		path := "/v1/apps/my%20app/secret-references"
		if calls != 1 {
			path += "/DATABASE_URL"
		}
		if r.URL.EscapedPath() != path {
			t.Errorf("escaped path: %s", r.URL.EscapedPath())
		}
		calls++
		switch r.Method {
		case http.MethodPut:
			var body faas.PutAppSecretReferenceRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Reference != "secret:DATABASE" {
				t.Errorf("body: %+v %v", body, err)
			}
			_ = json.NewEncoder(w).Encode(faas.AppSecretReferenceResponse{EnvironmentID: "env", Environment: "production", Key: "DATABASE_URL", Reference: body.Reference})
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(faas.AppSecretReferenceListResponse{EnvironmentID: "env", Environment: "production", References: map[string]string{"DATABASE_URL": "secret:DATABASE"}, SuppressedKeys: []string{"REMOVED"}, Count: 1, Quota: 20})
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer server.Close()
	client, err := faas.NewClient(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	set, err := client.SetAppSecretReference(ctx, "my app", "production", "DATABASE_URL", faas.PutAppSecretReferenceRequest{Reference: "secret:DATABASE"})
	if err != nil || set.EnvironmentID != "env" || set.Reference != "secret:DATABASE" {
		t.Fatalf("set: %+v %v", set, err)
	}
	list, err := client.ListAppSecretReferences(ctx, "my app", "production")
	if err != nil || list.References["DATABASE_URL"] != "secret:DATABASE" || list.Count != 1 || len(list.SuppressedKeys) != 1 || list.SuppressedKeys[0] != "REMOVED" {
		t.Fatalf("list: %+v %v", list, err)
	}
	if err := client.DeleteAppSecretReference(ctx, "my app", "production", "DATABASE_URL"); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("calls: %d", calls)
	}
}
