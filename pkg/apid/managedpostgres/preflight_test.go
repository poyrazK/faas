package managedpostgres

import (
	"testing"
	"time"

	domain "github.com/onebox-faas/faas/pkg/managedpostgres"
)

func TestPreflightViewRecomputesStalenessAndNeverAllowsActivation(t *testing.T) {
	now := time.Now().UTC()
	c := domain.Cutover{UpdatedAt: now, State: domain.CutoverVerified}
	p := domain.CutoverPreflight{State: "blocked", CheckedAt: now, CutoverUpdatedAt: now, ExpiresAt: now.Add(time.Minute)}
	if got := PreflightView(p, c, now); got.Stale || got.ActivationAllowed {
		t.Fatal(got)
	}
	for _, mode := range []string{"pending", "expired", "future", "cutover_changed", "runtime_changed", "source_sessions_changed", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			copyP, copyC := p, c
			switch mode {
			case "pending":
				copyP.State = "pending"
			case "expired":
				copyP.ExpiresAt = now
			case "future":
				copyP.CheckedAt = now.Add(time.Second)
			case "cutover_changed":
				copyC.UpdatedAt = now.Add(time.Second)
			case "runtime_changed":
				copyP.RuntimeDrainChanged = true
			case "source_sessions_changed":
				copyP.SourceSessionsChanged = true
			case "cancelled":
				copyC.State = domain.CutoverCancelled
			}
			if got := PreflightView(copyP, copyC, now); !got.Stale || got.ActivationAllowed {
				t.Fatal(got)
			}
		})
	}
}

func TestPreflightViewRetainsHistoricalRuntimeFindings(t *testing.T) {
	p := domain.CutoverPreflight{RuntimeDrainChanged: true, RuntimeDrain: &domain.CutoverRuntimeDrain{
		State: "catalog_retired", InventoryCount: 1, Retired: 1, Members: []domain.CutoverRuntimeDrainMember{{InstanceID: "instance", Scope: "production", Mode: "job", State: "retired"}},
	}}
	got := PreflightView(p, domain.Cutover{}, time.Now())
	if !got.Stale || got.ActivationAllowed || got.RuntimeDrain == nil || got.RuntimeDrain.State != "catalog_retired" || got.RuntimeDrain.Members[0].Scope != "production" {
		t.Fatal(got)
	}
	got.RuntimeDrain.Members[0].State = "mutated"
	if p.RuntimeDrain.Members[0].State != "retired" {
		t.Fatal("view mutated the saved report")
	}
}

func TestPreflightViewRetainsHistoricalSourceSessions(t *testing.T) {
	p := domain.CutoverPreflight{SourceSessionsChanged: true, SourceSessions: &domain.CutoverSourceSessions{
		State: "closed", AdmissionClosed: true, Sessions: 3, PreparedTransactions: 1, Blockers: []string{"source_sessions_active"},
		WriterIsolation: &domain.CutoverWriterIsolation{State: "unsupported", UnknownBackends: 2},
	}}
	got := PreflightView(p, domain.Cutover{}, time.Now())
	if !got.Stale || got.ActivationAllowed || got.SourceSessions == nil || got.SourceSessions.Sessions != 3 || !got.SourceSessions.AdmissionClosed {
		t.Fatal(got)
	}
	got.SourceSessions.Blockers[0] = "mutated"
	got.SourceSessions.WriterIsolation.UnknownBackends = 99
	if p.SourceSessions.WriterIsolation.UnknownBackends != 2 {
		t.Fatal("view mutated saved writer diagnostics")
	}
	if p.SourceSessions.Blockers[0] != "source_sessions_active" {
		t.Fatal("view mutated saved SQL findings")
	}
}
