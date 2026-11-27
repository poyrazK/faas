//go:build metal && linux

// elastic_builder_slots_metal_test.go — ADR-20261118154632 on a real
// Firecracker stack. With builder_elastic_slots on and builder_slots at one
// reserved and two slots in all, two source deploys build at once: the first
// takes the reserved slot (faas-cp-build.slice) and the second a borrowed
// slot, so its builder VM runs in faas-borrowed.slice with oom_score_adj
// 1000. Both deploys go live, and the borrowed build leaves no scope behind.
package e2e_test

// adr: 20261118154632

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/builderslots"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/runtimeconfig"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestElasticBuilderSlotsMetal(t *testing.T) {
	if os.Getenv("FAAS_TEST_KERNEL") == "" || os.Getenv("FAAS_BUILDER_BASE_PATH") == "" {
		t.Skip("FAAS_TEST_KERNEL / FAAS_BUILDER_BASE_PATH unset; skipping metal elastic builder slot test")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skipf("/dev/kvm not available: %v", err)
	}
	borrowedDir := ensureBorrowedSlice(t)
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Acceptance hosts have two vCPUs; a compute node runs a borrowed build
	// beside the reserved one only with four or more.
	h := e2etest.Start(t, pool, e2etest.All, "FAAS_BUILDER_SLOT_CPUS=4")
	t.Cleanup(func() {
		if t.Failed() {
			h.DumpLogs(t)
		}
	})
	ctx := context.Background()
	key := h.SeedAccount(ctx, api.PlanPro)
	store := state.NewPgStore(pool)
	var actor string
	if err := pool.QueryRow(ctx, `select id::text from accounts order by created_at limit 1`).Scan(&actor); err != nil {
		t.Fatalf("read an account for the runtime config actor: %v", err)
	}
	for keyName, value := range map[string]string{
		runtimeconfig.KeyBuilderSlots:        `{"default":{"reserved":1,"max":2}}`,
		runtimeconfig.KeyBuilderElasticSlots: `"on"`,
	} {
		if _, err := store.UpsertRuntimeConfig(ctx, state.RuntimeConfigUpdate{
			Key: keyName, Scope: state.RuntimeConfigScopeGlobal, DesiredValue: json.RawMessage(value),
			ApplyMode: state.RuntimeConfigApplyHot, ActorID: actor, Reason: "elastic builder slots metal test",
		}); err != nil {
			t.Fatalf("set %s: %v", keyName, err)
		}
	}
	// The watchers re-read within their five-second repair interval.
	time.Sleep(8 * time.Second)

	createOpenApp(t, h, key, "elastic-a")
	createOpenApp(t, h, key, "elastic-b")

	watchCtx, stopWatch := context.WithCancel(ctx)
	seen := make(chan borrowedSighting, 1)
	go watchBorrowedBuilders(watchCtx, borrowedDir, seen)

	type deployed struct {
		slug, depID, buildID string
	}
	var deploys []deployed
	for _, slug := range []string{"elastic-a", "elastic-b"} {
		body, status := postMultipartDeployment(t, h, key, slug, NodeFixture(t), false, "")
		if status != http.StatusAccepted {
			t.Fatalf("deploy %s: status=%d body=%s", slug, status, body)
		}
		depID, buildID := parseQueuedDeployment(t, body)
		deploys = append(deploys, deployed{slug: slug, depID: depID, buildID: buildID})
	}
	var wg sync.WaitGroup
	errs := make([]error, len(deploys))
	for i, d := range deploys {
		wg.Add(1)
		go func(i int, d deployed) {
			defer wg.Done()
			waitCtx, cancel := context.WithTimeout(ctx, sourceDeployCtxTimeout())
			defer cancel()
			_, build, err := e2etest.WaitForSourceDeployment(waitCtx, t, pool, d.depID, deferredExportStallWindow, e2etest.DefaultBuildCeiling)
			if err == nil && (build.ID != d.buildID || build.Status != state.BuildSucceeded) {
				err = errors.New("build " + build.ID + " is " + string(build.Status))
			}
			errs[i] = err
		}(i, d)
	}
	wg.Wait()
	stopWatch()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("deploy %s did not go live: %v", deploys[i].slug, err)
		}
	}

	t.Run("second-build-borrowed-a-slot", func(t *testing.T) {
		var sighting borrowedSighting
		select {
		case sighting = <-seen:
		default:
			logs := deploymentBuildLog(t, pool, deploys[0].depID) + "\n---\n" + deploymentBuildLog(t, pool, deploys[1].depID)
			t.Fatalf("no builder VM ever ran in %s; build logs:\n%s", borrowedDir, logs)
		}
		if sighting.oomScoreAdj != api.BorrowedBuilderOOMScoreAdj {
			t.Fatalf("borrowed builder %s ran with oom_score_adj %d, want %d", sighting.scope, sighting.oomScoreAdj, api.BorrowedBuilderOOMScoreAdj)
		}
		borrowedLogs := 0
		for _, d := range deploys {
			if strings.Contains(deploymentBuildLog(t, pool, d.depID), "allocated builder slot (borrowed)") {
				borrowedLogs++
			}
		}
		if borrowedLogs != 1 {
			t.Fatalf("%d build logs show a borrowed slot, want exactly 1", borrowedLogs)
		}
		t.Logf("borrowed builder %s ran with oom_score_adj %d", sighting.scope, sighting.oomScoreAdj)
	})
	t.Run("borrowed-scope-cleaned-up", func(t *testing.T) {
		deadline := time.Now().Add(time.Minute)
		for {
			scopes := borrowedScopes(borrowedDir)
			if len(scopes) == 0 {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("borrowed builder scopes left behind: %v", scopes)
			}
			time.Sleep(time.Second)
		}
	})
}

type borrowedSighting struct {
	scope       string
	oomScoreAdj int
}

// ensureBorrowedSlice makes faas-borrowed.slice exist with the per-VM
// controllers enabled, as ansible's systemd_slices role does on a node, and
// removes it again if this test created it.
func ensureBorrowedSlice(t *testing.T) string {
	t.Helper()
	parent := filepath.Join(builderslots.DefaultCgroupRoot, "faas.slice")
	dir := filepath.Join(builderslots.DefaultCgroupRoot, builderslots.BorrowedSlice)
	if _, err := os.Stat(parent); err != nil {
		t.Skipf("no faas.slice on this host: %v", err)
	}
	created := false
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatalf("create %s: %v", dir, err)
		}
		created = true
	}
	if err := os.WriteFile(filepath.Join(parent, "cgroup.subtree_control"), []byte("+cpu +memory +pids"), 0o644); err != nil {
		t.Fatalf("enable faas.slice controllers: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cgroup.subtree_control"), []byte("+cpu +memory +pids"), 0o644); err != nil {
		t.Fatalf("enable %s controllers: %v", dir, err)
	}
	if created {
		t.Cleanup(func() {
			if err := os.Remove(dir); err != nil {
				t.Errorf("remove %s: %v", dir, err)
			}
		})
	}
	return dir
}

func borrowedScopes(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasSuffix(e.Name(), ".slice") {
			out = append(out, e.Name())
		}
	}
	return out
}

// watchBorrowedBuilders reports the first borrowed builder scope that holds
// a process, with that process's oom_score_adj.
func watchBorrowedBuilders(ctx context.Context, dir string, seen chan<- borrowedSighting) {
	t := time.NewTicker(200 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		for _, scope := range borrowedScopes(dir) {
			raw, err := os.ReadFile(filepath.Join(dir, scope, "cgroup.procs"))
			if err != nil {
				continue
			}
			for _, field := range strings.Fields(string(raw)) {
				adj, err := os.ReadFile(filepath.Join("/proc", field, "oom_score_adj"))
				if err != nil {
					continue
				}
				n, err := strconv.Atoi(strings.TrimSpace(string(adj)))
				if err != nil {
					continue
				}
				select {
				case seen <- borrowedSighting{scope: scope, oomScoreAdj: n}:
				default:
				}
				return
			}
		}
	}
}
