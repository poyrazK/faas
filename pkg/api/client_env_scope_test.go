package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetAppsSlugEnvWithScope(t *testing.T) {
	for _, scope := range []string{"", "default", "staging"} {
		t.Run(scope, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/v1/apps/shop-api/env" || r.URL.Query().Get("scope") != scope {
					t.Errorf("wrong scoped metadata request: %s %s", r.Method, r.URL)
				}
				_, _ = io.WriteString(w, `{"env":[{"key":"REGION"}],"count":1,"quota_max":8}`)
			}))
			defer server.Close()
			client := NewClient(server.URL, "test-token")
			var metadata AppEnvListResponse
			var err error
			if scope == "" {
				metadata, err = client.GetAppsSlugEnv(context.Background(), "shop-api")
			} else {
				metadata, err = client.GetAppsSlugEnvWithScope(context.Background(), "shop-api", scope)
			}
			if err != nil || len(metadata.Env) != 1 || metadata.Env[0].Key != "REGION" {
				t.Fatalf("metadata=%+v err=%v", metadata, err)
			}
		})
	}
}
