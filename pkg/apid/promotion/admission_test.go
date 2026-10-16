package promotion

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type queuedHealthStore struct {
	healthControlStore
	calls int
	err   error
}

func (s *queuedHealthStore) DispatchProjectEnvironmentPromotion(_ context.Context, account, id, token string, generation int64) (*api.ProjectPromotionHealth, error) {
	if account != "account" || id != "promotion" || token == "" || generation != s.h.Generation {
		panic("lost dispatch identity")
	}
	s.calls++
	return s.h, s.err
}
func TestQueuedReconcileNeverPreparesOrProbes(t *testing.T) {
	for _, dispatchErr := range []error{nil, state.ErrConflict, context.DeadlineExceeded} {
		s := &queuedHealthStore{healthControlStore: healthControlStore{p: state.ProjectEnvironmentPromotion{ID: "promotion", AccountID: "account", Status: "running"}, h: &api.ProjectPromotionHealth{Phase: "queued", Generation: 1}}, err: dispatchErr}
		unexpected := func() { t.Fatal("queued promotion performed external work") }
		actions := HealthActions{Prepare: func(context.Context, state.ProjectEnvironmentPromotion, []state.ProjectEnvironmentPromotionWorkload) error {
			unexpected()
			return nil
		}, Probe: func(context.Context, state.ProjectEnvironmentPromotion, []state.ProjectEnvironmentPromotionWorkload, api.ProjectPromotionHealthPolicy) ([]api.ProjectReleaseProbeEvidence, error) {
			unexpected()
			return nil, nil
		}}
		err := ReconcileHealth(t.Context(), s, state.PromotionHealthJob{}, actions)
		if s.calls != 1 || !errors.Is(dispatchErr, state.ErrConflict) && !errors.Is(err, dispatchErr) || errors.Is(dispatchErr, state.ErrConflict) && err != nil {
			t.Fatal("dispatch failed", s.calls, err)
		}
	}
}
func TestAdmissionErrorHTTP(t *testing.T) {
	for _, scope := range []string{"account", "fleet"} {
		w := httptest.NewRecorder()
		if !WriteAdmissionError(w, &state.PromotionAdmissionFullError{Scope: scope, Limit: 32, Observed: 32}) || w.Code != 429 || w.Header().Get("Retry-After") != "5" {
			t.Fatal("queue overload response", w)
		}
		for _, part := range []string{api.CodeProjectPromotionQueueFull, `"limit":32`, `"observed":32`} {
			if !strings.Contains(w.Body.String(), part) {
				t.Fatal("missing rejection evidence", w.Body)
			}
		}
	}
	if WriteAdmissionError(httptest.NewRecorder(), errors.New("offline")) {
		t.Fatal("infra error classified as admission rejection")
	}
}
func TestQueuedMigrationCanAbort(t *testing.T) {
	h := &api.ProjectPromotionHealth{Phase: "queued", Generation: 1, Admission: &api.ProjectPromotionAdmission{WaitDeadline: time.Now().Add(time.Minute)}, Policy: api.ProjectPromotionHealthPolicy{Migration: &api.ProjectPromotionMigrationPolicy{}}}
	if a := api.ProjectPromotionControlActions(h); len(a) != 1 || a[0] != "abort" {
		t.Fatal("queued migration cannot be cancelled", a)
	}
	h.Phase = "migrating"
	if a := api.ProjectPromotionControlActions(h); len(a) != 0 {
		t.Fatal("active migration unexpectedly cancellable", a)
	}
}
