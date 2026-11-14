// adr: 20261102123523 — a production database's previews setting gives each
// PR preview environment one empty development database per source, outside
// the plan's database limit, which is purged with the environment and can be
// neither restored from, adopted by name, nor given previews of its own.
package managedpostgres

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type previewFixture struct {
	service  *Service
	store    *MemoryStore
	provider *fakeProvider
	limit    *int
	clock    *time.Time
}

func newPreviewFixture(t *testing.T) previewFixture {
	t.Helper()
	return newPreviewFixtureWith(t, nil)
}

// newPreviewFixtureWith is newPreviewFixture with the registry configuration
// changed by mutate, such as a fleet policy.
func newPreviewFixtureWith(t *testing.T, mutate func(*Config)) previewFixture {
	t.Helper()
	clock := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	limit := 1
	provider := &fakeProvider{capabilities: testCapabilities(), provisionStatus: ProviderStatusReady, inspectStatus: ProviderStatusReady, deleteDone: true}
	store := NewMemoryStore()
	sequence := 0
	service, err := NewService(testRegistry(t, provider, mutate), store, ServiceOptions{
		PollInterval:           time.Second,
		ProvisioningEnabled:    func() bool { return true },
		Now:                    func() time.Time { return clock },
		NewID:                  func() string { sequence++; return fmt.Sprintf("database-%d", sequence) },
		NewLeaseToken:          func() string { sequence++; return fmt.Sprintf("lease-%d", sequence) },
		MaxDatabasesPerAccount: func(context.Context, string) (int, error) { return limit, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	return previewFixture{service: service, store: store, provider: provider, limit: &limit, clock: &clock}
}

// source creates a ready database with empty previews.
func (f previewFixture) source(t *testing.T, name string) Database {
	t.Helper()
	ctx := context.Background()
	database, err := f.service.Create(ctx, CreateRequest{AccountID: "account-a", Name: name, Spec: testSpec()})
	if err != nil || database.State != StateReady {
		t.Fatalf("Create = %+v, %v", database, err)
	}
	database, err = f.service.SetPreviews(ctx, "account-a", database.ID, PreviewsEmpty)
	if err != nil || database.Previews != PreviewsEmpty {
		t.Fatalf("SetPreviews = %+v, %v", database, err)
	}
	return database
}

func previewRequest(source Database, environmentID string, prNumber int) PreviewDatabaseRequest {
	return PreviewDatabaseRequest{AccountID: "account-a", Owner: PreviewOwner{SourceDatabaseID: source.ID, EnvironmentID: environmentID, PRNumber: prNumber},
		StorageLimitBytes: api.ManagedPostgresPreviewStorageLimitBytes, Limit: 10}
}

func TestPreviewDatabaseName(t *testing.T) {
	owner := PreviewOwner{SourceDatabaseID: "source", EnvironmentID: "project-a", PRNumber: 7}
	name := PreviewDatabaseName("orders", owner)
	if !ValidName(name) || !strings.HasPrefix(name, "pr-7-orders-") || len(name) != len("pr-7-orders-")+6 {
		t.Fatalf("name = %q", name)
	}
	if other := PreviewDatabaseName("orders", PreviewOwner{SourceDatabaseID: "source", EnvironmentID: "project-b", PRNumber: 7}); other == name {
		t.Fatalf("two environments share the name %q", name)
	}
	long := PreviewDatabaseName("a"+strings.Repeat("b-", 40), PreviewOwner{SourceDatabaseID: "source", EnvironmentID: "project-a", PRNumber: 123456})
	if !ValidName(long) || len(long) != 63 || strings.Contains(long, "--") {
		t.Fatalf("long name = %q (%d)", long, len(long))
	}
}

func TestPreviewsSetting(t *testing.T) {
	f := newPreviewFixture(t)
	ctx := context.Background()
	source := f.source(t, "orders")
	if got, err := f.service.Get(ctx, "account-a", source.ID); err != nil || got.Previews != PreviewsEmpty {
		t.Fatalf("Get = %+v, %v", got, err)
	}
	for _, mode := range []PreviewMode{"", "branch"} {
		if _, err := f.service.SetPreviews(ctx, "account-a", source.ID, mode); !errors.Is(err, ErrInvalid) {
			t.Fatalf("SetPreviews(%q) = %v, want ErrInvalid", mode, err)
		}
	}
	off, err := f.service.SetPreviews(ctx, "account-a", source.ID, PreviewsOff)
	if err != nil || off.Previews != "" {
		t.Fatalf("SetPreviews(off) = %+v, %v; off reads as empty", off, err)
	}
	if _, err := f.service.SetPreviews(ctx, "account-b", source.ID, PreviewsEmpty); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another account's database = %v, want ErrNotFound", err)
	}
}

func TestCopyPreviewRestoresTheSourceAsOfSetup(t *testing.T) {
	f := newPreviewFixture(t)
	ctx := context.Background()
	source := f.source(t, "orders")
	source, err := f.service.SetPreviews(ctx, "account-a", source.ID, PreviewsCopy)
	if err != nil || source.Previews != PreviewsCopy {
		t.Fatalf("SetPreviews(copy) = %+v, %v", source, err)
	}
	// No database has history from before it existed: a copy set up in the
	// source's first second waits for the next attempt.
	if _, err := f.service.EnsurePreviewDatabase(ctx, previewRequest(source, "project-a", 7)); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("copy of a database created this second = %v, want ErrUnavailable", err)
	}
	*f.clock = f.clock.Add(time.Minute)

	copied, err := f.service.EnsurePreviewDatabase(ctx, previewRequest(source, "project-a", 7))
	if err != nil || copied.State != StateReady {
		t.Fatalf("EnsurePreviewDatabase = %+v, %v", copied, err)
	}
	want := source.Spec
	want.Class, want.Availability, want.ScaleToZero = ClassDevelopment, AvailabilitySingleZone, true
	at := f.clock.Add(-time.Second)
	if copied.Spec != want || copied.BackendID != source.BackendID || copied.RestoreSourceDatabaseID != source.ID ||
		!copied.RestorePointInTime.Equal(at) || copied.Preview.SourceDatabaseID != source.ID || !copied.RestoreSlotUntil.IsZero() {
		t.Fatalf("copy = %+v; want a development restore of the source as of a second before setup", copied)
	}
	if f.provider.restoreCalls != 1 || f.provider.lastRestore.SourceResourceID != source.ProviderResourceID || !f.provider.lastRestore.PointInTime.Equal(at) {
		t.Fatalf("provider restore = %+v (%d calls)", f.provider.lastRestore, f.provider.restoreCalls)
	}
	// Later pushes keep the copy and what was written to it.
	again, err := f.service.EnsurePreviewDatabase(ctx, previewRequest(source, "project-a", 7))
	if err != nil || again.ID != copied.ID || f.provider.restoreCalls != 1 {
		t.Fatalf("second EnsurePreviewDatabase = %+v, %v; restores %d", again, err, f.provider.restoreCalls)
	}
	// Turning copy off applies to the next environment.
	if _, err := f.service.SetPreviews(ctx, "account-a", source.ID, PreviewsEmpty); err != nil {
		t.Fatal(err)
	}
	empty, err := f.service.EnsurePreviewDatabase(ctx, previewRequest(source, "project-a", 8))
	if err != nil || empty.RestoreSourceDatabaseID != "" || f.provider.restoreCalls != 1 {
		t.Fatalf("after copy is turned off = %+v, %v", empty, err)
	}

	// A database without a restore window cannot be copied.
	*f.limit = 2
	spec := testSpec()
	spec.RestoreWindowSeconds = 0
	ledger, err := f.service.Create(ctx, CreateRequest{AccountID: "account-a", Name: "ledger", Spec: spec})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.SetPreviews(ctx, "account-a", ledger.ID, PreviewsCopy); !errors.Is(err, ErrPreviewCopyUnsupported) || !errors.Is(err, ErrInvalid) {
		t.Fatalf("SetPreviews(copy) without a restore window = %v, want ErrPreviewCopyUnsupported", err)
	}
}

func TestEnsurePreviewDatabaseSetsUpOnePerEnvironmentOutsideTheLimit(t *testing.T) {
	f := newPreviewFixture(t)
	ctx := context.Background()
	source := f.source(t, "orders")

	preview, err := f.service.EnsurePreviewDatabase(ctx, previewRequest(source, "project-a", 7))
	if err != nil || preview.State != StateReady {
		t.Fatalf("EnsurePreviewDatabase = %+v, %v", preview, err)
	}
	want := Spec{Region: source.Spec.Region, PostgresMajor: source.Spec.PostgresMajor, Class: ClassDevelopment,
		Availability: AvailabilitySingleZone, ScaleToZero: true, StorageLimitBytes: api.ManagedPostgresPreviewStorageLimitBytes}
	owner := PreviewOwner{SourceDatabaseID: source.ID, EnvironmentID: "project-a", PRNumber: 7}
	if preview.Spec != want || preview.Preview != owner || preview.Name != PreviewDatabaseName("orders", owner) || preview.Previews != "" {
		t.Fatalf("preview database = %+v", preview)
	}

	again, err := f.service.EnsurePreviewDatabase(ctx, previewRequest(source, "project-a", 7))
	if err != nil || again.ID != preview.ID || f.provider.provisionCalls != 2 {
		t.Fatalf("second EnsurePreviewDatabase = %+v, %v; provisions %d, want the same database", again, err, f.provider.provisionCalls)
	}
	otherPR, err := f.service.EnsurePreviewDatabase(ctx, previewRequest(source, "project-a", 8))
	if err != nil || otherPR.ID == preview.ID {
		t.Fatalf("another pull request = %+v, %v", otherPR, err)
	}
	otherProject, err := f.service.EnsurePreviewDatabase(ctx, previewRequest(source, "project-b", 7))
	if err != nil || otherProject.ID == preview.ID || otherProject.ID == otherPR.ID {
		t.Fatalf("another project = %+v, %v", otherProject, err)
	}

	// Three preview databases beside a one-database plan's source: they are
	// outside the limit, which still refuses a second customer database.
	if _, err := f.service.Create(ctx, CreateRequest{AccountID: "account-a", Name: "invoices", Spec: testSpec()}); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("create past the limit = %v", err)
	}
	*f.limit = 2
	if _, err := f.service.Create(ctx, CreateRequest{AccountID: "account-a", Name: "invoices", Spec: testSpec()}); err != nil {
		t.Fatalf("create inside the limit beside three preview databases = %v", err)
	}
	if count, err := f.store.CountSubjectDatabases(ctx, "account-a"); err != nil || count != 2 {
		t.Fatalf("CountSubjectDatabases = %d, %v, want the two customer databases", count, err)
	}

	listed, err := f.service.PreviewDatabases(ctx, "account-a", "project-a", 7)
	if err != nil || len(listed) != 1 || listed[0].ID != preview.ID {
		t.Fatalf("PreviewDatabases = %+v, %v", listed, err)
	}
}

func TestEnsurePreviewDatabaseRefusals(t *testing.T) {
	f := newPreviewFixture(t)
	ctx := context.Background()
	*f.limit = 3
	source := f.source(t, "orders")

	off, err := f.service.Create(ctx, CreateRequest{AccountID: "account-a", Name: "ledger", Spec: testSpec()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.EnsurePreviewDatabase(ctx, previewRequest(off, "project-a", 7)); !errors.Is(err, ErrPreviewsOff) {
		t.Fatalf("previews off = %v, want ErrPreviewsOff", err)
	}

	limited := previewRequest(source, "project-a", 7)
	limited.Limit = 1
	if _, err := f.service.EnsurePreviewDatabase(ctx, limited); err != nil {
		t.Fatal(err)
	}
	limited.Owner.PRNumber = 8
	_, err = f.service.EnsurePreviewDatabase(ctx, limited)
	if !errors.Is(err, ErrPreviewDatabaseLimit) || !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("past the preview limit = %v", err)
	}
	if status, code, _ := QuotaProblem(err); status != 403 || code != "managed_postgres_preview_database_limit" {
		t.Fatalf("limit problem = %d %s", status, code)
	}

	taken := previewRequest(source, "project-b", 9)
	if _, err := f.service.Create(ctx, CreateRequest{AccountID: "account-a", Name: PreviewDatabaseName("orders", taken.Owner), Spec: testSpec()}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.EnsurePreviewDatabase(ctx, taken); !errors.Is(err, ErrConflict) {
		t.Fatalf("name taken by a customer database = %v, want ErrConflict", err)
	}

	for _, request := range []PreviewDatabaseRequest{
		{AccountID: "account-a", Owner: PreviewOwner{SourceDatabaseID: source.ID, EnvironmentID: "project a", PRNumber: 7}, StorageLimitBytes: 1 << 30},
		{AccountID: "account-a", Owner: PreviewOwner{SourceDatabaseID: source.ID, EnvironmentID: "project-a"}, StorageLimitBytes: 1 << 30},
		{AccountID: "account-a", Owner: PreviewOwner{SourceDatabaseID: source.ID, EnvironmentID: "project-a", PRNumber: 7}},
	} {
		if _, err := f.service.EnsurePreviewDatabase(ctx, request); !errors.Is(err, ErrInvalid) {
			t.Fatalf("EnsurePreviewDatabase(%+v) = %v, want ErrInvalid", request, err)
		}
	}
}

// ADR-20261102123523 amendment 2026-11-10: one account's preview databases
// stop at its share of the fleet cap even when its plan allows more, so a
// few accounts cannot fill the fleet with disposable databases.
func TestPreviewDatabasesStopAtTheAccountsFleetShare(t *testing.T) {
	noReserve := 0
	f := newPreviewFixtureWith(t, func(c *Config) {
		c.Fleet = FleetConfig{MaxDatabases: 40, MaxMonthlyCostMillicents: 1_000_000, PaidReserveDatabases: &noReserve}
	})
	ctx := context.Background()
	source := f.source(t, "orders")
	if share := f.service.registry.Fleet().PreviewShare(); share != 2 {
		t.Fatalf("PreviewShare = %d, want 5%% of 40", share)
	}
	for pr := 1; pr <= 2; pr++ {
		if _, err := f.service.EnsurePreviewDatabase(ctx, previewRequest(source, "project-a", pr)); err != nil {
			t.Fatalf("preview %d inside the share: %v", pr, err)
		}
	}
	_, err := f.service.EnsurePreviewDatabase(ctx, previewRequest(source, "project-a", 3))
	if !errors.Is(err, ErrPreviewDatabaseLimit) {
		t.Fatalf("preview past the fleet share with a plan allowance of 10 = %v, want ErrPreviewDatabaseLimit", err)
	}
	if (FleetPolicy{}).PreviewShare() != 0 || (FleetPolicy{MaxDatabases: 3}).PreviewShare() != 1 {
		t.Fatal("PreviewShare: no cap means no share, and a small fleet still allows one")
	}
}

// Previews never take the paid reserve: inside it they are refused while a
// paid customer's production database is still admitted.
func TestPreviewDatabasesStayOutOfThePaidReserve(t *testing.T) {
	reserve := 38
	f := newPreviewFixtureWith(t, func(c *Config) {
		c.Fleet = FleetConfig{MaxDatabases: 40, MaxMonthlyCostMillicents: 1_000_000, PaidReserveDatabases: &reserve}
	})
	*f.limit = 2
	ctx := context.Background()
	source := f.source(t, "orders")
	if _, err := f.service.EnsurePreviewDatabase(ctx, previewRequest(source, "project-a", 1)); err != nil {
		t.Fatalf("preview outside the reserve: %v", err)
	}
	_, err := f.service.EnsurePreviewDatabase(ctx, previewRequest(source, "project-a", 2))
	if !errors.Is(err, ErrFleetPaused) || errors.Is(err, ErrPreviewDatabaseLimit) {
		t.Fatalf("preview inside the paid reserve = %v, want ErrFleetPaused", err)
	}
	if _, err := f.service.Create(ctx, CreateRequest{AccountID: "account-a", Name: "invoices", Spec: testSpec()}); err != nil {
		t.Fatalf("a paid production create inside the reserve = %v, want it admitted", err)
	}
}

func TestPreviewDatabaseIsNotACustomerDatabaseToChange(t *testing.T) {
	f := newPreviewFixture(t)
	ctx := context.Background()
	*f.limit = 3
	source := f.source(t, "orders")
	preview, err := f.service.EnsurePreviewDatabase(ctx, previewRequest(source, "project-a", 7))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.SetPreviews(ctx, "account-a", preview.ID, PreviewsEmpty); !errors.Is(err, ErrPreviewDatabase) {
		t.Fatalf("previews of a preview database = %v", err)
	}
	if _, err := f.service.EnsurePreviewDatabase(ctx, previewRequest(preview, "project-a", 7)); !errors.Is(err, ErrPreviewDatabase) {
		t.Fatalf("a preview database as a source = %v", err)
	}
	if _, err := f.service.Restore(ctx, RestoreDatabaseRequest{AccountID: "account-a", SourceDatabaseID: preview.ID, Name: "orders-copy", PointInTime: preview.CreatedAt}); !errors.Is(err, ErrPreviewDatabase) {
		t.Fatalf("restore from a preview database = %v", err)
	}
	if _, err := f.service.Create(ctx, CreateRequest{AccountID: "account-a", Name: preview.Name, Spec: preview.Spec}); !errors.Is(err, ErrConflict) {
		t.Fatalf("create adopting a preview database's name = %v", err)
	}
}

func TestDeletePreviewDatabasePurgesIt(t *testing.T) {
	f := newPreviewFixture(t)
	ctx := context.Background()
	source := f.source(t, "orders")
	preview, err := f.service.EnsurePreviewDatabase(ctx, previewRequest(source, "project-a", 7))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.DeletePreviewDatabase(ctx, "account-a", source.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("DeletePreviewDatabase of the source = %v, want ErrConflict", err)
	}
	deleted, err := f.service.DeletePreviewDatabase(ctx, "account-a", preview.ID)
	if err != nil || deleted.State != StateDeleted || !deleted.PurgeAfter.IsZero() || f.provider.lastDelete.ProviderResourceID != preview.ProviderResourceID {
		t.Fatalf("DeletePreviewDatabase = %+v, %v; provider delete %+v", deleted, err, f.provider.lastDelete)
	}
	if listed, err := f.service.PreviewDatabases(ctx, "account-a", "project-a", 7); err != nil || len(listed) != 0 {
		t.Fatalf("PreviewDatabases after the purge = %+v, %v", listed, err)
	}
	again, err := f.service.EnsurePreviewDatabase(ctx, previewRequest(source, "project-a", 7))
	if err != nil || again.ID == preview.ID || again.Name != preview.Name || again.State != StateReady {
		t.Fatalf("set up again after the purge = %+v, %v", again, err)
	}
	other, err := f.service.EnsurePreviewDatabase(ctx, previewRequest(source, "project-a", 8))
	if err != nil {
		t.Fatal(err)
	}
	// The sweep's pages hold the undeleted preview databases only, in ID order.
	var paged []string
	for after := ""; ; {
		page, err := f.service.PreviewDatabasePage(ctx, after, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		paged, after = append(paged, page[0].ID), page[0].ID
	}
	want := []string{again.ID, other.ID}
	slices.Sort(want)
	if !slices.Equal(paged, want) {
		t.Fatalf("preview database pages = %v, want %v", paged, want)
	}
}
