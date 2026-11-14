// adr: 20261019071733 amendment 13 — an unreadable freeze refuses the release.
package apidserver_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

// unreadableFreezes fails every freeze read while fail is set.
type unreadableFreezes struct {
	MemStore *state.MemStore
	fail     bool
}

func (s *unreadableFreezes) ListReleaseFreezes(ctx context.Context, owner state.ReleaseFreezeOwner, now time.Time) ([]state.ReleaseFreeze, error) {
	if s.fail {
		return nil, errors.New("freeze lookup failure")
	}
	return s.MemStore.ListReleaseFreezes(ctx, owner, now)
}

// failFreezeReads swaps the server's store for one whose freeze reads fail
// until the returned switch is flipped back.
func failFreezeReads(e apidserver.TestEnv) *unreadableFreezes {
	fake := &unreadableFreezes{MemStore: *e.Store(), fail: true}
	*(*e.S()).Store() = statetest.NewMem(fake)
	return fake
}

func wantFreezeReadRefusal(t *testing.T, code int, body []byte, what string) {
	t.Helper()
	if code != http.StatusServiceUnavailable || guardProblemCode(t, body) != api.CodeCapacity {
		t.Fatalf("%s with unreadable freezes = %d %s, want 503 %s", what, code, body, api.CodeCapacity)
	}
}

// A database error used to lift every freeze: a deploy, a retry and a GitOps
// activation released when the freezes could not be read (deploy review
// 2026-11-10). Each now refuses with a retryable error and releases once the
// freezes read again.
func TestUnreadableFreezesRefuseProductionDeploy(t *testing.T) {
	f := newPostReleaseFixture(t, "frz-unreadable", true, time.Hour)
	fake := failFreezeReads(f.e)
	rec := guardDo(t, f.e, http.MethodPost, "/v1/apps/frz-unreadable/deployments", api.CreateDeploymentRequest{Image: imageRef('a')}, nil)
	wantFreezeReadRefusal(t, rec.Code, rec.Body.Bytes(), "deploy")
	// An override cannot stand in for freezes nobody could read.
	rec = guardDo(t, f.e, http.MethodPost, "/v1/apps/frz-unreadable/deployments",
		api.CreateDeploymentRequest{Image: imageRef('b'), OverrideFreeze: "incident 4711"}, nil)
	wantFreezeReadRefusal(t, rec.Code, rec.Body.Bytes(), "overridden deploy")
	fake.fail = false
	if rec := guardDo(t, f.e, http.MethodPost, "/v1/apps/frz-unreadable/deployments", api.CreateDeploymentRequest{Image: imageRef('c')}, nil); rec.Code >= 300 {
		t.Fatalf("deploy once freezes read again = %d %s", rec.Code, rec.Body)
	}
}

func TestUnreadableFreezesRefuseRetry(t *testing.T) {
	e := apidserver.Setup(t, api.PlanScale)
	t.Setenv("FAAS_STORAGE_BACKEND", "local")
	t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
	store := *e.Store()
	app, err := store.CreateApp(t.Context(), state.App{AccountID: (*e.Acct()).ID, Slug: "retry-unreadable", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	dep := guardFailedSourceDeployment(t, store, app.ID, "")
	fake := failFreezeReads(e)
	r := e.Do(t, http.MethodPost, "/v1/deployments/"+dep.ID+"/retry", api.RetryDeploymentRequest{FromStage: "source_download"}, nil)
	wantFreezeReadRefusal(t, r.Code, r.Body.Bytes(), "retry")
	if latest, err := store.LatestDeployment(t.Context(), app.ID); err != nil || latest.ID != dep.ID {
		t.Fatalf("a refused retry created a deployment: %s %v", latest.ID, err)
	}
	fake.fail = false
	if r := e.Do(t, http.MethodPost, "/v1/deployments/"+dep.ID+"/retry", api.RetryDeploymentRequest{FromStage: "source_download"}, nil); r.Code != http.StatusAccepted {
		t.Fatalf("retry once freezes read again = %d %s", r.Code, r.Body)
	}
}

func TestUnreadableFreezesHoldGitOpsActivation(t *testing.T) {
	e := apidserver.Setup(t, api.PlanScale)
	store := *e.Store()
	app, err := store.CreateApp(t.Context(), state.App{AccountID: (*e.Acct()).ID, Slug: "gitops-unreadable", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	guardLiveDeployment(t, store, app.ID, "production")
	graph := state.EnvironmentWorkloadGraph{ID: "graph", Members: []state.EnvironmentWorkloadGraphMember{{AppID: app.ID, CandidateDeploymentID: "candidate"}}}
	var lease state.EnvironmentGitOpsLease
	lease.Source.EnvironmentSlug = "production"
	lease.Source.Spec.ApprovalPolicy = "protected_branch"
	fake := failFreezeReads(e)
	if hold, err := (*e.S()).GitOpsReleaseHoldForTest(t.Context(), lease, graph); err == nil {
		t.Fatalf("activation with unreadable freezes = %q, nil; want an error the lease retries", hold)
	}
	fake.fail = false
	if hold, err := (*e.S()).GitOpsReleaseHoldForTest(t.Context(), lease, graph); err != nil || hold != "" {
		t.Fatalf("activation once freezes read again = %q, %v", hold, err)
	}
}
