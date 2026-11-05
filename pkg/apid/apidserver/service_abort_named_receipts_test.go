// adr: 20261104173000
package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestServiceAbortNamedReceiptHTTP(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	s, a := *e.Store(), *e.Acct()
	ctx := t.Context()
	p, err := s.CreateProject(ctx, state.Project{AccountID: a.ID, Slug: "abort-project"})
	if err != nil {
		t.Fatal(err)
	}
	env, err := s.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: a.ID, ProjectID: p.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: a.ID, ProjectID: p.ID, Slug: "named-abort", Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	create := func() state.Deployment {
		t.Helper()
		review, err := s.ReadReleaseReview(ctx, app.ID, "staging")
		if err != nil {
			t.Fatal(err)
		}
		settings, err := state.WorkloadSettingsFromApp(app)
		if err != nil {
			t.Fatal(err)
		}
		d, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("a", 64), Scope: "staging", TrafficPercentExplicit: true, ExpectedReview: &review, ReviewedSettings: &settings})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.SetDeploymentRootfs(ctx, d.ID, "/test/"+d.ID, "test/"+d.ID, 4096); err != nil {
			t.Fatal(err)
		}
		if err := s.MarkDeploymentLive(ctx, d.ID); err != nil {
			t.Fatal(err)
		}
		return d
	}
	previous, candidate := create(), create()
	if err := s.SetDeploymentCanaryState(ctx, candidate.ID, "none", 0, 0, time.Now(), "rolling_out"); err != nil {
		t.Fatal(err)
	}
	review, err := s.ReadReleaseReview(ctx, app.ID, "staging")
	if err != nil {
		t.Fatal(err)
	}
	attempt := strings.Repeat("a", 32)
	body := api.RecoverRolloutRequest{Action: "abort", DeploymentID: candidate.ID, ExpectedPredecessorDeploymentID: previous.ID, ExpectedReview: &review, Reason: "named recovery"}
	path := "/v1/apps/" + app.Slug + "/rollouts/recover"
	headers := map[string]string{api.ServiceAbortAttemptHeader: attempt}
	bad := review
	bad.EnvironmentID = ""
	badBody := body
	badBody.ExpectedReview = &bad
	if got := e.Do(t, http.MethodPost, path, badBody, headers); got.Code != 400 {
		t.Fatalf("unbound named scope: %d %s", got.Code, got.Body)
	}
	if _, err := s.UpdateProjectEnvironmentProtection(ctx, a.ID, p.ID, "staging", true); err != nil {
		t.Fatal(err)
	}
	protected, err := s.ReadReleaseReview(ctx, app.ID, "staging")
	if err != nil {
		t.Fatal(err)
	}
	badBody.ExpectedReview = &protected
	if got := e.Do(t, http.MethodPost, path, badBody, headers); got.Code != 409 {
		t.Fatalf("protected intent admitted: %d %s", got.Code, got.Body)
	}
	if _, err := s.UpdateProjectEnvironmentProtection(ctx, a.ID, p.ID, "staging", false); err != nil {
		t.Fatal(err)
	}
	review, err = s.ReadReleaseReview(ctx, app.ID, "staging")
	if err != nil {
		t.Fatal(err)
	}
	body.ExpectedReview = &review
	handler := *e.H()
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			handler.ServeHTTP(discardPromotionReply{w}, r)
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = conn.Close()
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer b.Close()
	client := api.NewClient(b.URL, *e.Key()).SetCompletionCache(nil)
	if _, err := client.RecoverReviewedServiceRollout(api.ContextWithServiceAbortAttempt(ctx, attempt), app.Slug, candidate.ID, previous.ID, body.Reason, review); err == nil {
		t.Fatal("dropped native response succeeded")
	}
	r, err := client.GetServiceAbortReceipt(ctx, app.Slug, attempt)
	if err != nil || r.ProjectID != uuid.MustParse(p.ID).String() || r.EnvironmentID != uuid.MustParse(env.ID).String() || r.Scope != "staging" || r.RequestHash != api.ServiceAbortRequestHash(app.ID, body) {
		t.Fatalf("named receipt: %+v %v", r, err)
	}
	if _, err := s.UpdateProjectEnvironmentProtection(ctx, a.ID, p.ID, "staging", true); err != nil {
		t.Fatal(err)
	}
	if got := e.Do(t, http.MethodPost, path, body, headers); got.Code != 409 || !strings.Contains(got.Body.String(), "service_abort_attempt_accepted") {
		t.Fatalf("accepted guarded retry: %d %s", got.Code, got.Body)
	}
	if _, err := s.UpdateProjectEnvironmentProtection(ctx, a.ID, p.ID, "staging", false); err != nil {
		t.Fatal(err)
	}
	for _, d := range []state.Deployment{candidate, previous} {
		if err := s.UpdateDeploymentStatus(ctx, d.ID, state.DeploySuperseded, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.DeleteProjectEnvironment(ctx, a.ID, p.ID, "staging"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: a.ID, ProjectID: p.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	lookup := "/v1/apps/" + app.Slug + "/service-aborts/" + attempt
	if got := e.Do(t, http.MethodGet, lookup, nil, nil); got.Code != 404 {
		t.Fatalf("replacement scope disclosed evidence: %d %s", got.Code, got.Body)
	}
	if got := e.Do(t, http.MethodPost, path, body, headers); got.Code != 409 || !strings.Contains(got.Body.String(), "service_abort_attempt_accepted") {
		t.Fatalf("recreated scope lost tombstone: %d %s", got.Code, got.Body)
	}
	body.Reason = "different"
	got := e.Do(t, http.MethodPost, path, body, headers)
	var problem api.Problem
	if got.Code != 409 || json.Unmarshal(got.Body.Bytes(), &problem) != nil || problem.Code != "service_abort_attempt_conflict" {
		t.Fatalf("reused attempt after recreation: %d %s", got.Code, got.Body)
	}
}
