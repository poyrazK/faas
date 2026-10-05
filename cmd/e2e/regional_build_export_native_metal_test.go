//go:build metal

package e2e_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/region"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// This is a dedicated-fleet acceptance test: it stops services on two workers.
// The source fixture must be a known-good HTTP app archive for this fleet.
func TestNativeRegionalBuildExportAcceptance(t *testing.T) {
	if os.Getenv("FAAS_REGION_ACCEPTANCE_CONFIRM") != "native-x86" {
		t.Skip("requires dedicated native regional fleet; see ADR-586")
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Fatal("regional build acceptance requires native Linux x86_64")
	}
	required := func(key string) string {
		t.Helper()
		value := os.Getenv(key)
		if value == "" {
			t.Fatalf("%s is required", key)
		}
		return value
	}
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Minute)
	defer cancel()
	pool, err := pgxpool.New(ctx, required("DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	store := state.NewPgStore(pool)
	client := api.NewClient(required("FAAS_REGION_API_URL"), required("FAAS_REGION_API_TOKEN"))
	sourcePath := required("FAAS_REGION_SOURCE_TARBALL")
	source, err := os.Open(sourcePath) //nolint:forbidigo // Operator-supplied acceptance archive; the descriptor is checked below.
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = source.Close() }()
	info, err := source.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 {
		t.Fatal("FAAS_REGION_SOURCE_TARBALL must name a nonempty regular archive")
	}
	nodes, targets := regionalAcceptanceWorkers(t, ctx, store, required)
	regionID := *nodes[0].Region
	registry, err := store.ComputeRegionByID(ctx, regionID)
	if err != nil || !registry.AcceptingNewApps {
		t.Fatalf("region must accept apps: %v", err)
	}
	app, err := client.CreateApp(ctx, api.CreateAppRequest{Slug: "regional-export-" + uuid.NewString()[:12], ComputeRegion: regionID, RAMMB: 128, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), time.Minute)
		defer stop()
		if err := client.DeleteApp(cleanup, app.Slug); err != nil {
			t.Errorf("delete acceptance app: %v", err)
		}
	})
	service := func(ctx context.Context, index int, action, unit string) error {
		return exec.CommandContext(ctx, "ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", targets[index], "sudo", "-n", "systemctl", action, unit).Run()
	}
	for i := range nodes[:2] {
		for _, unit := range []string{"faas-builderd", "faas-imaged", "faas-vmmd"} {
			if err := service(ctx, i, "is-active", unit); err != nil {
				t.Fatalf("%s/%s must initially be running: %v", nodes[i].Name, unit, err)
			}
		}
	}
	type stoppedService struct {
		index int
		unit  string
	}
	var stopped []stoppedService
	disabled := false
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 2*time.Minute)
		defer stop()
		for i := len(stopped) - 1; i >= 0; i-- {
			if err := service(cleanup, stopped[i].index, "start", stopped[i].unit); err != nil {
				t.Errorf("restore %s: %v", stopped[i].unit, err)
			}
		}
		if disabled {
			if err := store.SetComputeNodeActive(cleanup, nodes[0].ID, true); err != nil {
				t.Errorf("restore producer admission: %v", err)
			}
		}
	})
	stopService := func(index int, unit string) {
		t.Helper()
		// Record before stopping: a timeout can still leave the unit stopped.
		stopped = append(stopped, stoppedService{index, unit})
		if err := service(ctx, index, "stop", unit); err != nil {
			t.Fatalf("stop %s: %v", unit, err)
		}
	}
	// Only A can build. No same-region imaged can consume until we capture
	// completion and make A unavailable; B must respect regional eligibility.
	stopService(1, "faas-builderd")
	stopService(0, "faas-imaged")
	stopService(1, "faas-imaged")
	dep, err := client.DeployFromSourceTarball(ctx, app.Slug, source, filepath.Base(sourcePath), api.SourceTarballDeployRequest{})
	if err != nil {
		t.Fatal(err)
	}
	completed := waitRegionalBuildExport(t, ctx, store, pool, dep.ID, nodes[0])
	stopService(0, "faas-builderd")
	stopService(0, "faas-vmmd")
	disabled = true
	if err := store.SetComputeNodeActive(ctx, nodes[0].ID, false); err != nil {
		t.Fatal(err)
	}
	// The consumer must have no local copy of the diagnostic producer path.
	quotedPath := "'" + strings.ReplaceAll(completed.RootfsPath, "'", "'\\''") + "'"
	if err := exec.CommandContext(ctx, "ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", targets[1], "test ! -e "+quotedPath).Run(); err != nil {
		t.Fatalf("peer must not have access to the producer export: %v", err)
	}
	if err := service(ctx, 1, "start", "faas-imaged"); err != nil {
		t.Fatal(err)
	}
	if _, err := e2etest.WaitForDeploymentLive(ctx, t, pool, dep.ID, 5*time.Minute); err != nil {
		t.Fatal(err)
	}
	final, err := store.DeploymentByID(ctx, dep.ID)
	wantKey := region.ArtifactKey(regionID, sched.AppLayerKey(app.Slug, dep.ID))
	if err != nil || final.BuildArtifactReference() != completed.BuildArtifactReference() || final.RootfsKey != wantKey {
		t.Fatalf("regional runtime publication lost the export: %+v %v", final, err)
	}
	build, err := store.BuildByDeployment(ctx, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	provenance, err := store.BuildProvenanceByBuildID(ctx, build.ID)
	if err != nil || provenance.SBOMStorageKey != region.BuildSBOMKey(regionID, build.ID) {
		t.Fatalf("regional SBOM reference: %+v %v", provenance, err)
	}
	sbom, err := client.GetBuildsIdSbom(ctx, build.ID)
	var document struct {
		Format string `json:"bomFormat"`
	}
	if err != nil || json.Unmarshal(sbom, &document) != nil || document.Format != "CycloneDX" {
		t.Fatalf("API cannot serve regional CycloneDX SBOM after producer loss: %v", err)
	}
	if err := client.Park(ctx, app.Slug); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "UPDATE snapshots SET stale=true WHERE deployment_id=$1", dep.ID); err != nil {
		t.Fatal(err)
	}
	waitRegionalSourceHTTP(t, ctx, app.URL)
	current, err := store.AppByID(ctx, app.ID)
	if err != nil || current.ComputeRegion != regionID || current.NodeID != nodes[1].ID {
		t.Fatalf("deployment did not become live on peer: %+v %v", current, err)
	}
	requireNativeRegionalLogBindings(t, ctx, store, app.ID, dep.ID, regionID, nodes[1].ID)
	var escaped int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM instances i JOIN compute_nodes n ON n.id=i.node_id WHERE i.app_id=$1 AND n.region IS DISTINCT FROM $2`, app.ID, regionID).Scan(&escaped); err != nil || escaped != 0 {
		t.Fatalf("source-build instances escaped region: count=%d err=%v", escaped, err)
	}
}

func waitRegionalSourceHTTP(t *testing.T, ctx context.Context, url string) {
	t.Helper()
	client := &http.Client{Timeout: 10 * time.Second}
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(time.Second):
		}
	}
	t.Fatal("regional source app could not cold-boot on its peer after producer loss")
}

func waitRegionalBuildExport(t *testing.T, ctx context.Context, store *state.PgStore, pool *pgxpool.Pool, id string, producer state.ComputeNode) state.Deployment {
	t.Helper()
	deadline := time.Now().Add(12 * time.Minute)
	for time.Now().Before(deadline) {
		dep, err := store.DeploymentByID(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if dep.Status == state.DeployFailed || dep.Status == state.DeployCancelled {
			t.Fatalf("source deployment failed: %s", dep.Error)
		}
		var status, owner string
		err = pool.QueryRow(ctx, `SELECT status,coalesce(builder_node_id::text,'') FROM builds WHERE deployment_id=$1 ORDER BY enqueued_at DESC,id DESC LIMIT 1`, id).Scan(&status, &owner)
		if err == nil && status == string(state.BuildSucceeded) {
			if owner != producer.ID || !dep.BuildArtifactReference().ValidFor(*producer.Region) || !strings.HasPrefix(dep.SourceStorageKey, "regions/"+*producer.Region+"/sources/") || !filepath.IsAbs(dep.RootfsPath) {
				t.Fatalf("source/export completion escaped placement or lost metadata: %+v owner=%s", dep, owner)
			}
			return dep
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(time.Second):
		}
	}
	t.Fatal("timed out waiting for durable regional build completion")
	return state.Deployment{}
}

// A real cold boot must capture ownership before its ring can archive evictions.
func requireNativeRegionalLogBindings(t *testing.T, ctx context.Context, s *state.PgStore, appID, depID, regionID, nodeID string) {
	t.Helper()
	instances, err := s.ListInstancesForApp(ctx, appID)
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, instance := range instances {
		if instance.State != string(state.StateRunning) {
			continue
		}
		binding, err := s.RuntimeLogArchiveBindingByInstance(ctx, instance.ID)
		if err != nil || binding.AppID != appID || binding.DeploymentID != depID || binding.ComputeRegion != regionID || instance.NodeID != nodeID {
			t.Fatalf("native wake did not capture regional log identity: %+v %v", binding, err)
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("no running native instance to verify log archive binding")
	}
}
