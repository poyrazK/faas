//go:build managed_postgres_native && metal

package e2e_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/e2etest/postgresstarter"
)

func waitPostgresStarterAdoption(t *testing.T, h *e2etest.Harness, key, slug, deployment string, generation int64) {
	t.Helper()
	postgresNativeWait(t, "current starter binding adoption", func(ctx context.Context) bool {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, h.APIDURL+"/v1/apps/"+slug+"/bindings", nil)
		if err != nil {
			return false
		}
		request.Header.Set("Authorization", "Bearer "+key)
		response, err := h.HTTPClient().Do(request)
		if err != nil {
			return false
		}
		defer func() { _ = response.Body.Close() }()
		data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		var inventory api.AppBindingInventory
		return err == nil && response.StatusCode == http.StatusOK && json.Unmarshal(data, &inventory) == nil && postgresstarter.Adopted(inventory, deployment, generation)
	})
}
