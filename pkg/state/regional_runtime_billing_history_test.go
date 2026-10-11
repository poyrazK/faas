package state

// adr: 799
import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
)

func regionalBillingHistoryRows(t *testing.T, s RegionalRuntimeBillingHistoryStore, app string, minute time.Time) []RegionalRuntimeBillingInterval {
	t.Helper()
	rows, err := s.ListRegionalRuntimeBillingIntervals(t.Context(), app, minute, 0, api.RegionalRuntimeCapacityPageMax)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func runRegionalRuntimeBillingHistoryContract(t *testing.T, fixture func(*testing.T) (regionalCapacityFixture, Instance)) {
	t.Helper()
	for _, kind := range []string{runtimequota.CapacityServing, runtimequota.CapacityWarm, runtimequota.CapacityPrime} {
		t.Run(kind, func(t *testing.T) {
			f, ins := fixture(t)
			s := f.ledger.(Store)
			minute := ins.StartedAt.UTC().Truncate(time.Minute)
			p := f.permit(kind)
			p.InstanceID, p.DeploymentID, p.NodeID, p.RunID = regionalQuotaCanonicalID(ins.ID), regionalQuotaCanonicalID(ins.DeploymentID), regionalQuotaCanonicalID(ins.NodeID), regionalQuotaCanonicalID(ins.WakeID)
			source := runtimequota.LaunchSource{Permit: p, InstanceKey: ins.ID}
			rows := regionalBillingHistoryRows(t, s, f.app(), minute)
			if len(rows) != 1 || !rows[0].CompleteStart || !regionalBillingIntervalMatches(rows[0], p) || rows[0].SourceMatches != 0 || rows[0].Permit != nil || rows[0].Launch != nil {
				t.Fatal("pre-launch billing history is missing or fabricated", rows)
			}
			original := rows[0]
			resources := RegionalRuntimeResources{RAMMB: 192, VCPU: 2, CPUMillicores: 1000} // Includes original sidecar RAM.
			placement := runtimequota.Placement{Permit: p}
			if kind == runtimequota.CapacityServing {
				placement.Seat = &runtimequota.ServingSeat{Permit: p, ID: uuid.NewString()}
			}
			if err := s.IssueRegionalRuntimeLaunchResources(t.Context(), placement, source, resources); err != nil {
				t.Fatal(err)
			}
			rows = regionalBillingHistoryRows(t, s, f.app(), minute)
			if len(rows) != 1 || rows[0].SourceMatches != 1 || rows[0].Permit == nil || *rows[0].Permit != p || rows[0].Launch == nil || rows[0].Launch.Source != source || rows[0].Launch.Resources != resources || rows[0].IssuedAt == nil || !rows[0].StartedAt.Equal(original.StartedAt) {
				t.Fatal("issued source lost original run and dimensions", rows)
			}
			fenced, err := s.BeginRegionalRuntimeLaunchFailure(t.Context(), source, uuid.NewString())
			if err != nil {
				t.Fatal(err)
			}
			f.stop(t, p, fenced.FenceID)
			if err := s.FailRegionalRuntimeAttempt(t.Context(), source, ins.State); err != nil {
				t.Fatal(err)
			}
			closed := regionalBillingHistoryRows(t, s, f.app(), minute)
			if len(closed) != 1 || closed[0].EndedAt == nil || closed[0].UnknownSince != nil || !closed[0].StartedAt.Equal(original.StartedAt) {
				t.Fatal("short run did not close independently of a sampler tick", closed)
			}
			if err := s.DeleteInstance(t.Context(), ins.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := s.ScheduleAppDeletion(t.Context(), f.app(), time.Now().Add(-time.Minute)); err != nil {
				t.Fatal(err)
			}
			if err := s.ClaimAppDeletion(t.Context(), f.app()); err != nil {
				t.Fatal(err)
			}
			if err := s.DeleteAppPermanently(t.Context(), f.app()); err != nil {
				t.Fatal(err)
			}
			if got := regionalBillingHistoryRows(t, s, f.app(), minute); !reflect.DeepEqual(got, closed) {
				t.Fatal("collection changed original billing history", got, closed)
			}
			closed[0].Launch.Resources.RAMMB++
			closed[0].Permit.NodeID = uuid.NewString()
			*closed[0].EndedAt = time.Time{}
			got := regionalBillingHistoryRows(t, s, f.app(), minute)
			if got[0].Launch.Resources != resources || *got[0].Permit != p || got[0].EndedAt.IsZero() {
				t.Fatal("returned history aliases retained evidence", got)
			}
		})
	}
	for _, provenance := range []string{"reserved", "wrong-run", "no-source", "unknown-resources", "ambiguous"} {
		t.Run(provenance, func(t *testing.T) {
			f, ins := fixture(t)
			s := f.ledger.(Store)
			p := f.permit(runtimequota.CapacityWarm)
			p.InstanceID, p.DeploymentID, p.NodeID, p.RunID = regionalQuotaCanonicalID(ins.ID), regionalQuotaCanonicalID(ins.DeploymentID), regionalQuotaCanonicalID(ins.NodeID), regionalQuotaCanonicalID(ins.WakeID)
			source := runtimequota.LaunchSource{Permit: p, InstanceKey: ins.ID}
			if provenance == "wrong-run" {
				p.RunID = uuid.NewString()
				source.Permit = p
			}
			if provenance == "reserved" {
				f.reserve(t, p)
			} else if provenance == "no-source" {
				f.reserve(t, p)
				f.issue(t, p)
			} else if err := s.IssueRegionalRuntimeLaunch(t.Context(), runtimequota.Placement{Permit: p}, source); err != nil {
				t.Fatal(err)
			}
			if provenance == "ambiguous" {
				p.ID, p.HostEpochID = uuid.NewString(), uuid.NewString()
				source.Permit = p
				if err := s.IssueRegionalRuntimeLaunch(t.Context(), runtimequota.Placement{Permit: p}, source); err != nil {
					t.Fatal(err)
				}
			}
			rows := regionalBillingHistoryRows(t, s, f.app(), ins.StartedAt.UTC().Truncate(time.Minute))
			if provenance == "reserved" || provenance == "wrong-run" {
				if len(rows) != 1 || rows[0].SourceMatches != 0 || rows[0].Permit != nil || rows[0].Launch != nil || rows[0].IssuedAt != nil {
					t.Fatal("unissued or unrelated run lent billing provenance", rows)
				}
				return
			}
			matches := int64(1)
			if provenance == "ambiguous" {
				matches = 2
			}
			if len(rows) != 1 || rows[0].SourceMatches != matches || rows[0].Permit == nil || rows[0].IssuedAt == nil {
				t.Fatal("missing or ambiguous issuance was hidden", rows)
			}
			if provenance == "no-source" && rows[0].Launch != nil {
				t.Fatal("native lookup was fabricated", rows)
			}
			if rows[0].Launch != nil && rows[0].Launch.Resources != (RegionalRuntimeResources{}) {
				t.Fatal("historical resources were filled from today's app", rows)
			}
		})
	}
	t.Run("deleted-open-run-still-holds-capacity", func(t *testing.T) {
		f, ins := fixture(t)
		s := f.ledger.(Store)
		p := f.permit(runtimequota.CapacityWarm)
		p.InstanceID, p.DeploymentID, p.NodeID, p.RunID = regionalQuotaCanonicalID(ins.ID), regionalQuotaCanonicalID(ins.DeploymentID), regionalQuotaCanonicalID(ins.NodeID), regionalQuotaCanonicalID(ins.WakeID)
		source := runtimequota.LaunchSource{Permit: p, InstanceKey: ins.ID}
		if err := s.IssueRegionalRuntimeLaunchResources(t.Context(), runtimequota.Placement{Permit: p}, source, RegionalRuntimeResources{RAMMB: 128, VCPU: 1, CPUMillicores: 1000}); err != nil {
			t.Fatal(err)
		}
		original := regionalBillingHistoryRows(t, s, f.app(), ins.StartedAt.UTC().Truncate(time.Minute))
		if err := s.UpdateInstanceState(t.Context(), ins.ID, string(StateRunning)); !errors.Is(err, ErrRegionalRuntimeQuotaFrozen) {
			t.Fatal("managed execution fence did not reject publication", err)
		}
		if got := regionalBillingHistoryRows(t, s, f.app(), ins.StartedAt.UTC().Truncate(time.Minute)); !reflect.DeepEqual(got, original) {
			t.Fatal("rejected transition changed billing history", got)
		}
		if err := s.DeleteInstance(t.Context(), ins.ID); err != nil {
			t.Fatal(err)
		}
		rows := regionalBillingHistoryRows(t, s, f.app(), ins.StartedAt.UTC().Truncate(time.Minute).Add(time.Minute))
		if len(rows) != 1 || rows[0].EndedAt != nil || rows[0].UnknownSince == nil || rows[0].SourceMatches != 1 || rows[0].Launch == nil || rows[0].Launch.Source != source {
			t.Fatal("deleting an open run proved an unsupported zero", rows)
		}
		f.usage(t, 1, 1, 0)
	})
}

func TestMemRegionalRuntimeBillingHistoryContract(t *testing.T) {
	runRegionalRuntimeBillingHistoryContract(t, func(t *testing.T) (regionalCapacityFixture, Instance) {
		s := NewMemStore()
		a, err := s.CreateAccount(t.Context(), uuid.NewString()+"@billing-history.example", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		return regionalAttemptFailureFixture(t, s, a, uuid.NewString())
	})
}

func TestMemRegionalRuntimeBillingHistoryTransitions(t *testing.T) {
	s := NewMemStore()
	minute := time.Now().UTC().Truncate(time.Minute)
	now := minute.Add(3 * time.Second)
	s.SetClockForTest(func() time.Time { return now })
	ins := Instance{ID: uuid.NewString(), AppID: uuid.NewString(), DeploymentID: uuid.NewString(), NodeID: uuid.NewString(), WakeID: uuid.NewString(), State: string(StateWaking), StartedAt: now}
	s.putInstanceLocked(ins.ID, ins)
	now = now.Add(2 * time.Second)
	ins.State, ins.StartedAt = string(StateRunning), now
	s.putInstanceLocked(ins.ID, ins)
	rows := regionalBillingHistoryRows(t, s, ins.AppID, minute)
	if len(rows) != 1 || !rows[0].StartedAt.Equal(minute.Add(3*time.Second)) {
		t.Fatal("running publication reset the original start", rows)
	}
	ins.State = string(StateDraining)
	s.putInstanceLocked(ins.ID, ins)
	ins.State = string(StateStopped)
	s.putInstanceLocked(ins.ID, ins)
	now = now.Add(time.Second)
	ins.State = string(StateWarm)
	s.putInstanceLocked(ins.ID, ins)
	now = now.Add(time.Second)
	ins.WakeID = uuid.NewString()
	s.putInstanceLocked(ins.ID, ins)
	now = now.Add(time.Second)
	s.deleteInstanceLocked(ins.ID)
	rows = regionalBillingHistoryRows(t, s, ins.AppID, minute)
	if len(rows) != 3 || rows[0].EndedAt == nil || rows[0].UnknownSince != nil || rows[1].UnknownSince == nil || rows[1].EndedAt != nil || rows[2].UnknownSince == nil || rows[2].EndedAt != nil {
		t.Fatal("closure, run replacement or deletion lost uncertainty", rows)
	}
	for i, row := range rows {
		page, err := s.ListRegionalRuntimeBillingIntervals(t.Context(), ins.AppID, minute, row.ID-1, 1)
		if err != nil || len(page) != 1 || !reflect.DeepEqual(page[0], rows[i]) {
			t.Fatal("history pagination", page, err)
		}
	}
	later := regionalBillingHistoryRows(t, s, ins.AppID, minute.Add(time.Minute))
	if len(later) != 2 || later[0].UnknownSince == nil || later[1].UnknownSince == nil {
		t.Fatal("unresolved deletion was interpreted as zero in a later minute", later)
	}
	ins.ID, ins.WakeID, ins.Mode = uuid.NewString(), uuid.NewString(), "mirror"
	s.putInstanceLocked(ins.ID, ins)
	if len(regionalBillingHistoryRows(t, s, ins.AppID, minute)) != 3 {
		t.Fatal("mirror added compute history")
	}
	ins.ID, ins.AppID, ins.Mode = uuid.NewString(), "", "job"
	s.putInstanceLocked(ins.ID, ins)
	if len(s.regionalRuntimeBillingRows) != 3 {
		t.Fatal("independent job task entered app runtime billing history")
	}
}

func TestMemRegionalRuntimeBillingHistoryPageValidation(t *testing.T) {
	s := NewMemStore()
	app, minute := uuid.NewString(), time.Now().UTC().Truncate(time.Minute)
	for _, tc := range []struct {
		app    string
		minute time.Time
		after  int64
		limit  int
	}{
		{"bad", minute, 0, 1}, {app, minute.Add(time.Second), 0, 1}, {app, minute, -1, 1},
		{app, minute, 0, 0}, {app, minute, 0, api.RegionalRuntimeCapacityPageMax + 1},
	} {
		if _, err := s.ListRegionalRuntimeBillingIntervals(t.Context(), tc.app, tc.minute, tc.after, tc.limit); !errors.Is(err, ErrInvalidArgument) {
			t.Fatal("invalid billing history page accepted", tc, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.ListRegionalRuntimeBillingIntervals(ctx, app, minute, 0, 1); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	start, err := s.RegionalRuntimeBillingHistoryStart(t.Context())
	if err != nil || start.IsZero() {
		t.Fatal("missing installation boundary", start, err)
	}
}
