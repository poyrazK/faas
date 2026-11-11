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
	"slices"
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
// FAAS_INVENTORY_CAPACITY=1 (about ten minutes):
//
//	FAAS_INVENTORY_CAPACITY=1 DATABASE_URL=... go test -run '^TestInventorySweepCapacity$' -v ./pkg/apid/object
//
// Results: docs/ops/evidence/20261110-object-inventory-capacity/REPORT.md.
func TestInventorySweepCapacity(t *testing.T) {
	if os.Getenv("FAAS_INVENTORY_CAPACITY") == "" {
		t.Skip("set FAAS_INVENTORY_CAPACITY=1 to measure inventory sweep capacity (wall-clock, ~10 min)")
	}
	scenarios := []struct {
		name         string
		latency      time.Duration
		small, large int
		largePages   int
		sweeps       int
		// listed marks every bucket listed 10 minutes ago with its key
		// count, as in production, where only a new (empty) bucket has no
		// inventory. Without it the large buckets' first listing is of
		// unknown size.
		listed bool
	}{
		{name: "small-100ms", latency: 100 * time.Millisecond, small: 4000, sweeps: 1},
		{name: "small-200ms", latency: 200 * time.Millisecond, small: 3000, sweeps: 1},
		{name: "small-300ms", latency: 300 * time.Millisecond, small: 2000, sweeps: 1},
		{name: "mixed-200ms", latency: 200 * time.Millisecond, small: 2000, large: 16, largePages: 500, sweeps: 2},
		{name: "steady-200ms", latency: 200 * time.Millisecond, small: 2000, large: 16, largePages: 500, sweeps: 2, listed: true},
	}
	t.Run("quiet-200ms", testInventorySweepQuietCapacity)
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			pool := pgtest.OpenMigrated(t)
			st := state.NewPgStore(pool)
			lister := &latencyLister{latency: sc.latency, keys: map[string]int{}}
			svc, err := seedInventoryCapacity(t, st, lister, sc.small, sc.large, sc.largePages, false)
			if err != nil {
				t.Fatal(err)
			}
			if sc.listed {
				var large []string
				for name, keys := range lister.keys {
					if keys > 3 {
						large = append(large, name)
					}
				}
				if _, err = pool.Exec(t.Context(), `INSERT INTO object_storage_bucket_usage (bucket_id, baseline_bytes, baseline_keys, observed_bytes, observed_keys, observed_at, attempt_at)
SELECT b.id, k, k, k, k, now() - interval '10 minutes', now() - interval '10 minutes'
FROM object_buckets b CROSS JOIN LATERAL (SELECT CASE WHEN b.physical_name = ANY($1::text[]) THEN $2::bigint ELSE 3 END AS k) n
ON CONFLICT (bucket_id) DO UPDATE SET baseline_bytes = EXCLUDED.baseline_bytes, baseline_keys = EXCLUDED.baseline_keys,
observed_bytes = EXCLUDED.observed_bytes, observed_keys = EXCLUDED.observed_keys, observed_at = EXCLUDED.observed_at, attempt_at = EXCLUDED.attempt_at`,
					large, int64(sc.largePages*api.MaxObjectS3ListItems)); err != nil {
					t.Fatal(err)
				}
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
	mu      sync.Mutex
	listed  map[string]int // physical bucket name → pages listed, when set
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
	p.mu.Lock()
	if p.listed != nil {
		p.listed[physical]++
	}
	p.mu.Unlock()
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
// gatewaySafety configures gateway-safety accounting, which quiet buckets
// need (ADR-20261110151628 §2).
func seedInventoryCapacity(t *testing.T, st *state.PgStore, lister *latencyLister, small, large, largePages int, gatewaySafety bool) (Service, error) {
	policy := api.ObjectStoragePolicy{MaxAccountBytes: 1 << 40, MaxBucketBytes: 1 << 40, MaxAccountKeys: api.MaxObjectStorageAccountKeys, MaxMonthlyCostMillicents: 1 << 40, MaxMonthlyRequests: 1 << 40, MaxMonthlyEgressBytes: 1 << 40, MaxMonthlyAuthorizations: 1 << 40, MaxReportAgeSeconds: 3600}
	config := objectstorage.Config{Accounting: &policy, DefaultRegion: "us-east-1", Defaults: map[string]string{"us-east-1": "ovh"}, Backends: []objectstorage.BackendConfig{{ID: "ovh", Driver: "fake", Region: "us-east-1", Namespace: "isolated", Endpoint: "https://s3.example.test", S3Region: "us-east-1", UsageReportsPath: "/var/spool/faas/ovh-usage.json"}}}
	if gatewaySafety {
		since := time.Now().UTC().Add(-48 * time.Hour)
		policy.AccountingMode, policy.GatewayMeteringSince, policy.MaxMonthlyCostMillicents = api.ObjectStorageGatewaySafetyV1, &since, 0
		config.PublicEndpoint, config.Transfer = "https://s3.gregale.dev", objectstorage.ObjectTransferConfig{Profile: "proxied"}
	}
	registry, err := objectstorage.NewRegistry(config, func(string) string { return "" }, map[string]objectstorage.Factory{"fake": func(objectstorage.BackendConfig, func(string) string) (objectstorage.Provider, error) {
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

// testInventorySweepQuietCapacity measures the sweep with object_inventory_quiet
// on (ADR-20261110151628 step 5): 20,000 quiet one-page buckets listed two
// hours ago, 1,000 active ones (written after their listing ten minutes ago)
// and 1,000 quiet ones due for their daily verification. Only the last two
// groups should be listed.
func testInventorySweepQuietCapacity(t *testing.T) {
	const quiet, active, verify = 20000, 1000, 1000
	const latency = 200 * time.Millisecond
	pool := pgtest.OpenMigrated(t)
	st := state.NewPgStore(pool)
	lister := &latencyLister{latency: latency, keys: map[string]int{}}
	svc, err := seedInventoryCapacity(t, st, lister, quiet+active+verify, 0, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(lister.keys))
	for name := range lister.keys {
		names = append(names, name)
	}
	slices.Sort(names)
	class := map[string]string{}
	for i, name := range names {
		switch {
		case i < active:
			class[name] = "active"
		case i < active+verify:
			class[name] = "verify"
		default:
			class[name] = "quiet"
		}
	}
	ctx := t.Context()
	if _, err = pool.Exec(ctx, `INSERT INTO object_storage_bucket_usage (bucket_id, baseline_bytes, baseline_keys, observed_bytes, observed_keys, observed_at, attempt_at)
SELECT b.id, 3, 3, 3, 3, now() - a.age, now() - a.age FROM object_buckets b
CROSS JOIN LATERAL (SELECT CASE WHEN b.physical_name = ANY($1::text[]) THEN interval '10 minutes'
  WHEN b.physical_name = ANY($2::text[]) THEN interval '23 hours 30 minutes' ELSE interval '2 hours' END AS age) a
ON CONFLICT (bucket_id) DO UPDATE SET observed_at = EXCLUDED.observed_at, attempt_at = EXCLUDED.attempt_at,
observed_bytes = 3, observed_keys = 3, baseline_bytes = 3, baseline_keys = 3`, names[:active], names[active:active+verify]); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO object_bucket_activity (bucket_id, mutated_at, active_since)
SELECT id, now() - interval '1 minute', now() - interval '1 minute' FROM object_buckets WHERE physical_name = ANY($1::text[])`, names[:active]); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO runtime_config_entries (id, config_key, scope, scope_id, desired_value, effective_value, version, apply_mode, status)
VALUES (gen_random_uuid(), $1, 'global', '', '"on"', '"on"', 1, 'hot', 'applied')`, api.ObjectInventoryQuietConfigKey); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	due, err := st.DueObjectInventoryQueue(ctx, 100, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("due query over %d buckets: %s (%d rows)", len(names), time.Since(start).Round(time.Microsecond), len(due))
	start = time.Now()
	if due, err = st.DueObjectInventoryQueue(ctx, 100, nil, false); err != nil {
		t.Fatal(err)
	}
	t.Logf("due query without quiet buckets: %s (%d rows)", time.Since(start).Round(time.Microsecond), len(due))
	start = time.Now()
	backlog, err := st.ObjectInventoryBacklog(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("backlog query: %s: due %d, oldest active %s, oldest quiet %s", time.Since(start).Round(time.Microsecond), backlog.DueBuckets, backlog.OldestObservationAge.Round(time.Second), backlog.OldestQuietObservationAge.Round(time.Second))
	if backlog.DueBuckets != active+verify {
		t.Fatalf("due %d, want %d", backlog.DueBuckets, active+verify)
	}
	lister.mu.Lock()
	lister.listed = map[string]int{}
	lister.mu.Unlock()
	cycle := time.Now()
	for sweep := 1; sweep <= 6; sweep++ {
		start := time.Now()
		more, err := svc.reconcileObjectInventories(ctx, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		lister.mu.Lock()
		byClass := map[string]int{}
		for name := range lister.listed {
			byClass[class[name]]++
		}
		lister.mu.Unlock()
		t.Logf("sweep %d: %s, listed so far %v, more %v", sweep, time.Since(start).Round(time.Millisecond), byClass, more)
		if !more {
			break
		}
	}
	lister.mu.Lock()
	defer lister.mu.Unlock()
	byClass := map[string]int{}
	for name := range lister.listed {
		byClass[class[name]]++
	}
	t.Logf("cycle: %s for %d buckets of %d", time.Since(cycle).Round(time.Millisecond), len(lister.listed), len(names))
	if byClass["quiet"] != 0 || byClass["active"] != active || byClass["verify"] != verify {
		t.Fatalf("listed %v, want every active and verifying bucket and no quiet one", byClass)
	}
}
