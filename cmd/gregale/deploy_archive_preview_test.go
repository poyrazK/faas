package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestValidatePreviewArchivePlanLimitUsesAccountPlan(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/account" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(api.AccountResponse{Plan: string(api.PlanFree)})
	}))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "oversized.tar.gz")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	limit := int64(api.MustLimitsFor(api.PlanFree).SourceTarballMaxMB) * 1024 * 1024
	if err := file.Truncate(limit + 1); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	err = validatePreviewArchivePlanLimit(context.Background(), NewClient(server.URL, "token"), path)
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) || apiErr.Problem.Code != api.CodeSourceTooLarge {
		t.Fatalf("error = %v, want source_too_large API error", err)
	}
}
