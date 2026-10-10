package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGetAppsDeploymentOpenAPIDocCaptureMetadata(t *testing.T) {
	for _, headers := range []bool{true, false} {
		t.Run(map[bool]string{true: "authoritative_headers", false: "no_inferred_identity"}[headers], func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer token" {
					t.Error("missing authentication")
				}
				if headers {
					w.Header().Set("X-OpenAPI-Doc-Deployment-ID", "server-deployment")
					w.Header().Set("X-OpenAPI-Doc-App-ID", "server-app")
					w.Header().Set("X-OpenAPI-Doc-SHA256", strings.Repeat("a", 64))
					w.Header().Set("X-OpenAPI-Doc-Truncated", "1")
					w.Header().Set("X-OpenAPI-Doc-Captured-At", "2026-10-08T10:00:00Z")
				}
				w.Write([]byte(`{"openapi":"3.0.3","paths":{}}`))
			}))
			defer server.Close()
			doc, err := NewClient(server.URL, "token").GetAppsDeploymentOpenAPIDoc(context.Background(), "requested-app", "requested-deployment")
			if err != nil || doc.Doc["openapi"] != "3.0.3" {
				t.Fatalf("raw capture: %+v %v", doc, err)
			}
			if headers {
				if doc.DeploymentID != "server-deployment" || doc.AppID != "server-app" || doc.DocSHA256 != strings.Repeat("a", 64) || !doc.Truncated || doc.CapturedAt == "" {
					t.Fatalf("metadata lost: %+v", doc)
				}
			} else if doc.DeploymentID != "" || doc.AppID != "" || doc.DocSHA256 != "" {
				t.Fatalf("fabricated metadata: %+v", doc)
			}
		})
	}
}
