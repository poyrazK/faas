package state

// adr: 633
import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
)

func runRegionalLaunchSourceContract(t *testing.T, fixture func(*testing.T) regionalCapacityFixture) {
	t.Helper()
	for _, kind := range []string{runtimequota.CapacityServing, runtimequota.CapacityWarm, runtimequota.CapacityPrime, runtimequota.CapacityAppTask} {
		t.Run(kind+"-exact-concurrent-source", func(t *testing.T) {
			f := fixture(t)
			p := initialRegionalPlacement(f, kind)
			source := runtimequota.LaunchSource{Permit: p.Permit, InstanceKey: strings.ReplaceAll(p.Permit.InstanceID, "-", "")}
			var wg sync.WaitGroup
			done := make(chan error, 4)
			for range 4 {
				wg.Go(func() { done <- f.ledger.IssueRegionalRuntimeLaunch(t.Context(), p, source) })
			}
			wg.Wait()
			close(done)
			for err := range done {
				if err != nil {
					t.Fatal(err)
				}
			}
			before, err := f.ledger.RegionalRuntimeLaunchSource(t.Context(), f.app(), p.Permit.ID)
			if err != nil || before.Source != source || before.CreatedAt.IsZero() {
				t.Fatal("original native lookup not retained", before, err)
			}
			if err := f.ledger.CheckRegionalRuntimeIssuedPlacement(t.Context(), p); err != nil {
				t.Fatal(err)
			}
			changed := source
			changed.InstanceKey = p.Permit.InstanceID
			if err := f.ledger.IssueRegionalRuntimeLaunch(t.Context(), p, changed); !errors.Is(err, ErrConflict) {
				t.Fatal("equivalent UUID spelling replaced native lookup", err)
			}
			after, err := f.ledger.RegionalRuntimeLaunchSource(t.Context(), f.app(), p.Permit.ID)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("conflicting retry changed original source", err)
			}
			if _, err := f.ledger.BeginRegionalRuntimeRetirement(t.Context(), p.Permit, uuid.NewString()); err != nil {
				t.Fatal(err)
			}
			if err := f.ledger.IssueRegionalRuntimeLaunch(t.Context(), p, source); !errors.Is(err, ErrRegionalRuntimePlacementUnavailable) {
				t.Fatal("source retry reopened a fence", err)
			}
		})
	}
	for _, planned := range []bool{false, true} {
		t.Run(map[bool]string{false: "new", true: "planned"}[planned]+"-atomic-seat-failure", func(t *testing.T) {
			f := fixture(t)
			for range f.quota.grant.Concurrency {
				p := initialRegionalPlacement(f, runtimequota.CapacityServing)
				if err := f.ledger.IssueRegionalRuntimeLaunch(t.Context(), p, runtimequota.LaunchSource{Permit: p.Permit, InstanceKey: p.Permit.InstanceID}); err != nil {
					t.Fatal(err)
				}
			}
			p := initialRegionalPlacement(f, runtimequota.CapacityServing)
			if planned {
				f.reserve(t, p.Permit)
			}
			if err := f.ledger.IssueRegionalRuntimeLaunch(t.Context(), p, runtimequota.LaunchSource{Permit: p.Permit, InstanceKey: p.Permit.InstanceID}); !errors.Is(err, ErrRegionalRuntimeCapacity) {
				t.Fatal("quota exhaustion admitted launch", err)
			}
			if _, err := f.ledger.RegionalRuntimeLaunchSource(t.Context(), f.app(), p.Permit.ID); !errors.Is(err, ErrNotFound) {
				t.Fatal("failed issuance left a source", err)
			}
			r, err := f.ledger.RegionalRuntimeCapacity(t.Context(), f.app(), p.Permit.ID)
			if (planned && (err != nil || r.IssuedAt != nil)) || (!planned && !errors.Is(err, ErrNotFound)) {
				t.Fatal("failed issuance changed physical hold", r, err)
			}
		})
	}
	t.Run("legacy-issued-source-cannot-be-backfilled", func(t *testing.T) {
		f := fixture(t)
		p := initialRegionalPlacement(f, runtimequota.CapacityWarm)
		if err := f.ledger.IssueRegionalRuntimePlacement(t.Context(), p); err != nil {
			t.Fatal(err)
		}
		if err := f.ledger.IssueRegionalRuntimeLaunch(t.Context(), p, runtimequota.LaunchSource{Permit: p.Permit, InstanceKey: p.Permit.InstanceID}); !errors.Is(err, ErrRegionalRuntimePlacementUnavailable) {
			t.Fatal("legacy source fabricated after issuance", err)
		}
		if _, err := f.ledger.BeginRegionalRuntimeRetirement(t.Context(), p.Permit, uuid.NewString()); err != nil {
			t.Fatal(err)
		}
		rows, err := f.ledger.ListRegionalRuntimeRetirements(t.Context(), f.cell.IssuerID(), RegionalRuntimeRetirementCursor{}, 10)
		if err != nil || len(rows) != 0 {
			t.Fatal("missing source became a cleanup candidate", rows, err)
		}
		f.usage(t, 1, 1, 0)
	})
	for _, bad := range []string{"empty", "uppercase", "urn", "other-instance", "other-permit", "cancelled"} {
		t.Run("refuse-"+bad, func(t *testing.T) {
			f := fixture(t)
			p := initialRegionalPlacement(f, runtimequota.CapacityWarm)
			p.Permit.InstanceID = "abcdef01-2345-4678-9abc-def012345678"
			source := runtimequota.LaunchSource{Permit: p.Permit, InstanceKey: p.Permit.InstanceID}
			ctx := t.Context()
			switch bad {
			case "empty":
				source.InstanceKey = ""
			case "uppercase":
				source.InstanceKey = strings.ToUpper(source.InstanceKey)
			case "urn":
				source.InstanceKey = "urn:uuid:" + source.InstanceKey
			case "other-instance":
				source.InstanceKey = uuid.NewString()
			case "other-permit":
				source.Permit.RunID = uuid.NewString()
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if err := f.ledger.IssueRegionalRuntimeLaunch(ctx, p, source); err == nil {
				t.Fatal("invalid source issued")
			}
			f.usage(t, 0, 0, 0)
		})
	}
	t.Run("fenced-original-pagination-and-release", func(t *testing.T) {
		f := fixture(t)
		var want []RegionalRuntimeRetirementCandidate
		for range 3 {
			p := initialRegionalPlacement(f, runtimequota.CapacityWarm)
			source := runtimequota.LaunchSource{Permit: p.Permit, InstanceKey: strings.ReplaceAll(p.Permit.InstanceID, "-", "")}
			if err := f.ledger.IssueRegionalRuntimeLaunch(t.Context(), p, source); err != nil {
				t.Fatal(err)
			}
			r, err := f.ledger.BeginRegionalRuntimeRetirement(t.Context(), p.Permit, uuid.NewString())
			if err != nil {
				t.Fatal(err)
			}
			want = append(want, RegionalRuntimeRetirementCandidate{Source: source, FenceID: r.FenceID})
		}
		// Sources that remain unfenced are never retired merely because they exist.
		p := initialRegionalPlacement(f, runtimequota.CapacityWarm)
		if err := f.ledger.IssueRegionalRuntimeLaunch(t.Context(), p, runtimequota.LaunchSource{Permit: p.Permit, InstanceKey: p.Permit.InstanceID}); err != nil {
			t.Fatal(err)
		}
		all, err := f.ledger.ListRegionalRuntimeRetirements(t.Context(), f.cell.IssuerID(), RegionalRuntimeRetirementCursor{}, 10)
		if err != nil || len(all) != 3 {
			t.Fatal("fenced scan", all, err)
		}
		seen := map[string]bool{}
		var cursor RegionalRuntimeRetirementCursor
		for range 3 {
			rows, err := f.ledger.ListRegionalRuntimeRetirements(t.Context(), f.cell.IssuerID(), cursor, 1)
			if err != nil || len(rows) != 1 || seen[rows[0].Source.Permit.ID] {
				t.Fatal("page skipped/repeated original", rows, err)
			}
			row := rows[0]
			seen[row.Source.Permit.ID] = true
			cursor = RegionalRuntimeRetirementCursor{AppID: f.app(), PermitID: row.Source.Permit.ID}
		}
		if rows, err := f.ledger.ListRegionalRuntimeRetirements(t.Context(), f.cell.IssuerID(), cursor, 1); err != nil || len(rows) != 0 {
			t.Fatal("end of scan", rows, err)
		}
		for _, w := range want {
			if !seen[w.Source.Permit.ID] {
				t.Fatal("missing original source")
			}
		}
		if rows, err := f.ledger.ListRegionalRuntimeRetirements(t.Context(), uuid.NewString(), RegionalRuntimeRetirementCursor{}, 10); err != nil || len(rows) != 0 {
			t.Fatal("foreign cell source selected", rows, err)
		}
		f.stop(t, all[0].Source.Permit, all[0].FenceID)
		if rows, err := f.ledger.ListRegionalRuntimeRetirements(t.Context(), f.cell.IssuerID(), RegionalRuntimeRetirementCursor{}, 10); err != nil || len(rows) != 2 {
			t.Fatal("accepted stop did not leave scan", rows, err)
		}
	})
}

func TestRegionalRuntimeLaunchSourceContract(t *testing.T) {
	runRegionalLaunchSourceContract(t, memIssuedPlacementFixture)
}

func runRegionalRetirementCompositeCursor(t *testing.T, fixture func(*testing.T) regionalCapacityFixture) {
	t.Helper()
	f := fixture(t)
	store := f.ledger.(Store)
	account, err := store.CreateAccount(t.Context(), "cursor-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	g := newRegionalCapacityFixture(t, store, f.ledger, account, f.cell.IssuerID())
	sharedPermitID := uuid.NewString()
	for _, x := range []regionalCapacityFixture{f, g} {
		p := initialRegionalPlacement(x, runtimequota.CapacityWarm)
		p.Permit.ID = sharedPermitID
		if err := x.ledger.IssueRegionalRuntimeLaunch(t.Context(), p, runtimequota.LaunchSource{Permit: p.Permit, InstanceKey: p.Permit.InstanceID}); err != nil {
			t.Fatal(err)
		}
		if _, err := x.ledger.BeginRegionalRuntimeRetirement(t.Context(), p.Permit, uuid.NewString()); err != nil {
			t.Fatal(err)
		}
	}
	var after RegionalRuntimeRetirementCursor
	seen := map[string]bool{}
	for range 2 {
		rows, err := f.ledger.ListRegionalRuntimeRetirements(t.Context(), f.cell.IssuerID(), after, 1)
		if err != nil || len(rows) != 1 {
			t.Fatal("composite page missed an app", rows, err)
		}
		p := rows[0].Source.Permit
		if seen[p.Grant.Binding.RegionalAppID] || p.ID != sharedPermitID {
			t.Fatal("same permit ID skipped/repeated another app")
		}
		seen[p.Grant.Binding.RegionalAppID] = true
		after = RegionalRuntimeRetirementCursor{AppID: p.Grant.Binding.RegionalAppID, PermitID: p.ID}
	}
	if rows, err := f.ledger.ListRegionalRuntimeRetirements(t.Context(), f.cell.IssuerID(), after, 1); err != nil || len(rows) != 0 {
		t.Fatal("composite scan did not finish", rows, err)
	}
}

func TestRegionalRuntimeRetirementCompositeCursor(t *testing.T) {
	runRegionalRetirementCompositeCursor(t, memIssuedPlacementFixture)
}
