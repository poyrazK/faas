package financialtest

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state"
)

type financialTestStore interface {
	state.Store
	state.FinancialStore
	state.JobStore
	state.JobUsageAppender
}

// adr: 530 — canonical usage and retained evidence must agree across stores.
func TestFinancialStores(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store financialTestStore = state.NewMemStore()
			if backend == "postgres" {
				pg, _, _ := financialPostgres(t)
				store = pg
			}
			t.Run("replay_retention_and_snapshot", func(t *testing.T) { financialRetentionSuite(t, store) })
			t.Run("immutable_prices", func(t *testing.T) { financialPriceSuite(t, store) })
			t.Run("job_identity", func(t *testing.T) { financialJobSuite(t, store) })
			t.Run("captured_contracts_and_aggregation", func(t *testing.T) { financialContractSuite(t, store) })
			t.Run("sampling_coverage", func(t *testing.T) { financialSamplingSuite(t, store) })
			t.Run("adjustment_lineage", func(t *testing.T) { financialAdjustmentSuite(t, store) })
		})
	}
}

func financialAccount(t *testing.T, store state.Store) state.Account {
	t.Helper()
	a, err := store.CreateAccount(t.Context(), uuid.NewString()+"@financial.example", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func financialPeriod() (time.Time, time.Time) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	return start, start.AddDate(0, 1, 0)
}

func financialRows(t *testing.T, store state.FinancialStore, account string, head int64) []state.FinancialUsageRecord {
	t.Helper()
	start, end := financialPeriod()
	var all []state.FinancialUsageRecord
	var after int64
	for {
		page, err := store.ListFinancialUsageEvidence(t.Context(), account, start, end, after, head, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			return all
		}
		if page[0].Sequence <= after || page[0].Sequence > head {
			t.Fatalf("invalid page: %+v", page)
		}
		all = append(all, page...)
		after = page[len(page)-1].Sequence
	}
}

func financialRetentionSuite(t *testing.T, store financialTestStore) {
	a := financialAccount(t, store)
	other := financialAccount(t, store)
	app, err := store.CreateApp(t.Context(), state.App{AccountID: a.ID, Slug: "billing-original", Type: state.AppTypeApp, Runtime: "node22", RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	start, end := financialPeriod()
	minute := start.Add(24 * time.Hour)
	dep, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:billing", Status: state.DeployPending})
	if err != nil {
		t.Fatal(err)
	}
	nodeID := state.DefaultLocalNodeName
	if pg, ok := store.(*state.PgStore); ok {
		nodeID = financialLocalNode(t, t.Context(), pg)
	}
	ins, err := store.CreateInstance(t.Context(), app.ID, dep.ID, string(state.StateParked), 256, nodeID, "")
	if err != nil {
		t.Fatal(err)
	}
	instance := ins.ID
	for range 2 {
		if err := store.AppendUsage(t.Context(), a.ID, app.ID, instance, minute, 15_840, 0, 0, 0, 0, 0, 0, 0); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if _, _, err := store.AppendNetworkUsageObservation(t.Context(), a.ID, app.ID, instance, minute, 100, true, 40, true); err != nil {
			t.Fatal(err)
		}
	}
	head, err := store.FinancialEvidenceHead(t.Context(), a.ID, start, end)
	if err != nil {
		t.Fatal(err)
	}
	frozen := financialRows(t, store, a.ID, head)
	if len(frozen) != 2 || frozen[0].Evidence.Meter != "compute" || frozen[0].Evidence.Quantity != 15_840 || frozen[1].Evidence.Quantity != 100 {
		t.Fatalf("replay duplicated or lost evidence: %+v", frozen)
	}
	if _, err := store.RenameApp(t.Context(), a.ID, app.Slug, "billing-renamed"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.AppendNetworkUsageObservation(t.Context(), a.ID, app.ID, instance, minute, 150, true, 40, true); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteApp(t.Context(), app.ID); err != nil {
		t.Fatal(err)
	}
	if got := financialRows(t, store, a.ID, head); !reflect.DeepEqual(got, frozen) {
		t.Fatalf("fixed head changed: %+v", got)
	}
	head, err = store.FinancialEvidenceHead(t.Context(), a.ID, start, end)
	if err != nil {
		t.Fatal(err)
	}
	rows := financialRows(t, store, a.ID, head)
	if len(rows) != 3 || rows[2].Evidence.Quantity != 50 {
		t.Fatalf("incremental evidence: %+v", rows)
	}
	for _, row := range rows {
		if row.Evidence.Attribution.AppID != app.ID || row.Evidence.Attribution.Name != "billing-original" || row.Plan != api.PlanHobby {
			t.Fatalf("lost historical identity: %+v", row)
		}
	}
	if got := financialRows(t, store, other.ID, head); len(got) != 0 {
		t.Fatalf("cross-account evidence: %+v", got)
	}
	coverage, err := store.FinancialEvidenceCoverage(t.Context())
	if err != nil || coverage.IsZero() || coverage.After(time.Now()) {
		t.Fatalf("coverage = %v, %v", coverage, err)
	}
	if _, err := store.ListFinancialUsageEvidence(t.Context(), a.ID, start, end, 0, head, api.FinancialEvidencePageMax+1); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("unbounded page: %v", err)
	}
}

func financialPriceSuite(t *testing.T, store financialTestStore) {
	a := financialAccount(t, store)
	other := financialAccount(t, store)
	start, end := financialPeriod()
	p := state.FinancialPriceSnapshot{AccountID: a.ID, PeriodStart: start, PeriodEnd: end, Plan: api.PlanHobby, EffectiveFrom: start, DeliveryMode: "live", Price: financial.Price{Version: "compute-hobby-v1", Meter: "compute", Currency: "EUR", Unit: "mb_seconds", UnitQuantity: api.SecondsPerGBHour, MillicentsPerUnit: 1000, IncludedQuantity: 50 * api.SecondsPerGBHour}}
	first, err := store.PutFinancialPriceSnapshot(t.Context(), p)
	if err != nil || first.RecordedAt.IsZero() {
		t.Fatalf("price put: %+v, %v", first, err)
	}
	replay, err := store.PutFinancialPriceSnapshot(t.Context(), p)
	if err != nil || !reflect.DeepEqual(replay, first) {
		t.Fatalf("price replay: %+v, %v", replay, err)
	}
	p.Price.MillicentsPerUnit++
	if _, err := store.PutFinancialPriceSnapshot(t.Context(), p); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("mutable price: %v", err)
	}
	rows, err := store.ListFinancialPriceSnapshots(t.Context(), a.ID, start)
	if err != nil || len(rows) != 1 || !reflect.DeepEqual(rows[0], first) {
		t.Fatalf("price read: %+v, %v", rows, err)
	}
	rows, err = store.ListFinancialPriceSnapshots(t.Context(), other.ID, start)
	if err != nil || len(rows) != 0 {
		t.Fatalf("cross-account price: %+v, %v", rows, err)
	}
	p.AccountID = uuid.NewString()
	if _, err := store.PutFinancialPriceSnapshot(t.Context(), p); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("missing account price: %v", err)
	}
	p.AccountID = a.ID
	p.PeriodStart = start.Add(time.Second)
	if _, err := store.PutFinancialPriceSnapshot(t.Context(), p); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("unaligned period: %v", err)
	}
}

func financialJobSuite(t *testing.T, store financialTestStore) {
	a := financialAccount(t, store)
	job, err := store.JobCreate(t.Context(), a.ID, "billing-job", "batch", "registry.example/job:v1", []string{"/bin/job"}, 256, 60, 1, 0, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	start, end := financialPeriod()
	if err := store.AppendJobUsage(t.Context(), a.ID, job.ID, uuid.NewString(), start.Add(time.Hour), 100, 0, 0, 0, 50, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	head, err := store.FinancialEvidenceHead(context.Background(), a.ID, start, end)
	if err != nil {
		t.Fatal(err)
	}
	rows := financialRows(t, store, a.ID, head)
	if len(rows) != 2 {
		t.Fatalf("job evidence: %+v", rows)
	}
	for _, row := range rows {
		if row.Evidence.Attribution.AppID != "" || row.Evidence.Attribution.JobID != job.ID || row.Evidence.Attribution.Name != job.Name {
			t.Fatalf("job attributed as app: %+v", row)
		}
	}
}

func financialContractSuite(t *testing.T, store financialTestStore) {
	a := financialAccount(t, store)
	app, err := store.CreateApp(t.Context(), state.App{AccountID: a.ID, Slug: "contracts", Type: state.AppTypeApp, Runtime: "node22", RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	start, end := financialPeriod()
	minute := start.Add(2 * time.Hour)
	appendUsage := func(at time.Time, quantity int64) {
		t.Helper()
		if err := store.AppendUsage(t.Context(), a.ID, app.ID, uuid.NewString(), at, quantity, 0, 0, 0, 0, 0, 0, 0); err != nil {
			t.Fatal(err)
		}
	}
	appendUsage(minute, 5) // Missing prices must remain missing after later activation.
	p := state.FinancialPriceSnapshot{AccountID: a.ID, PeriodStart: start, PeriodEnd: end, Plan: api.PlanHobby, EffectiveFrom: minute.Add(time.Minute), DeliveryMode: "live", Price: financial.Price{Version: "hobby-first", Meter: "compute", Currency: "EUR", Unit: "mb_seconds", UnitQuantity: api.SecondsPerGBHour, MillicentsPerUnit: 1000, IncludedQuantity: 50 * api.SecondsPerGBHour}}
	if _, err := store.PutFinancialPriceSnapshot(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	appendUsage(p.EffectiveFrom, 10)
	appendUsage(p.EffectiveFrom.Add(time.Minute), 20)
	head, err := store.FinancialEvidenceHead(t.Context(), a.ID, start, end)
	if err != nil {
		t.Fatal(err)
	}
	p.Price.Version, p.Plan, p.EffectiveFrom = "pro-first", api.PlanPro, minute.Add(3*time.Minute)
	p.Price.IncludedQuantity = 250 * api.SecondsPerGBHour
	if _, err := store.PutFinancialPriceSnapshot(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountPlan(t.Context(), a.ID, api.PlanPro); err != nil {
		t.Fatal(err)
	}
	appendUsage(p.EffectiveFrom, 40)
	rows := financialRows(t, store, a.ID, head)
	if len(rows) != 3 || rows[0].PriceVersion != "" || rows[1].PriceVersion != "hobby-first" || rows[2].PriceVersion != "hobby-first" {
		t.Fatalf("historical price capture changed: %+v", rows)
	}
	aggregates, err := store.AggregateFinancialUsage(t.Context(), a.ID, start, end, head)
	if err != nil || len(aggregates) != 2 {
		t.Fatalf("fixed-head aggregation: %+v, %v", aggregates, err)
	}
	byVersion := map[string]state.FinancialUsageAggregate{}
	for _, row := range aggregates {
		byVersion[row.PriceVersion] = row
	}
	if byVersion[""].Quantity != 5 || byVersion["hobby-first"].Quantity != 30 || byVersion["hobby-first"].SourceCount != 2 {
		t.Fatalf("aggregation lost evidence or included a later write: %+v", byVersion)
	}
	head, err = store.FinancialEvidenceHead(t.Context(), a.ID, start, end)
	if err != nil {
		t.Fatal(err)
	}
	rows = financialRows(t, store, a.ID, head)
	if len(rows) != 4 || rows[3].PriceVersion != "pro-first" || rows[3].Plan != api.PlanPro {
		t.Fatalf("new contract not captured: %+v", rows)
	}
}

func financialSamplingSuite(t *testing.T, store financialTestStore) {
	start, _ := financialPeriod()
	start = start.Add(48 * time.Hour)
	for _, write := range []struct {
		offset          time.Duration
		compute, egress bool
	}{{0, true, false}, {0, false, true}, {time.Minute, false, false}, {2 * time.Minute, true, false}, {3 * time.Minute, true, true}} {
		if err := store.RecordFinancialSamplingWindow(t.Context(), start.Add(write.offset), write.compute, write.egress); err != nil {
			t.Fatal(err)
		}
	}
	coverage, err := store.FinancialSamplingCoverage(t.Context(), start, start.Add(3*time.Minute))
	if err != nil || coverage.ComputeMinutes != 2 || coverage.EgressMinutes != 1 || coverage.ObservedAt.IsZero() {
		t.Fatalf("coverage counts false, duplicate or outside windows: %+v, %v", coverage, err)
	}
	empty, err := store.FinancialSamplingCoverage(t.Context(), start, start)
	if err != nil || empty != (state.FinancialSamplingCoverage{}) {
		t.Fatalf("empty coverage: %+v, %v", empty, err)
	}
	if err := store.RecordFinancialSamplingWindow(t.Context(), start.Add(time.Second), true, true); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("unaligned sample accepted: %v", err)
	}
}
