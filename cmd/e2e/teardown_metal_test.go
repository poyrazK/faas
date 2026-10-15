//go:build metal

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/e2etest"
)

// stopAppOnCleanup leaves the app with no live microVM before the harness
// stops. vmmd keeps its VMs running across a restart (it recovers them), so
// a test that ends with a running instance leaks a Firecracker process,
// its netns and its jail on the host. Registered after e2etest.Start, so it
// runs before the harness's own cleanup, while every daemon is still up,
// and also when the test fails part way.
func stopAppOnCleanup(t *testing.T, h *e2etest.Harness, pool *pgxpool.Pool, key, slug, appID string) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		// Forks are never idle-parked; cancel any that are still active.
		if raw, status := doReq(t, h, key, http.MethodGet, "/v1/apps/"+slug+"/forks", nil); status == http.StatusOK {
			var list api.AppForkListResponse
			if json.Unmarshal(raw, &list) == nil {
				for _, f := range list.Items {
					if f.Status == "queued" || f.Status == "restoring" || f.Status == "running" {
						_, _ = doReq(t, h, key, http.MethodDelete, "/v1/apps/"+slug+"/forks/"+f.ID, nil)
					}
				}
			}
		}
		setAppIdleTimeout(t, h, key, slug, api.IdleTimeoutFloorSeconds)
		for {
			var live int
			err := pool.QueryRow(ctx, `SELECT count(*) FROM instances
				WHERE app_id = $1 AND state IN ('waking', 'cold_booting', 'running', 'snapshotting')`, appID).Scan(&live)
			if err == nil && live == 0 {
				return
			}
			select {
			case <-ctx.Done():
				t.Errorf("teardown: app %s still has %d live instance(s); its microVMs will leak (err=%v)", slug, live, err)
				return
			case <-time.After(time.Second):
			}
		}
	})
}
