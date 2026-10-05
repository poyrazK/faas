// adr: 590
package sched

import (
	"context"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
	"log/slog"
	"strings"
	"testing"
)

type standardEgressRepairStore struct {
	*state.MemStore
	target  state.ApplicationStandardEgressTarget
	records int
	stale   bool
}

func (s *standardEgressRepairStore) ListPendingApplicationStandardEgress(context.Context, string) ([]state.ApplicationStandardEgressTarget, error) {
	return []state.ApplicationStandardEgressTarget{s.target}, nil
}
func (s *standardEgressRepairStore) RecordApplicationStandardEgress(_ context.Context, t state.ApplicationStandardEgressTarget, r runtimeadmission.EgressReceipt) (state.ApplicationStandardEgressObservation, error) {
	if s.stale {
		return state.ApplicationStandardEgressObservation{}, state.ErrApplicationStandardRuntimeStale
	}
	if r.Check(t.Identity, t.Policy) != nil {
		return state.ApplicationStandardEgressObservation{}, state.ErrInvalidArgument
	}
	s.records++
	return state.ApplicationStandardEgressObservation{Target: t, Receipt: r}, nil
}

type standardEgressRepairRouter struct {
	*recordingRouterVMM
	apply func(runtimeadmission.Identity, runtimeadmission.EgressPolicy) (runtimeadmission.EgressReceipt, error)
	calls int
}

func (r *standardEgressRepairRouter) UpdateAdmittedAppEgressPolicy(_ context.Context, i runtimeadmission.Identity, p runtimeadmission.EgressPolicy) (runtimeadmission.EgressReceipt, error) {
	r.calls++
	return r.apply(i, p)
}

func TestApplicationStandardEgressRestartRepairAndDelayedReply(t *testing.T) {
	p := runtimeadmission.EgressPolicy{AppID: uuid.NewString(), Revision: 4}
	hash, _ := p.Hash()
	i := runtimeadmission.Identity{NodeID: uuid.NewString(), Incarnation: uuid.NewString(), ProtocolVersion: runtimeadmission.ArtifactProtocolVersion}
	store := &standardEgressRepairStore{MemStore: state.NewMemStore(), target: state.ApplicationStandardEgressTarget{OrgID: uuid.NewString(), AppID: p.AppID, DesiredRevision: 3, EffectiveHash: strings.Repeat("a", 64), Identity: i, Policy: p, PolicyHash: hash}}
	router := &standardEgressRepairRouter{recordingRouterVMM: &recordingRouterVMM{}}
	router.apply = func(i runtimeadmission.Identity, p runtimeadmission.EgressPolicy) (runtimeadmission.EgressReceipt, error) {
		return runtimeadmission.EgressReceipt{Identity: i, AppID: p.AppID, Revision: p.Revision, PolicyHash: hash}, nil
	}
	subscriber := &EgressDriftSubscriber{engine: &Engine{store: store}, router: router, log: slog.Default()}
	subscriber.reconcilePending(t.Context())
	if router.calls != 1 || store.records != 1 {
		t.Fatal("startup repair required a notification")
	}
	store.stale = true
	subscriber.reconcileApp(t.Context(), p.AppID)
	if router.calls != 2 || store.records != 1 {
		t.Fatal("delayed reply bypassed storage fencing")
	}
	store.stale = false
	router.apply = func(runtimeadmission.Identity, runtimeadmission.EgressPolicy) (runtimeadmission.EgressReceipt, error) {
		return runtimeadmission.EgressReceipt{}, runtimeadmission.ErrUnavailable
	}
	subscriber.reconcileStandardEgress(t.Context(), p.AppID)
	if store.records != 1 {
		t.Fatal("missing native capability became observed")
	}
}
