// adr: 20261030100835

package object

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

// pagedLister lists one-byte objects three to a page, from StartAfter or an
// index cursor. Every failEvery-th request fails; onPage runs after each
// page it serves.
type pagedLister struct {
	objectstorage.Provider
	mu               sync.Mutex
	keys             []string
	failEvery        int
	ignoreStartAfter bool
	onPage           func(request int)
	requests         []objectstorage.ObjectListRequest
}

func newPagedLister(n int) *pagedLister {
	p := &pagedLister{}
	for i := range n {
		p.keys = append(p.keys, fmt.Sprintf("k%02d", i))
	}
	return p
}

func (p *pagedLister) ListObjectsV2(_ context.Context, _ string, r objectstorage.ObjectListRequest) (objectstorage.ObjectPage, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = append(p.requests, r)
	n := len(p.requests)
	if p.failEvery > 0 && n%p.failEvery == 0 {
		return objectstorage.ObjectPage{}, objectstorage.ErrUnavailable
	}
	start := 0
	if r.Cursor != "" {
		start, _ = strconv.Atoi(r.Cursor)
	} else if !p.ignoreStartAfter {
		start, _ = slices.BinarySearch(p.keys, r.StartAfter+"\x00")
	}
	end := min(start+3, len(p.keys))
	var page objectstorage.ObjectPage
	for _, k := range p.keys[start:end] {
		page.Items = append(page.Items, objectstorage.Object{Key: k, Size: 1})
	}
	if end < len(p.keys) {
		page.NextCursor = strconv.Itoa(end)
	}
	if p.onPage != nil {
		p.onPage(n)
	}
	return page, nil
}

func TestResumeObjectInventoryResumesAfterLastKey(t *testing.T) {
	p := newPagedLister(10)
	ctx, cancel := context.WithCancel(t.Context())
	p.onPage = func(n int) {
		if n == 2 {
			cancel()
		}
	}
	var scan state.ObjectInventoryScan
	if err := ResumeObjectInventory(ctx, p, "physical", &scan, nil); err != nil {
		t.Fatal("running out of time is not an error", err)
	}
	if scan.Pages != 2 || scan.Keys != 6 || scan.After != "k05" || scan.Complete {
		t.Fatalf("first attempt = %+v", scan)
	}
	p.onPage = nil
	if err := ResumeObjectInventory(t.Context(), p, "physical", &scan, nil); err != nil {
		t.Fatal(err)
	}
	if scan.Pages != 4 || scan.Keys != 10 || scan.Bytes != 10 || scan.After != "k09" || !scan.Complete {
		t.Fatalf("second attempt = %+v", scan)
	}
	if r := p.requests[2]; r.StartAfter != "k05" || r.Cursor != "" {
		t.Fatalf("second attempt did not start after the last listed key: %+v", r)
	}
	if r := p.requests[3]; r.StartAfter != "k05" || r.Cursor == "" {
		t.Fatalf("a listing changed its query between pages: %+v", r)
	}
}

func TestResumeObjectInventoryKeepsPagesBeforeAFailure(t *testing.T) {
	p := newPagedLister(10)
	p.failEvery = 3
	var scan state.ObjectInventoryScan
	if err := ResumeObjectInventory(t.Context(), p, "physical", &scan, nil); !errors.Is(err, objectstorage.ErrUnavailable) {
		t.Fatal(err)
	}
	if scan.Pages != 2 || scan.Keys != 6 || scan.After != "k05" {
		t.Fatalf("pages before the failure were lost: %+v", scan)
	}
}

func TestResumeObjectInventoryRefusesKeysOutOfOrderAcrossAttempts(t *testing.T) {
	p := newPagedLister(10)
	p.ignoreStartAfter = true
	scan := state.ObjectInventoryScan{After: "k05", Keys: 6, Bytes: 6, Pages: 2}
	want := scan
	if err := ResumeObjectInventory(t.Context(), p, "physical", &scan, nil); !errors.Is(err, objectstorage.ErrInvalid) {
		t.Fatal(err)
	}
	if scan != want {
		t.Fatalf("a refused page changed the listing: %+v", scan)
	}
}

func TestResumeObjectInventoryStopsAtThePageBudget(t *testing.T) {
	p := newPagedLister(10)
	scan := state.ObjectInventoryScan{After: "k05", Pages: api.ObjectStorageInventoryMaxPages}
	if err := ResumeObjectInventory(t.Context(), p, "physical", &scan, nil); !errors.Is(err, objectstorage.ErrUnavailable) || len(p.requests) != 0 {
		t.Fatal(err, len(p.requests))
	}
}

// TestInventorySweepResumesLargeBucket lists a bucket over three sweeps. Each
// attempt keeps what it listed, the bucket is due again at once, only the
// first attempt renews the time deletions yield to, and the listing publishes
// as of its start.
func TestInventorySweepResumesLargeBucket(t *testing.T) {
	st := state.NewMemStore()
	provider := newPagedLister(16)
	provider.failEvery = 3
	policy := api.ObjectStoragePolicy{MaxAccountBytes: 1000, MaxBucketBytes: 1000, MaxAccountKeys: 100, MaxMonthlyCostMillicents: 100, MaxMonthlyRequests: 1000, MaxMonthlyEgressBytes: 1000, MaxMonthlyAuthorizations: 1000, MaxReportAgeSeconds: 3600}
	registry, err := objectstorage.NewRegistry(objectstorage.Config{Accounting: &policy, DefaultRegion: "us-east-1", Defaults: map[string]string{"us-east-1": "external"}, Backends: []objectstorage.BackendConfig{{ID: "external", Driver: "fake", Region: "us-east-1", Namespace: "isolated", Endpoint: "https://s3.example.test", S3Region: "us-east-1", UsageReportsPath: "/var/spool/faas/external-usage.json"}}}, func(string) string { return "" }, map[string]objectstorage.Factory{"fake": func(objectstorage.BackendConfig, func(string) string) (objectstorage.Provider, error) {
		return provider, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	backend, err := registry.Default("us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	account, err := st.CreateAccount(t.Context(), uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := st.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "resume-" + uuid.NewString()[:8], Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	b, err := st.ReserveObjectBucket(t.Context(), state.ObjectBucket{ID: uuid.NewString(), AccountID: account.ID, AppID: app.ID, Name: "large", Scope: "default", PhysicalName: "physical-large", BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, Region: "us-east-1"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.ClaimObjectBucket(t.Context(), account.ID, app.ID, b.ID, "create", "provisioning"); err != nil {
		t.Fatal(err)
	}
	if err = st.FinishObjectBucket(t.Context(), b.ID, "create", "ready"); err != nil {
		t.Fatal(err)
	}
	usage := func() state.ObjectBucketUsage {
		t.Helper()
		s, err := st.ObjectUsage(t.Context(), account.ID, time.Now())
		if err != nil || len(s.Buckets) != 1 {
			t.Fatal(s, err)
		}
		return s.Buckets[0]
	}
	svc := Service{Host: receiptRecoveryHost{store: st, registry: registry}}
	var started time.Time
	for sweep, want := range []struct {
		outcome string
		pages   int
	}{{"failed", 2}, {"failed", 4}, {"success", 0}} {
		var outcomes []string
		if err = svc.ReconcileObjectInventories(t.Context(), func(o string) { outcomes = append(outcomes, o) }); err != nil {
			t.Fatal(err)
		}
		u := usage()
		if !slices.Equal(outcomes, []string{want.outcome}) || u.InventoryScan.Pages != want.pages || u.Token != "" {
			t.Fatalf("sweep %d: outcomes %v, listing %+v, token %q", sweep+1, outcomes, u.InventoryScan, u.Token)
		}
		if sweep == 0 {
			started = u.InventoryScan.StartedAt
		}
		if !u.AttemptAt.Equal(started) {
			t.Fatalf("sweep %d renewed the attempt time deletions yield to", sweep+1)
		}
	}
	if u := usage(); u.ObservedBytes != 16 || u.ObservedKeys != 16 || !u.ObservedAt.Equal(started) {
		t.Fatalf("published %d bytes, %d keys at %v; want 16, 16 at %v", u.ObservedBytes, u.ObservedKeys, u.ObservedAt, started)
	}
}
