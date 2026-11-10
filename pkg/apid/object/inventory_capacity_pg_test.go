//go:build !no_pg

// adr: 20261110151628

package object

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestInventorySweepCapacity measures what one apid inventory sweep refreshes
// against PostgreSQL and a lister with a fixed provider page latency, the
// input to ADR-20261110151628. Each scenario seeds more due buckets than one
// sweep can reach and runs the real sweep (reconcileObjectInventories) once or
// twice. It reads wall-clock time, so it runs only when
// FAAS_INVENTORY_CAPACITY=1 (about seven minutes):
//
//	FAAS_INVENTORY_CAPACITY=1 DATABASE_URL=... go test -run '^TestInventorySweepCapacity$' -v ./pkg/apid/object
//
// Results: docs/ops/evidence/20261110-object-inventory-capacity/REPORT.md.
func TestInventorySweepCapacity(t *testing.T) {
	if os.Getenv("FAAS_INVENTORY_CAPACITY") == "" {
		t.Skip("set FAAS_INVENTORY_CAPACITY=1 to measure inventory sweep capacity (wall-clock, ~7 min)")
	}
	scenarios := []struct {
		name         string
		latency      time.Duration
		small, large int
		largePages   int
		sweeps       int
	}{
		{name: "small-100ms", latency: 100 * time.Millisecond, small: 4000, sweeps: 1},
		{name: "small-200ms", latency: 200 * time.Millisecond, small: 3000, sweeps: 1},
		{name: "small-300ms", latency: 300 * time.Millisecond, small: 2000, sweeps: 1},
		{name: "mixed-200ms", latency: 200 * time.Millisecond, small: 2000, large: 16, largePages: 500, sweeps: 2},
	}
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			st := state.NewPgStore(pgtest.OpenMigrated(t))
			lister := &latencyLister{latency: sc.latency, keys: map[string]int{}}
			svc, err := seedInventoryCapacity(t, st, lister, sc.small, sc.large, sc.largePages)
			if err != nil {
				t.Fatal(err)
			}
			due := time.Now()
			if _, err = st.DueObjectInventories(t.Context(), api.ObjectStorageInventorySweepBatch); err != nil {
				t.Fatal(err)
			}
			t.Logf("due query over %d buckets: %s", sc.small+sc.large, time.Since(due).Round(time.Microsecond))
			for sweep := 1; sweep <= sc.sweeps; sweep++ {
				var mu sync.Mutex
				outcomes := map[string]int{}
				pagesBefore := lister.pages.Load()
				start := time.Now()
				more, err := svc.reconcileObjectInventories(t.Context(), func(outcome string) {
					mu.Lock()
					outcomes[outcome]++
					mu.Unlock()
				}, nil)
				elapsed := time.Since(start)
				if err != nil {
					t.Fatal(err)
				}
				pages := lister.pages.Load() - pagesBefore
				busy := time.Duration(pages) * sc.latency
				t.Logf("sweep %d: %s, outcomes %v, pages %d, %.1f attempts/s, %.1f pages/s, listers busy %.0f%%, more %v",
					sweep, elapsed.Round(time.Millisecond), outcomes, pages,
					float64(outcomes["success"]+outcomes["partial"]+outcomes["failed"])/elapsed.Seconds(),
					float64(pages)/elapsed.Seconds(),
					100*busy.Seconds()/(float64(api.ObjectStorageInventorySweepConcurrency)*elapsed.Seconds()), more)
			}
		})
	}
}

// latencyLister serves "k%09d" keys of one byte, 1,000 to a page, after a fixed
// latency, as a provider bucket listing would.
type latencyLister struct {
	objectstorage.Provider
	latency time.Duration
	keys    map[string]int // physical bucket name → keys; written only while seeding
	pages   atomic.Int64
}

func (p *latencyLister) ListObjectsV2(ctx context.Context, physical string, r objectstorage.ObjectListRequest) (objectstorage.ObjectPage, error) {
	timer := time.NewTimer(p.latency)
	select {
	case <-ctx.Done():
		timer.Stop()
		return objectstorage.ObjectPage{}, ctx.Err()
	case <-timer.C:
	}
	p.pages.Add(1)
	total := p.keys[physical]
	start := 0
	if r.Cursor != "" {
		start, _ = strconv.Atoi(r.Cursor)
	} else if r.StartAfter != "" {
		n, _ := strconv.Atoi(strings.TrimPrefix(r.StartAfter, "k"))
		start = n + 1
	}
	end := min(start+int(r.Limit), total)
	var page objectstorage.ObjectPage
	for i := start; i < end; i++ {
		page.Items = append(page.Items, objectstorage.Object{Key: fmt.Sprintf("k%09d", i), Size: 1})
	}
	if end < total {
		page.NextCursor = strconv.Itoa(end)
	}
	return page, nil
}

type capacityHost struct {
	Host
	store    state.Store
	registry *objectstorage.Registry
}

func (h capacityHost) Store() state.Store                     { return h.store }
func (h capacityHost) ObjectStorage() *objectstorage.Registry { return h.registry }
func (h capacityHost) Logger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// seedInventoryCapacity creates small three-key buckets and large buckets of
// largePages full pages, ten to an app, all ready and never inventoried.
func seedInventoryCapacity(t *testing.T, st *state.PgStore, lister *latencyLister, small, large, largePages int) (Service, error) {
	policy := api.ObjectStoragePolicy{MaxAccountBytes: 1 << 40, MaxBucketBytes: 1 << 40, MaxAccountKeys: api.MaxObjectStorageAccountKeys, MaxMonthlyCostMillicents: 1 << 40, MaxMonthlyRequests: 1 << 40, MaxMonthlyEgressBytes: 1 << 40, MaxMonthlyAuthorizations: 1 << 40, MaxReportAgeSeconds: 3600}
	registry, err := objectstorage.NewRegistry(objectstorage.Config{Accounting: &policy, DefaultRegion: "us-east-1", Defaults: map[string]string{"us-east-1": "ovh"}, Backends: []objectstorage.BackendConfig{{ID: "ovh", Driver: "fake", Region: "us-east-1", Namespace: "isolated", Endpoint: "https://s3.example.test", S3Region: "us-east-1", UsageReportsPath: "/var/spool/faas/ovh-usage.json"}}}, func(string) string { return "" }, map[string]objectstorage.Factory{"fake": func(objectstorage.BackendConfig, func(string) string) (objectstorage.Provider, error) {
		return lister, nil
	}})
	if err != nil {
		return Service{}, err
	}
	backend, err := registry.Default("us-east-1")
	if err != nil {
		return Service{}, err
	}
	sizes := make([]int, 0, small+large)
	for range large {
		sizes = append(sizes, largePages*api.MaxObjectS3ListItems)
	}
	for range small {
		sizes = append(sizes, 3)
	}
	names := make([]string, len(sizes))
	for i := range names {
		names[i] = "physical-" + uuid.NewString()
		lister.keys[names[i]] = sizes[i]
	}
	const perApp = 10
	work := make(chan int)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var first error
	for range 8 {
		wg.Go(func() {
			for from := range work {
				if err := seedInventoryApp(t.Context(), st, backend, names[from:min(from+perApp, len(names))]); err != nil {
					mu.Lock()
					first = cmp.Or(first, err)
					mu.Unlock()
				}
			}
		})
	}
	for from := 0; from < len(names); from += perApp {
		work <- from
	}
	close(work)
	wg.Wait()
	return Service{Host: capacityHost{store: st, registry: registry}}, first
}

func seedInventoryApp(ctx context.Context, st *state.PgStore, backend objectstorage.Backend, physical []string) error {
	account, err := st.CreateAccount(ctx, uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		return err
	}
	app, err := st.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "inv-" + uuid.NewString()[:8], Status: state.AppActive})
	if err != nil {
		return err
	}
	for i, name := range physical {
		b, err := st.ReserveObjectBucket(ctx, state.ObjectBucket{ID: uuid.NewString(), AccountID: account.ID, AppID: app.ID, Name: fmt.Sprintf("b%d", i), Scope: "default", PhysicalName: name, BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, Region: "us-east-1"}, len(physical))
		if err != nil {
			return err
		}
		if _, err = st.ClaimObjectBucket(ctx, account.ID, app.ID, b.ID, "create", "provisioning"); err != nil {
			return err
		}
		if err = st.FinishObjectBucket(ctx, b.ID, "create", "ready"); err != nil {
			return err
		}
	}
	return nil
}
