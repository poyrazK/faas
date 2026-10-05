// adr: 435 — exercise the real reviewed admission and automatic repair stores.
package sched

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func approveEgressAdmission(t *testing.T, s *state.MemStore, owner state.CreateAccountWithPersonalOrgResult) {
	t.Helper()
	v, err := s.PublishApplicationStandardVersion(t.Context(), state.ApplicationStandardPublish{OrgID: owner.PersonalOrg.ID, ActorID: owner.Account.ID, Slug: "runtime-company", CreateApplicationStandardVersionRequest: api.CreateApplicationStandardVersionRequest{Definition: json.RawMessage(`{"egress_cidrs":{"mode":"mandatory","value":["8.8.8.0/24"]}}`)}})
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.PreviewApplicationStandardAssignment(t.Context(), owner.PersonalOrg.ID, owner.Account.ID, state.ApplicationStandardReviewRequest{Scope: "organization", ScopeID: owner.PersonalOrg.ID, StandardID: v.StandardID, AdmissionVersion: 1, Active: true, BatchSize: 1})
	if err != nil || len(p.Blockers) != 0 {
		t.Fatalf("preview blockers=%+v err=%v", p.Blockers, err)
	}
	if _, err := s.ApproveApplicationStandardReview(t.Context(), p.OrgID, owner.Account.ID, p.ID, p.ApprovalHash); err != nil {
		t.Fatal(err)
	}
}

func standardAdmissionOwner(t *testing.T, s *state.MemStore) state.CreateAccountWithPersonalOrgResult {
	t.Helper()
	o, err := s.CreateAccountWithPersonalOrg(t.Context(), state.CreateAccountWithPersonalOrgParams{Email: "runtime-standards@example.com", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func TestApplicationStandardPendingRestoreRejectsRuntimeEntryPoints(t *testing.T) {
	s := state.NewMemStore()
	o := standardAdmissionOwner(t, s)
	app, err := s.CreateApp(t.Context(), state.App{AccountID: o.Account.ID, OrgID: o.PersonalOrg.ID, Slug: "runtime-restored", RAMMB: 128, MaxConcurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := s.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc", Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	vmm := &fakeVMM{}
	e := newEngine(t, s, vmm, &fakeNotifier{}, "1.10.0")
	initial, err := e.Wake(t.Context(), app.ID, dep.ID, "", TriggerGateway)
	if err != nil || initial.InstanceID == "" {
		t.Fatalf("initial wake=%+v err=%v", initial, err)
	}
	approveEgressAdmission(t, s, o)
	if _, err := s.ScheduleAppDeletion(t.Context(), app.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	app, err = s.RestoreApp(t.Context(), app.ID, api.MustLimitsFor(api.PlanPro))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunningInstanceForApp(t.Context(), app.ID); err != nil {
		t.Fatalf("fixture must retain a running instance to exercise reuse: %v", err)
	}
	for _, tc := range []struct {
		name string
		call func() error
	}{
		{"reuse running instance", func() error { _, err := e.Wake(t.Context(), app.ID, dep.ID, "", TriggerGateway); return err }},
		{"new admission", func() error { _, err := e.AdmitInstance(t.Context(), app.ID, dep.ID, "", TriggerGateway); return err }},
		{"explicit deployment", func() error {
			_, err := e.AdmitInstanceForDeployment(t.Context(), app.ID, dep.ID, "", TriggerDeploymentSmoke)
			return err
		}},
		{"coordinated wake", func() error { _, err := e.EnsureWake(t.Context(), app.ID, TriggerGateway); return err }},
		{"prime", func() error { return e.Prime(t.Context(), app.ID, dep.ID) }},
		{"runtime account resolution", func() error { _, _, _, err := e.resolveAppAccount(t.Context(), app.ID); return err }},
		{"new deployment settings", func() error { _, _, _, err := e.resolveAppForDeploy(t.Context(), app.ID); return err }},
		{"live deployment settings", func() error { _, _, _, _, err := e.resolveApp(t.Context(), app.ID); return err }},
		{"migration spec", func() error { _, err := e.BuildAppSpecForMigration(t.Context(), initial.InstanceID); return err }},
		{"warm pool", func() error { return e.ReconcileWarmPool(t.Context(), app.ID) }},
		{"restart", func() error { _, err := e.RestartApp(t.Context(), app.ID, uuid.NewString()); return err }},
		{"runtime refresh", func() error { _, err := e.RefreshRuntimeConfig(t.Context(), app.ID, uuid.NewString()); return err }},
		{"app task", func() error {
			_, err := e.ResolveAppTaskRuntime(t.Context(), AppTaskRestoreRequest{ID: uuid.NewString(), AccountID: o.Account.ID, AppID: app.ID, DeploymentID: dep.ID})
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			var p *api.Problem
			if !errors.Is(err, state.ErrApplicationStandardsPending) || !errors.As(err, &p) || p.Code != api.CodeApplicationStandardsPending {
				t.Fatalf("pending standard bypassed: %v", err)
			}
		})
	}
	if vmm.coldBoots != 1 || vmm.restores != 0 || vmm.destroys != 0 || vmm.snapshots != 0 || vmm.adopts != 0 {
		t.Fatalf("denied entry point changed VM lifecycle: %+v", vmm)
	}
	instances, err := s.ListInstancesForApp(t.Context(), app.ID)
	if err != nil || len(instances) != 1 || instances[0].State != string(state.StateRunning) {
		t.Fatalf("denied admission changed instances: %+v err=%v", instances, err)
	}
}

func TestApplicationStandardAutomaticPersistencePermitsWakeWithoutObservation(t *testing.T) {
	s := state.NewMemStore()
	o := standardAdmissionOwner(t, s)
	approveEgressAdmission(t, s, o)
	c, err := s.ClaimApplicationStandardOperation(t.Context(), "empty-admission")
	if err != nil {
		t.Fatal(err)
	}
	operation, err := s.MaterializeNextApplicationStandardTarget(t.Context(), c)
	if err != nil || operation.State != "completed" || len(operation.Targets) != 0 {
		t.Fatalf("empty operation=%+v err=%v", operation, err)
	}
	app, err := s.CreateApp(t.Context(), state.App{AccountID: o.Account.ID, OrgID: o.PersonalOrg.ID, Slug: "automatic-runtime", RAMMB: 128, MaxConcurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	e := newEngine(t, s, newStandardNativeTestVMM(t, s, &fakeVMM{}, standardNativeTestNodeID(t, s)), &fakeNotifier{}, "1.10.0")
	if _, err := e.Wake(t.Context(), app.ID, "", "", TriggerGateway); !errors.Is(err, state.ErrApplicationStandardsPending) {
		t.Fatalf("pending fresh service wake=%v", err)
	}
	claim, err := s.ClaimApplicationStandardEnrollment(t.Context(), "runtime-repair")
	if err != nil {
		t.Fatal(err)
	}
	enrollment, err := s.MaterializeApplicationStandardEnrollment(t.Context(), claim)
	if err != nil || enrollment.PersistedRevision != enrollment.DesiredRevision || enrollment.ObservedRevision != 0 {
		t.Fatalf("installation=%+v err=%v", enrollment, err)
	}
	dep, err := s.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc", Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	out, err := e.Wake(t.Context(), app.ID, dep.ID, "", TriggerGateway)
	if err != nil || out.InstanceID == "" {
		t.Fatalf("persisted service wake=%+v err=%v", out, err)
	}
	if _, err := e.BuildAppSpecForMigration(t.Context(), out.InstanceID); err != nil {
		t.Fatalf("persisted migration: %v", err)
	}
	after, err := s.GetApplicationStandardEnrollment(context.Background(), app.OrgID, app.ID)
	if err != nil || after.ObservedRevision != 0 || after.DesiredRevision != enrollment.DesiredRevision {
		t.Fatalf("runtime admission invented observation: %+v err=%v", after, err)
	}
}

type enrollmentReadFailureStore struct {
	*state.MemStore
	failure error
}

func (s *enrollmentReadFailureStore) GetApplicationStandardEnrollment(context.Context, string, string) (state.ApplicationStandardEnrollment, error) {
	return state.ApplicationStandardEnrollment{}, s.failure
}

type admissionStoreWithoutReader struct{ state.Store }

func TestApplicationStandardRuntimeReadFailsClosed(t *testing.T) {
	base := state.NewMemStore()
	o := standardAdmissionOwner(t, base)
	app, err := base.CreateApp(t.Context(), state.App{AccountID: o.Account.ID, OrgID: o.PersonalOrg.ID, Slug: "admission-read-error", RAMMB: 128})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		store   state.Store
		pending bool
	}{
		{"missing enrollment", &enrollmentReadFailureStore{MemStore: base, failure: state.ErrNotFound}, true},
		{"storage unavailable", &enrollmentReadFailureStore{MemStore: base, failure: errors.New("storage unavailable")}, false},
		{"missing reader capability", &admissionStoreWithoutReader{Store: base}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vmm := &fakeVMM{}
			e := newEngine(t, tc.store, vmm, &fakeNotifier{}, "1.10.0")
			_, err := e.Wake(t.Context(), app.ID, "", "", TriggerGateway)
			if err == nil || errors.Is(err, state.ErrApplicationStandardsPending) != tc.pending {
				t.Fatalf("storage failure bypassed admission: %v", err)
			}
			if vmm.coldBoots != 0 || vmm.restores != 0 {
				t.Fatal("read failure booted a VM")
			}
		})
	}
}
