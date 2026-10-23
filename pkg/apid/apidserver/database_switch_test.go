// adr: 20261023181931 — a rollback may restore the database; the request, routes and guard.
package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

type switchDrain struct{}

func (switchDrain) PreviewDrainBlockers(context.Context, string) (state.DrainPreview, error) {
	return state.DrainPreview{}, nil
}

func (switchDrain) DrainPlanForCutover(context.Context, string, string, time.Time) (state.ManagedPostgresDrainPlan, bool, error) {
	return state.ManagedPostgresDrainPlan{}, false, nil
}

// switchFixture is an app with a managed database bound for migrations and
// v1 (no release command) superseded by v2 and v3, whose release commands
// ran while v1 and v2 served: a rollback to v1 crosses v3's release.
type switchFixture struct {
	env        apidserver.SourceRefTestEnv
	slug       string
	sourceID   string
	v1, v2, v3 state.Deployment
}

func newSwitchFixture(t *testing.T) switchFixture {
	t.Helper()
	env, _, prepare := cutoverAPIFixture(t)
	env.SetDatabaseSwitchDrain(switchDrain{})
	if !env.DatabaseSwitchWorkerConfigured() {
		t.Fatal("database switch worker not configured")
	}
	ctx := context.Background()
	store := *env.Store()
	app, err := store.AppByID(ctx, *env.AppID())
	if err != nil {
		t.Fatal(err)
	}
	deploy := func(command []string) state.Deployment {
		t.Helper()
		d, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage,
			ImageDigest: "sha256:switch" + time.Now().Format("150405.000000000"), Status: state.DeploySnapshotting, ReleaseCommand: command})
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range []state.DeploymentStatus{state.DeployBuilding, state.DeployImaging, state.DeploySnapshotting} {
			if err := store.UpdateDeploymentStatus(ctx, d.ID, s, ""); err != nil {
				t.Fatal(err)
			}
		}
		if err := store.SetDeploymentRootfs(ctx, d.ID, "/tmp/r.ext4", "apps/"+app.Slug+"/"+d.ID+".ext4", 4096); err != nil {
			t.Fatal(err)
		}
		if len(command) > 0 {
			runRelease(ctx, t, store, *env.AcctID(), app.ID, d.ID)
		}
		if err := store.MarkDeploymentLive(ctx, d.ID); err != nil {
			t.Fatal(err)
		}
		got, err := store.DeploymentByID(ctx, d.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	f := switchFixture{env: env, slug: app.Slug, sourceID: prepare.SourceDatabaseID}
	f.v1 = deploy(nil)
	f.v2 = deploy([]string{"bin/migrate"})
	f.v3 = deploy([]string{"bin/migrate"})
	return f
}

func (f switchFixture) do(t *testing.T, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	if method == http.MethodPost {
		return f.env.Post(t, path, body)
	}
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("Authorization", "Bearer "+(*f.env.Key()))
	w := httptest.NewRecorder()
	(*f.env.H()).ServeHTTP(w, req)
	return w
}

func problemCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var p api.Problem
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatalf("problem body %s: %v", w.Body.String(), err)
	}
	return p.Code
}

func TestDatabaseSwitchRollbackRecordsTheSwitchWithoutTouchingTheRelease(t *testing.T) {
	f := newSwitchFixture(t)
	rollback := "/v1/apps/" + f.slug + "/rollback"
	w := f.do(t, http.MethodPost, rollback, api.RollbackRequest{TargetDeploymentID: &f.v1.ID, RestoreDatabases: true})
	if w.Code != http.StatusConflict || problemCode(t, w) != api.CodeRollbackCrossesRelease {
		t.Fatalf("unacknowledged switch = %d %s, want rollback_crosses_release", w.Code, w.Body)
	}
	w = f.do(t, http.MethodPost, rollback, api.RollbackRequest{TargetDeploymentID: &f.v1.ID, RestoreDatabases: true, AcknowledgeReleases: true})
	if w.Code != http.StatusAccepted {
		t.Fatalf("switch = %d %s, want 202", w.Code, w.Body)
	}
	var resp api.DeploymentResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || resp.DatabaseSwitch == nil {
		t.Fatalf("response %s: %v", w.Body, err)
	}
	sw := *resp.DatabaseSwitch
	if sw.Phase != "restoring" || sw.FromDeploymentID != f.v3.ID || sw.TargetDeploymentID != f.v1.ID || sw.TargetRevision != f.v1.Revision ||
		!slices.Equal(sw.UndoneDeploymentIDs, []string{f.v3.ID}) || sw.SourceDatabaseID != f.sourceID ||
		sw.SourceDatabaseName != "orders" || sw.TargetName != "orders-before-v3" || sw.RestorePoint == "" {
		t.Fatalf("switch %+v", sw)
	}
	store := *f.env.Store()
	ctx := context.Background()
	for _, d := range []state.Deployment{f.v1, f.v3} {
		got, err := store.DeploymentByID(ctx, d.ID)
		if err != nil || got.Status != d.Status {
			t.Fatalf("the request changed %s: %s -> %s (%v)", d.ID, d.Status, got.Status, err)
		}
	}

	w = f.do(t, http.MethodPost, rollback, api.RollbackRequest{TargetDeploymentID: &f.v1.ID, RestoreDatabases: true, AcknowledgeReleases: true})
	if w.Code != http.StatusConflict || problemCode(t, w) != api.CodeDatabaseSwitchInProgress {
		t.Fatalf("second switch = %d %s", w.Code, w.Body)
	}
	w = f.do(t, http.MethodGet, "/v1/apps/"+f.slug+"/database-switches", nil)
	var list api.DatabaseSwitchList
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &list) != nil || len(list.Items) != 1 || list.Items[0].ID != sw.ID {
		t.Fatalf("list = %d %s", w.Code, w.Body)
	}
	w = f.do(t, http.MethodGet, "/v1/apps/"+f.slug+"/database-switches/"+sw.ID, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get = %d %s", w.Code, w.Body)
	}
	w = f.do(t, http.MethodPost, "/v1/apps/"+f.slug+"/database-switches/"+sw.ID+"/cancel", nil)
	var cancelled api.DatabaseSwitch
	if w.Code != http.StatusAccepted || json.Unmarshal(w.Body.Bytes(), &cancelled) != nil || cancelled.CancelRequestedAt == "" {
		t.Fatalf("cancel = %d %s", w.Code, w.Body)
	}
	if w = f.do(t, http.MethodGet, "/v1/apps/"+f.slug+"/database-switches/00000000-0000-4000-8000-000000000000", nil); w.Code != http.StatusNotFound {
		t.Fatalf("unknown switch = %d", w.Code)
	}
}

func TestDatabaseSwitchRollbackRefusals(t *testing.T) {
	f := newSwitchFixture(t)
	rollback := "/v1/apps/" + f.slug + "/rollback"
	current := f.v3.ID
	w := f.do(t, http.MethodPost, rollback, api.RollbackRequest{TargetDeploymentID: &f.v1.ID, RestoreDatabases: true,
		AcknowledgeReleases: true, ExpectedCurrentDeploymentID: &current})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("checked switch = %d %s, want 400", w.Code, w.Body)
	}
	// v3's release ran while v2 served: nothing to restore.
	w = f.do(t, http.MethodPost, rollback, api.RollbackRequest{TargetDeploymentID: &f.v2.ID, RestoreDatabases: true, AcknowledgeReleases: true})
	if w.Code != http.StatusConflict || problemCode(t, w) != api.CodeDatabaseSwitchUnavailable {
		t.Fatalf("switch crossing nothing = %d %s", w.Code, w.Body)
	}

	plain := newReleaseHistory(t, "switch-unconfigured")
	if code, p := plain.rollback(t, api.RollbackRequest{TargetDeploymentID: &plain.v1.ID, RestoreDatabases: true, AcknowledgeReleases: true}); code != http.StatusConflict ||
		p.Code != api.CodeDatabaseSwitchUnavailable {
		t.Fatalf("switch without managed PostgreSQL = %d %+v", code, p)
	}
}

// completeSwitch records a completed switch from v3 back to v1 and makes v1
// live, as the worker leaves them.
func completeSwitch(t *testing.T, f switchFixture) {
	t.Helper()
	ctx := context.Background()
	store := *f.env.Store()
	now := time.Now().UTC()
	sw, err := store.CreateDatabaseSwitch(ctx, state.DatabaseSwitch{AccountID: *f.env.AcctID(), AppID: *f.env.AppID(), Scope: "default",
		FromDeploymentID: f.v3.ID, TargetDeploymentID: f.v1.ID, UndoneDeploymentIDs: []string{f.v3.ID},
		SourceDatabaseID: f.sourceID, TargetName: "orders-before-v3", RestorePoint: now.Add(-time.Minute), CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimDueDatabaseSwitches(ctx, "test", now.Add(time.Second), now.Add(time.Minute), 1)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim %v %v", claimed, err)
	}
	done := claimed[0]
	done.Phase, done.TargetDatabaseID, done.CutoverID = state.DatabaseSwitchCompleted, sw.SourceDatabaseID, sw.ID
	done.ActivatedAt, done.ReleaseSwitchedAt, done.FinishedAt = &now, &now, &now
	if _, err := store.SaveDatabaseSwitch(ctx, done, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PrepareDeploymentRollback(ctx, *f.env.AppID(), f.v1.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, f.v1.ID); err != nil {
		t.Fatal(err)
	}
}

func TestRollbackToAReleaseASwitchUndidIsRefused(t *testing.T) {
	f := newSwitchFixture(t)
	completeSwitch(t, f)
	w := f.do(t, http.MethodPost, "/v1/apps/"+f.slug+"/rollback", api.RollbackRequest{TargetDeploymentID: &f.v3.ID, AcknowledgeReleases: true})
	if w.Code != http.StatusConflict || problemCode(t, w) != api.CodeRollbackTargetDatabaseRestored {
		t.Fatalf("rollback to an undone release = %d %s", w.Code, w.Body)
	}
	// v2's release is still in the database; v3's undone one is no longer
	// listed as crossed.
	w = f.do(t, http.MethodPost, "/v1/apps/"+f.slug+"/rollback", api.RollbackRequest{TargetDeploymentID: &f.v2.ID})
	if w.Code == http.StatusConflict && problemCode(t, w) == api.CodeRollbackCrossesRelease {
		t.Fatalf("rollback to v2 still crosses the undone release: %s", w.Body)
	}
}
