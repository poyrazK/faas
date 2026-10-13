// adr: 590
package apidserver

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

// Embed the production interface implementation so any unexpected legacy/native
// call fails instead of being silently modeled as successful metadata cleanup.
type terminalCompensationWorkerTestStore struct {
	PgStore              *state.PgStore
	t                    *testing.T
	lease                state.ProjectEnvironmentCloneLease
	events               []string
	failStep             string
	lostReply, committed bool
	wrongReceipt         bool
}

var _ projectEnvironmentCloneCoordinatorStore = (*statetest.Pg)(nil)

func (s *terminalCompensationWorkerTestStore) ClaimNextProjectEnvironmentClone(_ context.Context, token string, _ time.Duration) (state.ProjectEnvironmentCloneLease, error) {
	s.events = append(s.events, "claim")
	s.lease.Token = token
	return s.lease, nil
}
func (s *terminalCompensationWorkerTestStore) RenewProjectEnvironmentCloneLease(_ context.Context, l state.ProjectEnvironmentCloneLease, _ time.Duration) (state.ProjectEnvironmentCloneLease, error) {
	s.events = append(s.events, "renew")
	if !reflect.DeepEqual(l, s.lease) {
		s.t.Fatal("worker adopted another lease")
	}
	return l, nil
}
func (s *terminalCompensationWorkerTestStore) ReleaseProjectEnvironmentCloneLease(_ context.Context, l state.ProjectEnvironmentCloneLease, _ time.Duration) error {
	s.events = append(s.events, "release")
	if s.committed {
		return state.ErrConflict
	}
	if !reflect.DeepEqual(l, s.lease) {
		s.t.Fatal("failed cleanup changed lease", l)
	}
	return nil
}
func (s *terminalCompensationWorkerTestStore) step(ctx context.Context, l state.ProjectEnvironmentCloneLease, name string) error {
	s.t.Helper()
	s.events = append(s.events, name)
	deadline, ok := ctx.Deadline()
	if !ok || deadline.After(l.ExpiresAt) {
		s.t.Fatal("unbounded cleanup")
	}
	if !reflect.DeepEqual(l, s.lease) {
		s.t.Fatal("cleanup rewrote original inventory")
	}
	if s.failStep == name {
		return state.ErrConflict
	}
	return nil
}
func (s *terminalCompensationWorkerTestStore) CleanupProjectEnvironmentCloneConfigurationForLease(ctx context.Context, l state.ProjectEnvironmentCloneLease) error {
	return s.step(ctx, l, "configuration")
}
func (s *terminalCompensationWorkerTestStore) CleanupProjectEnvironmentCloneWorkloadsForLease(ctx context.Context, l state.ProjectEnvironmentCloneLease) error {
	return s.step(ctx, l, "workloads")
}
func (s *terminalCompensationWorkerTestStore) CleanupProjectEnvironmentCloneDeploymentsForLease(ctx context.Context, l state.ProjectEnvironmentCloneLease) error {
	return s.step(ctx, l, "deployments")
}
func (s *terminalCompensationWorkerTestStore) CleanupProjectEnvironmentCloneEnvironmentForLease(ctx context.Context, l state.ProjectEnvironmentCloneLease) error {
	return s.step(ctx, l, "environment")
}
func (s *terminalCompensationWorkerTestStore) CompleteProjectEnvironmentCloneCheckpointCompensation(ctx context.Context, l state.ProjectEnvironmentCloneLease) (state.ProjectEnvironmentCloneCompensationReceipt, error) {
	if err := s.step(ctx, l, "complete"); err != nil {
		return state.ProjectEnvironmentCloneCompensationReceipt{}, err
	}
	r := state.ProjectEnvironmentCloneCompensationReceipt{Report: state.ProjectEnvironmentCloneCheckpointCompensationReconciliation{OperationID: l.Operation.ID, Revision: l.Operation.Revision}, SHA256: strings.Repeat("a", 64)}
	if s.wrongReceipt {
		r.Report.OperationID = uuid.NewString()
		return r, nil
	}
	s.committed = true
	if s.lostReply {
		s.lostReply = false
		return state.ProjectEnvironmentCloneCompensationReceipt{}, errors.New("test commit reply lost")
	}
	return r, nil
}

func terminalCompensationWorkerFixture(t *testing.T) (*server, *terminalCompensationWorkerTestStore) {
	t.Helper()
	store := &terminalCompensationWorkerTestStore{t: t, lease: state.ProjectEnvironmentCloneLease{
		Operation: state.ProjectEnvironmentCloneOperation{ID: uuid.NewString(), AccountID: uuid.NewString(), ProjectID: uuid.NewString(), Revision: 7, Status: state.CloneOperationCompensating,
			Resources: []state.ProjectEnvironmentCloneResource{{Kind: "data_checkpoint_adoption", Name: "production", Status: "adopted"}}},
		Token: uuid.NewString(), ExpiresAt: time.Now().Add(time.Minute),
	}}
	srv := &server{store: statetest.NewPg(store), cloneWorkerAdmission: func(ctx context.Context) error { store.events = append(store.events, "admit"); return ctx.Err() }}
	return srv, store
}

func TestCloneTerminalCompensationWorkerCommitsWithoutReleasingClearedLease(t *testing.T) {
	for _, lost := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "lost_reply"}[lost], func(t *testing.T) {
			srv, store := terminalCompensationWorkerFixture(t)
			store.lostReply = lost
			resources := append([]state.ProjectEnvironmentCloneResource(nil), store.lease.Operation.Resources...)
			if err := srv.processNextProjectEnvironmentClone(t.Context(), statetest.NewPg(store)); err != nil {
				t.Fatal(err)
			}
			want := []string{"claim", "renew", "admit", "configuration", "admit", "workloads", "admit", "deployments", "admit", "environment", "admit", "complete"}
			if lost {
				want = append(want, "admit", "complete")
			}
			if !reflect.DeepEqual(store.events, want) || !store.committed {
				t.Fatal("terminal worker did not recover original commit", store.events)
			}
			if !reflect.DeepEqual(resources, store.lease.Operation.Resources) {
				t.Fatal("worker changed inventory")
			}
		})
	}
}

func TestCloneTerminalCompensationWorkerStopsOnEveryFailedPredecessor(t *testing.T) {
	for _, step := range []string{"configuration", "workloads", "deployments", "environment", "complete"} {
		t.Run(step, func(t *testing.T) {
			srv, store := terminalCompensationWorkerFixture(t)
			store.failStep = step
			if err := srv.processNextProjectEnvironmentClone(t.Context(), statetest.NewPg(store)); !errors.Is(err, state.ErrConflict) {
				t.Fatal(err)
			}
			want := []string{"claim", "renew"}
			for _, name := range []string{"configuration", "workloads", "deployments", "environment", "complete"} {
				want = append(want, "admit", name)
				if name == step {
					break
				}
			}
			want = append(want, "release")
			if !reflect.DeepEqual(store.events, want) || store.committed {
				t.Fatal("failed predecessor permitted later work", store.events)
			}
		})
	}
}

func TestCloneTerminalCompensationWorkerAdmissionAndReceiptIdentity(t *testing.T) {
	for _, fault := range []string{"missing_admission", "lost_admission", "wrong_receipt"} {
		t.Run(fault, func(t *testing.T) {
			srv, store := terminalCompensationWorkerFixture(t)
			switch fault {
			case "missing_admission":
				srv.cloneWorkerAdmission = nil
			case "lost_admission":
				srv.cloneWorkerAdmission = func(context.Context) error { store.events = append(store.events, "admit"); return state.ErrConflict }
			case "wrong_receipt":
				store.wrongReceipt = true
			}
			if err := srv.processNextProjectEnvironmentClone(t.Context(), statetest.NewPg(store)); err == nil {
				t.Fatal("invalid worker admission or receipt accepted")
			}
			if store.committed || store.events[len(store.events)-1] != "release" {
				t.Fatal("unverified terminal result cleared local lease", store.events)
			}
			if fault != "wrong_receipt" && strings.Contains(strings.Join(store.events, ","), "configuration") {
				t.Fatal("cleanup before admission")
			}
		})
	}
	srv, store := terminalCompensationWorkerFixture(t)
	srv.store = state.NewMemStore()
	if _, err := srv.completeProjectEnvironmentCloneCompensation(t.Context(), store.lease); !errors.Is(err, errCloneCompensationUnavailable) {
		t.Fatal("legacy store gained completion authority", err)
	}
}

func TestCloneTerminalCompensationWorkerReturnsTerminalRevision(t *testing.T) {
	srv, store := terminalCompensationWorkerFixture(t)
	ctx, cancel := context.WithDeadline(t.Context(), store.lease.ExpiresAt)
	defer cancel()
	finished, err := srv.processProjectEnvironmentCloneLease(ctx, statetest.NewPg(store), store.lease)
	if err != nil || finished.Operation.Status != state.CloneOperationCompensated || finished.Operation.Revision != store.lease.Operation.Revision+1 || finished.Token != "" || !finished.ExpiresAt.IsZero() {
		t.Fatal("terminal lease view", finished, err)
	}
	if !reflect.DeepEqual(finished.Operation.Resources, store.lease.Operation.Resources) {
		t.Fatal("terminal view changed original resources")
	}
}

func TestCloneTerminalCompensationWorkerStopsWhenHostAdmissionIsLost(t *testing.T) {
	for _, boundary := range []int{2, 5} {
		t.Run(map[int]string{2: "after_configuration", 5: "before_terminal_commit"}[boundary], func(t *testing.T) {
			srv, store := terminalCompensationWorkerFixture(t)
			calls := 0
			srv.cloneWorkerAdmission = func(ctx context.Context) error {
				store.events = append(store.events, "admit")
				calls++
				if calls == boundary {
					return state.ErrConflict
				}
				return ctx.Err()
			}
			if err := srv.processNextProjectEnvironmentClone(t.Context(), statetest.NewPg(store)); !errors.Is(err, state.ErrConflict) {
				t.Fatal("host loss accepted", err)
			}
			want := []string{"claim", "renew"}
			for i, name := range []string{"configuration", "workloads", "deployments", "environment", "complete"} {
				want = append(want, "admit")
				if i+1 == boundary {
					break
				}
				want = append(want, name)
			}
			want = append(want, "release")
			if store.committed || !reflect.DeepEqual(store.events, want) {
				t.Fatal("work continued after host authority was lost", store.events)
			}
		})
	}
}
