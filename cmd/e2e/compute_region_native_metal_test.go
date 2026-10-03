//go:build metal

package e2e_test

import (
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestNativeComputeRegionAcceptance uses a dedicated three-worker native fleet:
// two workers in the selected region and one in a different region. It creates
// its own app through apid, drives real HTTP wakes, and stops vmmd over SSH.
// Ordinary metal runs skip it unless the explicit dedicated-fleet opt-in is set.
func TestNativeComputeRegionAcceptance(t *testing.T) {
	if os.Getenv("FAAS_REGION_ACCEPTANCE_CONFIRM") != "native-x86" {
		t.Skip("requires dedicated native regional fleet; see ADR-466")
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Fatal("regional acceptance must run on native Linux x86_64")
	}
	required := func(key string) string {
		t.Helper()
		value := os.Getenv(key)
		if value == "" {
			t.Fatalf("%s is required", key)
		}
		return value
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Minute)
	defer cancel()
	pool, err := pgxpool.New(ctx, required("DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	store := state.NewPgStore(pool)
	client := api.NewClient(required("FAAS_REGION_API_URL"), required("FAAS_REGION_API_TOKEN"))
	image := required("FAAS_REGION_IMAGE")
	if !regexp.MustCompile(`@sha256:[a-f0-9]{64}$`).MatchString(image) {
		t.Fatal("FAAS_REGION_IMAGE must be digest-pinned")
	}
	nodes, targets := regionalAcceptanceWorkers(t, ctx, store, required)
	ssh := make(map[string]string)
	for i, node := range nodes {
		ssh[node.ID] = targets[i]
	}
	regionID := *nodes[0].Region
	region, err := store.ComputeRegionByID(ctx, regionID)
	if err != nil || !region.AcceptingNewApps {
		t.Fatalf("selected region must accept new apps: %v", err)
	}
	app, err := client.CreateApp(ctx, api.CreateAppRequest{Slug: "regional-native-" + uuid.NewString()[:12], ComputeRegion: regionID, RAMMB: 128, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := client.DeleteApp(cleanup, app.Slug); err != nil {
			t.Errorf("delete acceptance app: %v", err)
		}
	})
	stopped := map[string]bool{}
	service := func(ctx context.Context, nodeID, action string) error {
		return exec.CommandContext(ctx, "ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", ssh[nodeID], "sudo", "-n", "systemctl", action, "faas-vmmd").Run()
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		for nodeID := range stopped {
			if err := service(cleanup, nodeID, "start"); err != nil {
				t.Errorf("restore vmmd: %v", err)
			}
		}
		for _, n := range nodes[:2] {
			if _, err := pool.Exec(cleanup, "UPDATE compute_nodes SET admission_ceiling_mb=$2 WHERE id=$1", n.ID, n.AdmissionCeilingMB); err != nil {
				t.Errorf("restore ceiling: %v", err)
			}
		}
		if _, err := store.UpsertComputeRegion(cleanup, region); err != nil {
			t.Errorf("restore region policy: %v", err)
		}
	})
	dep, err := client.Deploy(ctx, app.Slug, api.CreateDeploymentRequest{Image: image})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e2etest.WaitForDeploymentLive(ctx, t, pool, dep.ID, 3*time.Minute); err != nil {
		t.Fatal(err)
	}
	checkPlacement := func(t *testing.T) {
		t.Helper()
		var escaped int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM instances i JOIN compute_nodes n ON n.id=i.node_id WHERE i.app_id=$1 AND n.region IS DISTINCT FROM $2`, app.ID, regionID).Scan(&escaped); err != nil || escaped != 0 {
			t.Fatalf("instances escaped selected region: count=%d err=%v", escaped, err)
		}
		current, err := store.AppByID(ctx, app.ID)
		if err != nil || current.ComputeRegion != regionID {
			t.Fatalf("lost region: %v", err)
		}
		if current.NodeID != "" {
			owner, err := store.ComputeNodeByID(ctx, current.NodeID)
			if err != nil || !state.ComputeRegionAllowsNode(regionID, owner) {
				t.Fatalf("owner escaped region: %v", err)
			}
		}
	}
	wait := func(t *testing.T, description string, predicate func() bool) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Minute)
		for time.Now().Before(deadline) {
			if predicate() {
				checkPlacement(t)
				return
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(time.Second):
			}
		}
		t.Fatalf("timed out: %s", description)
	}
	request := func(t *testing.T) int {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, app.URL, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := (&http.Client{Timeout: 40 * time.Second}).Do(req)
		if err != nil {
			return 0
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	park := func(t *testing.T) {
		t.Helper()
		if err := client.Park(ctx, app.Slug); err != nil {
			t.Fatal(err)
		}
	}
	if t.Failed() {
		return
	}
	t.Run("paused_admission_preserves_cold_wake", func(t *testing.T) {
		park(t)
		if _, err := pool.Exec(ctx, "UPDATE snapshots SET stale=true WHERE deployment_id=$1", dep.ID); err != nil {
			t.Fatal(err)
		}
		paused := region
		paused.AcceptingNewApps = false
		if _, err := store.UpsertComputeRegion(ctx, paused); err != nil {
			t.Fatal(err)
		}
		wait(t, "cold wake while new-app admission is paused", func() bool { return request(t) == http.StatusOK })
	})
	if t.Failed() {
		return
	}
	t.Run("regional_saturation_refuses_remote_capacity", func(t *testing.T) {
		park(t)
		for _, n := range nodes[:2] {
			if _, err := pool.Exec(ctx, "UPDATE compute_nodes SET admission_ceiling_mb=1 WHERE id=$1", n.ID); err != nil {
				t.Fatal(err)
			}
		}
		wait(t, "regional capacity refusal", func() bool { return request(t) == http.StatusServiceUnavailable })
		for _, n := range nodes[:2] {
			if _, err := pool.Exec(ctx, "UPDATE compute_nodes SET admission_ceiling_mb=$2 WHERE id=$1", n.ID, n.AdmissionCeilingMB); err != nil {
				t.Fatal(err)
			}
		}
	})
	if t.Failed() {
		return
	}
	t.Run("owner_recovery_uses_same_region_peer", func(t *testing.T) {
		park(t)
		current, err := store.AppByID(ctx, app.ID)
		if err != nil || ssh[current.NodeID] == "" {
			t.Fatalf("missing owner: %v", err)
		}
		stopped[current.NodeID] = true
		if err := service(ctx, current.NodeID, "stop"); err != nil {
			t.Fatal(err)
		}
		wait(t, "same-region ownership recovery", func() bool {
			recovered, err := store.AppByID(ctx, app.ID)
			return err == nil && recovered.NodeID != current.NodeID && recovered.NodeID != ""
		})
		wait(t, "wake on recovered owner", func() bool { return request(t) == http.StatusOK })
	})
	if t.Failed() {
		return
	}
	t.Run("whole_region_outage_remains_visible_and_refuses_remote_execution", func(t *testing.T) {
		park(t)
		for _, n := range nodes[:2] {
			if stopped[n.ID] {
				continue
			}
			stopped[n.ID] = true
			if err := service(ctx, n.ID, "stop"); err != nil {
				t.Fatal(err)
			}
		}
		wait(t, "unavailable region retained in discovery", func() bool {
			catalog, err := client.ListComputeRegions(ctx)
			if err != nil {
				return false
			}
			for _, r := range catalog {
				if r.ID == regionID {
					return r.Availability == "unavailable" && r.EligibleNodes == 0 && !r.AcceptingNewApps
				}
			}
			return false
		})
		wait(t, "no cross-region execution during outage", func() bool { return request(t) == http.StatusServiceUnavailable })
		t.Logf("qualified app %s in region %s against remote region %s", app.ID, regionID, *nodes[2].Region)
	})
	if t.Failed() {
		t.Logf("regional acceptance failed for app %s", app.ID)
	}
}
