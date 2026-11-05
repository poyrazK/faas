// adr: 20261104173000
package state_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestServiceAbortNamedReceiptsMem(t *testing.T) {
	s := state.NewMemStore()
	serviceAbortNamedSuite(t, s, func(ctx context.Context, id string) error {
		return s.SetDeploymentCanaryState(ctx, id, "none", 0, 0, time.Now(), "rolling_out")
	})
}

func TestServiceAbortNamedReceiptsPG(t *testing.T) {
	s, pool, _ := pgStoreWithPool(t)
	serviceAbortNamedSuite(t, s, func(ctx context.Context, id string) error {
		_, err := pool.Exec(ctx, `UPDATE deployments SET canary_total_steps=0,canary_step=0,rollout_state='rolling_out' WHERE id=$1`, id)
		return err
	})
}

func serviceAbortNamedSuite(t *testing.T, s state.Store, mark func(context.Context, string) error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	a, err := s.CreateAccount(ctx, uuid.NewString()+"@named-abort.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.CreateProject(ctx, state.Project{AccountID: a.ID, Slug: "abort-project"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: a.ID, ProjectID: p.ID, Slug: "named-abort", Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	env, err := s.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: a.ID, ProjectID: p.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	reviews, aborter, receipts, attempts := s.(state.ReleaseReviewStore), s.(state.ServiceRolloutBindingStore), s.(state.ServiceAbortReceiptStore), s.(state.ServiceAbortAttemptStore)
	create := func() state.Deployment {
		t.Helper()
		review, err := reviews.ReadReleaseReview(ctx, app.ID, "staging")
		if err != nil {
			t.Fatal(err)
		}
		settings, err := state.WorkloadSettingsFromApp(app)
		if err != nil {
			t.Fatal(err)
		}
		d, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Scope: "staging", ImageDigest: "sha256:" + strings.Repeat("a", 64), TrafficPercentExplicit: true, ExpectedReview: &review, ReviewedSettings: &settings})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.MarkDeploymentLive(ctx, d.ID); err != nil {
			t.Fatal(err)
		}
		return d
	}
	predecessor, candidate := create(), create()
	if err := mark(ctx, candidate.ID); err != nil {
		t.Fatal(err)
	}
	attempt := strings.Repeat("e", 32)
	writeContext := func(review api.AppReleaseReview) context.Context {
		return state.ContextWithServiceAbortAttempt(state.WithServiceRecoveryReview(ctx, review), attempt)
	}
	if _, err := s.UpdateProjectEnvironmentProtection(ctx, a.ID, p.ID, "staging", true); err != nil {
		t.Fatal(err)
	}
	protected, err := reviews.ReadReleaseReview(ctx, app.ID, "staging")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := aborter.RequestServiceRolloutAbort(writeContext(protected), app.ID, candidate.ID, predecessor.ID, "recover"); !errors.Is(err, state.ErrReleaseReviewChanged) {
		t.Fatalf("protected abort: %v", err)
	}
	if _, err := receipts.ServiceAbortReceipt(ctx, app.ID, attempt); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("rejected intent has receipt")
	}
	if _, err := s.UpdateProjectEnvironmentProtection(ctx, a.ID, p.ID, "staging", false); err != nil {
		t.Fatal(err)
	}
	review, err := reviews.ReadReleaseReview(ctx, app.ID, "staging")
	if err != nil {
		t.Fatal(err)
	}
	stale := review
	stale.EnvironmentID = uuid.NewString()
	if _, _, err := aborter.RequestServiceRolloutAbort(writeContext(stale), app.ID, candidate.ID, predecessor.ID, "recover"); !errors.Is(err, state.ErrReleaseReviewChanged) {
		t.Fatalf("wrong environment accepted: %v", err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for range 8 {
		wg.Go(func() {
			_, _, err := aborter.RequestServiceRolloutAbort(writeContext(review), app.ID, candidate.ID, predecessor.ID, "recover")
			results <- err
		})
	}
	wg.Wait()
	close(results)
	accepted := 0
	for err := range results {
		if err == nil {
			accepted++
		} else if !errors.Is(err, state.ErrServiceAbortAttemptAccepted) {
			t.Fatalf("competing attempt: %v", err)
		}
	}
	if accepted != 1 {
		t.Fatalf("accepted %d writes", accepted)
	}
	r, err := receipts.ServiceAbortReceipt(ctx, app.ID, attempt)
	if err != nil || r.Scope != "staging" || r.ProjectID != uuid.MustParse(p.ID).String() || r.EnvironmentID != uuid.MustParse(env.ID).String() {
		t.Fatalf("scoped receipt: %+v %v", r, err)
	}
	if _, err := s.UpdateProjectEnvironmentProtection(ctx, a.ID, p.ID, "staging", true); err != nil {
		t.Fatal(err)
	}
	later, err := receipts.ServiceAbortReceipt(ctx, app.ID, attempt)
	if err != nil || later != r {
		t.Fatalf("protection erased acceptance: %+v %v", later, err)
	}
	if err := attempts.CheckServiceAbortAttempt(ctx, app.ID, attempt, r.RequestHash); !errors.Is(err, state.ErrServiceAbortAttemptAccepted) {
		t.Fatal(err)
	}
	if _, err := s.UpdateProjectEnvironmentProtection(ctx, a.ID, p.ID, "staging", false); err != nil {
		t.Fatal(err)
	}
	for _, d := range []state.Deployment{candidate, predecessor} {
		if err := s.UpdateDeploymentStatus(ctx, d.ID, state.DeploySuperseded, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.DeleteProjectEnvironment(ctx, a.ID, p.ID, "staging"); err != nil {
		t.Fatal(err)
	}
	if _, err := receipts.ServiceAbortReceipt(ctx, app.ID, attempt); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("deleted environment disclosed receipt: %v", err)
	}
	newEnv, err := s.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: a.ID, ProjectID: p.ID, Slug: "staging"})
	if err != nil || newEnv.ID == env.ID {
		t.Fatalf("replacement environment: %+v %v", newEnv, err)
	}
	if _, err := receipts.ServiceAbortReceipt(ctx, app.ID, attempt); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("same slug retargeted historical evidence")
	}
	if err := attempts.CheckServiceAbortAttempt(ctx, app.ID, attempt, r.RequestHash); !errors.Is(err, state.ErrServiceAbortAttemptAccepted) {
		t.Fatalf("lost retained tombstone: %v", err)
	}
	newPredecessor, newCandidate := create(), create()
	if err := mark(ctx, newCandidate.ID); err != nil {
		t.Fatal(err)
	}
	newReview, err := reviews.ReadReleaseReview(ctx, app.ID, "staging")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := aborter.RequestServiceRolloutAbort(writeContext(newReview), app.ID, newCandidate.ID, newPredecessor.ID, "recover"); !errors.Is(err, state.ErrServiceAbortAttemptConflict) {
		t.Fatalf("reused old attempt in recreated scope: %v", err)
	}
}
