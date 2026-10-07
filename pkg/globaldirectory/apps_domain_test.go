package globaldirectory

// adr: 657
import (
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func exerciseAppsDomainRegistry(t *testing.T, f quotaFixture) {
	t.Helper()
	ctx := t.Context()
	store := f.store
	writer := store.(CellRegistryWriter)
	region := f.request.Shares[0].Region
	cell, err := store.CellByRegion(ctx, region)
	if err != nil {
		t.Fatal(err)
	}
	original, err := Resolve(ctx, store, f.owner.ID, f.request.Slug, region)
	if err != nil {
		t.Fatal(err)
	}
	j, consent := consentFixture(t, f)
	if _, err = j.PrepareRuntimeConsent(ctx, f.owner.ID, f.request.Slug, consent); err != nil {
		t.Fatal(err)
	}
	if _, err = j.ObserveRuntimeConsent(ctx, f.owner.ID, f.request.Slug, consent.ID, state.RegionalRuntimeConsent{Consent: consent, CreatedAt: time.Now().UTC().Truncate(time.Microsecond)}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.quota.ReserveRuntimeQuota(ctx, f.request); err != nil {
		t.Fatal(err)
	}
	if _, err = f.quota.IssueRuntimeQuotaTarget(ctx, f.owner.ID, f.request.Slug, f.request.AllocationID, region); err != nil {
		t.Fatal(err)
	}
	before, err := f.quota.RuntimeQuota(ctx, f.owner.ID, f.request.Slug, f.request.AllocationID)
	if err != nil {
		t.Fatal(err)
	}
	consentBefore, err := j.RuntimeConsent(ctx, f.owner.ID, f.request.Slug, consent.ID)
	if err != nil {
		t.Fatal(err)
	}
	domain := "apps.fsn.example.com"
	cell.APIBase = "https://new-api.example.com"
	updated, err := writer.PutCellRegistry(ctx, cell, &domain)
	if err != nil || updated.Generation != cell.Generation+1 || updated.AppsDomain != domain {
		t.Fatalf("changed: %+v %v", updated, err)
	}
	legacy := updated
	legacy.AppsDomain = ""
	got, err := store.PutCell(ctx, legacy)
	if err != nil || got != updated {
		t.Fatalf("legacy write erased domain: %+v %v", got, err)
	}
	if _, err = Resolve(ctx, store, f.owner.ID, f.request.Slug, region); !errors.Is(err, ErrStaleCell) {
		t.Fatal("old attachment remained resolvable", err)
	}
	if _, err = store.AttachTarget(ctx, f.owner.ID, f.request.Slug, original.Binding); !errors.Is(err, ErrStaleCell) {
		t.Fatal("old probe attached", err)
	}
	if _, err = j.CheckRuntimeConsent(ctx, f.owner.ID, f.request.Slug, consent.ID); err == nil {
		t.Fatal("old consent dispatch allowed")
	}
	after, err := f.quota.RuntimeQuota(ctx, f.owner.ID, f.request.Slug, f.request.AllocationID)
	if err != nil {
		t.Fatal(err)
	}
	consentAfter, err := j.RuntimeConsent(ctx, f.owner.ID, f.request.Slug, consent.ID)
	if err != nil {
		t.Fatal(err)
	}
	marshal := func(value any) string {
		b, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if marshal(before) != marshal(after) || marshal(consentBefore) != marshal(consentAfter) {
		t.Fatal("registry change rewrote original quota or consent")
	}
	assertQuotaConservation(t, f, []string{f.request.AllocationID})
	original.CellGeneration = updated.Generation
	if _, err = store.AttachTarget(ctx, f.owner.ID, f.request.Slug, original.Binding); err != nil {
		t.Fatal(err)
	}
	if _, err = Resolve(ctx, store, f.owner.ID, f.request.Slug, region); err != nil {
		t.Fatal(err)
	}
	// Concurrent older clients must never overwrite an explicitly registered suffix.
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var c Cell
			var err error
			if i%2 == 0 {
				c, err = store.PutCell(ctx, legacy)
			} else {
				c, err = writer.PutCellRegistry(ctx, legacy, &domain)
			}
			if err != nil || c.AppsDomain != domain {
				t.Errorf("concurrent domain erased: %+v %v", c, err)
			}
		}(i)
	}
	wg.Wait()
	empty := ""
	cleared, err := writer.PutCellRegistry(ctx, legacy, &empty)
	if err != nil || cleared.AppsDomain != "" || cleared.Generation != updated.Generation+1 {
		t.Fatalf("clear: %+v %v", cleared, err)
	}
	if _, err = Resolve(ctx, store, f.owner.ID, f.request.Slug, region); !errors.Is(err, ErrStaleCell) {
		t.Fatal("clear retained attachment", err)
	}
	listed, err := store.ListCells(ctx)
	if err != nil || len(listed) != 1 || listed[0] != cleared {
		t.Fatal("registry roundtrip", listed, err)
	}
}

func TestRegionalAppsDomainMemoryContract(t *testing.T) {
	exerciseAppsDomainRegistry(t, memoryQuotaFixture(t, false, 1))
}
