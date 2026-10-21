// adr: 595. Exact-plan reviews bind desired and deployed environment revisions.
package state_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type standardEnvironmentReviewTestStore = state.ContractTestStore

type standardEnvironmentReviewFixture struct {
	app     state.App
	env     state.ProjectEnvironment
	spec    state.ProjectEnvironmentWorkloadSpec
	dep     state.Deployment
	request state.ApplicationStandardReviewRequest
}

func newStandardEnvironmentReviewFixture(t *testing.T, s standardEnvironmentReviewTestStore) standardEnvironmentReviewFixture {
	t.Helper()
	ctx := t.Context()
	owner, err := s.CreateAccountWithPersonalOrg(ctx, state.CreateAccountWithPersonalOrgParams{Email: "environment-review-" + uuid.NewString() + "@example.test", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	project, err := s.CreateProject(ctx, state.Project{AccountID: owner.Account.ID, Slug: "review-project"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: owner.Account.ID, OrgID: owner.PersonalOrg.ID, ProjectID: project.ID,
		Slug: "environment-review", WorkloadName: "web", Type: state.AppTypeApp, RAMMB: 128})
	if err != nil {
		t.Fatal(err)
	}
	env, err := s.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: app.AccountID, ProjectID: project.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	settings, err := state.WorkloadSettingsFromApp(app)
	if err != nil {
		t.Fatal(err)
	}
	settings.RAMMB = 256
	settings.PublicAuthBasicSealed = []byte("sealed-review-environment-credential")
	// jsonb would rewrite this numeric representation and invalidate its hash.
	settings.RetryPolicyJSON = json.RawMessage(`{"max_attempts":2,"backoff_multiplier":1e0}`)
	spec, err := s.PutProjectEnvironmentWorkloadSpec(ctx, app.AccountID, app.ProjectID, env.Slug, app.ID, 0, settings)
	if err != nil {
		t.Fatal(err)
	}
	dep, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: env.Slug, Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	v, err := s.PublishApplicationStandardVersion(ctx, state.ApplicationStandardPublish{OrgID: app.OrgID, ActorID: app.AccountID, Slug: "environment-standard",
		CreateApplicationStandardVersionRequest: api.CreateApplicationStandardVersionRequest{Definition: json.RawMessage(`{"security_policy":{"mode":"mandatory","value":"warn"}}`)}})
	if err != nil {
		t.Fatal(err)
	}
	return standardEnvironmentReviewFixture{app: app, env: env, spec: spec, dep: dep, request: state.ApplicationStandardReviewRequest{
		Scope: "application", ScopeID: app.ID, StandardID: v.StandardID, AdmissionVersion: 1, Active: true, BatchSize: 1}}
}

func (f standardEnvironmentReviewFixture) preview(t *testing.T, s standardEnvironmentReviewTestStore) state.ApplicationStandardReviewPlan {
	t.Helper()
	p, err := s.PreviewApplicationStandardAssignment(t.Context(), f.app.OrgID, f.app.AccountID, f.request)
	if err != nil || len(p.Blockers) != 0 || len(p.Applications) != 1 {
		t.Fatalf("environment review: %v %+v", err, p.Blockers)
	}
	return p
}

func (f standardEnvironmentReviewFixture) assertStale(t *testing.T, s standardEnvironmentReviewTestStore, p state.ApplicationStandardReviewPlan) {
	t.Helper()
	if _, err := s.ValidateApplicationStandardReview(t.Context(), f.app.OrgID, f.app.AccountID, p.ID, p.ApprovalHash); !errors.Is(err, state.ErrApplicationStandardReviewStale) {
		t.Fatalf("changed environment review validated: %v", err)
	}
	if _, err := s.ApproveApplicationStandardReview(t.Context(), f.app.OrgID, f.app.AccountID, p.ID, p.ApprovalHash); !errors.Is(err, state.ErrApplicationStandardReviewStale) {
		t.Fatalf("changed environment review approved: %v", err)
	}
	assignments, err := s.ListApplicationStandardAssignments(t.Context(), f.app.OrgID)
	if err != nil || len(assignments) != 0 {
		t.Fatalf("stale review changed assignments: %+v %v", assignments, err)
	}
	app, err := s.AppByID(t.Context(), f.app.ID)
	if err != nil || app.SecurityPolicy != f.app.SecurityPolicy {
		t.Fatalf("stale review changed app controls: %+v %v", app, err)
	}
}

func TestMemApplicationStandardEnvironmentReview(t *testing.T) {
	standardEnvironmentReviewRevisions(t, state.NewMemStore())
}

func standardEnvironmentReviewRevisions(t *testing.T, s standardEnvironmentReviewTestStore) {
	t.Helper()
	f := newStandardEnvironmentReviewFixture(t, s)
	p := f.preview(t, s)
	if _, err := s.ValidateApplicationStandardReview(t.Context(), f.app.OrgID, f.app.AccountID, p.ID, p.ApprovalHash); err != nil {
		t.Fatal(err)
	}
	retained, err := s.GetApplicationStandardReviewPlan(t.Context(), f.app.OrgID, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{p, (*p.ApprovalInputs()), retained, (*retained.ApprovalInputs())} {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{string(f.spec.Settings.PublicAuthBasicSealed), base64.StdEncoding.EncodeToString(f.spec.Settings.PublicAuthBasicSealed), "settings_body", "backoff_multiplier"} {
			if bytes.Contains(raw, []byte(secret)) {
				t.Fatal("saved review retained private environment settings")
			}
		}
	}
	settings := f.spec.Settings
	settings.RAMMB = 512
	second, err := s.PutProjectEnvironmentWorkloadSpec(t.Context(), f.app.AccountID, f.app.ProjectID, f.env.Slug, f.app.ID, 1, settings)
	if err != nil {
		t.Fatal(err)
	}
	f.assertStale(t, s, p)
	restored, err := s.PutProjectEnvironmentWorkloadSpec(t.Context(), f.app.AccountID, f.app.ProjectID, f.env.Slug, f.app.ID, second.Revision, f.spec.Settings)
	if err != nil || restored.Hash != f.spec.Hash || restored.ID == f.spec.ID {
		t.Fatalf("revision restore: %+v %v", restored, err)
	}
	// Identical settings in a new revision do not revive an older approval.
	f.assertStale(t, s, p)
	p = f.preview(t, s)
	if _, err := s.UpdateProjectEnvironmentProtection(t.Context(), f.app.AccountID, f.app.ProjectID, f.env.Slug, true); err != nil {
		t.Fatal(err)
	}
	f.assertStale(t, s, p)
	p = f.preview(t, s)
	if _, err := s.UpdateProjectEnvironmentProtection(t.Context(), f.app.AccountID, f.app.ProjectID, f.env.Slug, false); err != nil {
		t.Fatal(err)
	}
	f.assertStale(t, s, p)
	p = f.preview(t, s)
	if err := s.DeleteProjectEnvironment(t.Context(), f.app.AccountID, f.app.ProjectID, f.env.Slug); err != nil {
		t.Fatal(err)
	}
	env, err := s.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: f.app.AccountID, ProjectID: f.app.ProjectID, Slug: f.env.Slug})
	if err != nil || env.ID == f.env.ID {
		t.Fatalf("environment lifetime reused: %+v %v", env, err)
	}
	if _, err := s.PutProjectEnvironmentWorkloadSpec(t.Context(), f.app.AccountID, f.app.ProjectID, env.Slug, f.app.ID, 0, f.spec.Settings); err != nil {
		t.Fatal(err)
	}
	f.assertStale(t, s, p)
	current := f.preview(t, s)
	if _, err := s.ValidateApplicationStandardReview(t.Context(), f.app.OrgID, f.app.AccountID, current.ID, current.ApprovalHash); err != nil {
		t.Fatal(err)
	}
}

func TestMemApplicationStandardEnvironmentReviewPinAndBody(t *testing.T) {
	m := state.NewMemStore()
	standardEnvironmentReviewPinAndBody(t, m, func(f standardEnvironmentReviewFixture, specID string) {
		(*m.Mu()).Lock()
		defer (*m.Mu()).Unlock()
		(*m.ProjectEnvironmentWorkloadDeploymentSpecs())[f.dep.ID] = specID
	}, func(f standardEnvironmentReviewFixture) {
		(*m.Mu()).Lock()
		defer (*m.Mu()).Unlock()
		spec := (*m.ProjectEnvironmentWorkloadSpecs())[f.spec.ID]
		spec.Settings.RAMMB = 1024
		(*m.ProjectEnvironmentWorkloadSpecs())[spec.ID] = spec
	})
}

func standardEnvironmentReviewPinAndBody(t *testing.T, s standardEnvironmentReviewTestStore, pin func(standardEnvironmentReviewFixture, string), corrupt func(standardEnvironmentReviewFixture)) {
	t.Helper()
	f := newStandardEnvironmentReviewFixture(t, s)
	settings := f.spec.Settings
	settings.RAMMB = 512
	second, err := s.PutProjectEnvironmentWorkloadSpec(t.Context(), f.app.AccountID, f.app.ProjectID, f.env.Slug, f.app.ID, 1, settings)
	if err != nil {
		t.Fatal(err)
	}
	p := f.preview(t, s)
	pin(f, second.ID)
	f.assertStale(t, s, p)
	// The old revision remains retained by the deployment while the head is new.
	pin(f, f.spec.ID)
	p = f.preview(t, s)
	corrupt(f)
	f.assertStale(t, s, p)
	if _, err := s.PreviewApplicationStandardAssignment(t.Context(), f.app.OrgID, f.app.AccountID, f.request); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("corrupt retained settings accepted: %v", err)
	}
}

func TestMemApplicationStandardEnvironmentMaterializationReview(t *testing.T) {
	standardEnvironmentMaterializationReview(t, state.NewMemStore())
}

func standardEnvironmentMaterializationReview(t *testing.T, s standardEnvironmentReviewTestStore) {
	t.Helper()
	standardEnvironmentMaterializationDrift(t, s, func(f standardEnvironmentReviewFixture) {
		settings := f.spec.Settings
		settings.RAMMB = 512
		if _, err := s.PutProjectEnvironmentWorkloadSpec(t.Context(), f.app.AccountID, f.app.ProjectID, f.env.Slug, f.app.ID, 1, settings); err != nil {
			t.Fatal(err)
		}
	})
}

func standardEnvironmentMaterializationDrift(t *testing.T, s standardEnvironmentReviewTestStore, change func(standardEnvironmentReviewFixture)) {
	t.Helper()
	f := newStandardEnvironmentReviewFixture(t, s)
	p := f.preview(t, s)
	if _, err := s.ApproveApplicationStandardReview(t.Context(), f.app.OrgID, f.app.AccountID, p.ID, p.ApprovalHash); err != nil {
		t.Fatal(err)
	}
	change(f)
	for range 2 {
		c, err := s.ClaimApplicationStandardOperation(t.Context(), "environment-review-worker")
		if err != nil {
			t.Fatal(err)
		}
		o, err := s.MaterializeNextApplicationStandardTarget(t.Context(), c)
		if err != nil || len(o.Targets) != 1 || o.Targets[0].State != "blocked" || o.Targets[0].ErrorCode != "reviewed_inputs_changed" {
			t.Fatalf("environment drift crossed materialization: %+v %v", o, err)
		}
		app, err := s.AppByID(t.Context(), f.app.ID)
		if err != nil || app.SecurityPolicy != f.app.SecurityPolicy {
			t.Fatalf("stale projection changed root controls: %+v %v", app, err)
		}
		e, err := s.GetApplicationStandardEnrollment(t.Context(), f.app.OrgID, f.app.ID)
		if err != nil || e.DesiredRevision != 1 || e.PersistedRevision != 0 {
			t.Fatalf("stale projection changed checkpoint: %+v %v", e, err)
		}
	}
}

func TestMemApplicationStandardEnvironmentMaterializationCorruptBody(t *testing.T) {
	m := state.NewMemStore()
	standardEnvironmentMaterializationDrift(t, m, func(f standardEnvironmentReviewFixture) {
		(*m.Mu()).Lock()
		defer (*m.Mu()).Unlock()
		spec := (*m.ProjectEnvironmentWorkloadSpecs())[f.spec.ID]
		spec.Settings.RAMMB = 1024
		(*m.ProjectEnvironmentWorkloadSpecs())[spec.ID] = spec
	})
}

func TestApplicationStandardEnvironmentReviewRejectsUnverifiedIdentity(t *testing.T) {
	m := state.NewMemStore()
	f := newStandardEnvironmentReviewFixture(t, m)
	w, err := state.MakeStandardReviewEnvironmentWorkload(f.spec, f.env, "deployed", f.dep.ID, f.dep.Scope)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*state.StandardReviewEnvironmentWorkload)
	}{
		{"missing body", func(w *state.StandardReviewEnvironmentWorkload) { w.Settings = nil }},
		{"foreign app", func(w *state.StandardReviewEnvironmentWorkload) { w.AppID = uuid.NewString() }},
		{"foreign owner", func(w *state.StandardReviewEnvironmentWorkload) { w.AccountID = uuid.NewString() }},
		{"foreign project", func(w *state.StandardReviewEnvironmentWorkload) { w.ProjectID = uuid.NewString() }},
		{"wrong scope", func(w *state.StandardReviewEnvironmentWorkload) { w.DeploymentScope = "production" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copy := w
			tc.mutate(&copy)
			app := state.StandardReviewAppSnapshot{AppID: f.app.ID, AccountID: f.app.AccountID, ProjectID: f.app.ProjectID, EnvironmentWorkloads: []state.StandardReviewEnvironmentWorkload{copy}}
			if err := state.NormalizeStandardReviewEnvironmentWorkloads(&app); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("unverified input accepted: %v", err)
			}
		})
	}
}

func TestApplicationStandardEnvironmentReviewPrivateVerification(t *testing.T) {
	m := state.NewMemStore()
	f := newStandardEnvironmentReviewFixture(t, m)
	(*m.Mu()).Lock()
	snapshot, err := m.StandardReviewSnapshotLocked(t.Context(), f.app.OrgID, f.app.AccountID, f.request)
	(*m.Mu()).Unlock()
	if err != nil {
		t.Fatal(err)
	}
	first, err := state.NormalizeStandardReviewSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	second, err := state.NormalizeStandardReviewSnapshot(first)
	if err != nil {
		t.Fatalf("private verified snapshot cannot be reused: %v", err)
	}
	a, _ := state.StandardReviewDigest(first)
	b, _ := state.StandardReviewDigest(second)
	if a != b || len(first.Applications[0].EnvironmentWorkloads) != 2 {
		t.Fatal("normalization changed verified environment provenance")
	}
	if snapshot.Applications[0].EnvironmentWorkloads[0].Settings == nil {
		t.Fatal("normalization modified the caller's private snapshot")
	}
	raw, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(base64.StdEncoding.EncodeToString(f.spec.Settings.PublicAuthBasicSealed))) {
		t.Fatal("verified snapshot retained sealed settings")
	}
	var supplied state.StandardReviewSnapshot
	if err := json.Unmarshal(raw, &supplied); err != nil {
		t.Fatal(err)
	}
	if _, err := state.NormalizeStandardReviewSnapshot(supplied); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("serialized descriptor supplied private verification authority: %v", err)
	}
}
