package apidserver

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routeprobe"
	"github.com/onebox-faas/faas/pkg/state"
)

type projectCriticalProbeSender struct{}

func (projectCriticalProbeSender) Configured() bool { return true }
func (projectCriticalProbeSender) Probe(context.Context, string, string, string, string, string) (routeprobe.Outcome, error) {
	return routeprobe.Success, nil
}
func (projectCriticalProbeSender) ProbeCritical(context.Context, string, string, string, string, string) (routeprobe.Outcome, error) {
	return routeprobe.Success, nil
}

func TestProjectVerificationAdmissionSnapshotsPolicy(t *testing.T) {
	s, store, account, key := projectSecretsTestServer(t, api.ScopesAdminOnly)
	rec := projectSecretsRequest(t, s, key, "/v1/projects", "", nil)
	if rec.Code != 200 {
		t.Fatalf("initial=%d %s", rec.Code, rec.Body)
	}
	var original api.ApplyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &original); err != nil {
		t.Fatal(err)
	}
	app, err := store.MemStore.AppByID(t.Context(), original.Builds[0].AppID)
	if err != nil {
		t.Fatal(err)
	}
	release := state.ProjectReleaseSet{ID: "previous", TTLSeconds: 1800, Members: []state.ProjectReleaseMember{{AppID: app.ID}}}
	if _, problem := s.prepareProjectReleaseVerification(t.Context(), account.ID, release); problem == nil {
		t.Fatal("unconfigured sender accepted")
	}
	s.routeProbes = projectCriticalProbeSender{}
	if _, problem := s.prepareProjectReleaseVerification(t.Context(), account.ID, release); problem == nil {
		t.Fatal("missing probe policy accepted")
	}
	zero := int64(0)
	if _, err := store.MemStore.SetRouteHealthGate(t.Context(), account.ID, app.ID, api.SetRouteHealthGateRequest{Mode: "report", ExpectedRevision: &zero, Routes: []api.RouteHealthRoute{{Method: "GET", Path: "/health", Probe: &api.RouteHealthProbe{Path: "/health"}}}}); err != nil {
		t.Fatal(err)
	}
	v, problem := s.prepareProjectReleaseVerification(t.Context(), account.ID, release)
	if problem != nil || len(v.Probes) != 1 || v.Probes[0].Slug != app.Slug {
		t.Fatalf("policy=%+v problem=%v", v, problem)
	}
	identity := safeProjectReleaseID(&api.ProjectSafeRelease{PreviousReleaseID: release.ID, Verification: v})
	copy := *v
	copy.Probes = append([]api.ProjectReleaseProbe(nil), v.Probes...)
	copy.Probes[0].Path = "/changed"
	if identity == safeProjectReleaseID(&api.ProjectSafeRelease{PreviousReleaseID: release.ID, Verification: &copy}) {
		t.Fatal("plan did not bind probe policy")
	}
	release.TTLSeconds = 30
	if _, problem := s.prepareProjectReleaseVerification(t.Context(), account.ID, release); problem == nil {
		t.Fatal("short rollback retention accepted")
	}
}

func TestProjectVerificationProbeCountsExactMembers(t *testing.T) {
	s, _, _, _ := projectSecretsTestServer(t, api.ScopesAdminOnly)
	s.routeProbes = projectCriticalProbeSender{}
	p := api.ProjectReleaseProbe{AppID: "app", Slug: "api", Method: "GET", Path: "/health"}
	receipt := api.ProjectSubmissionReceipt{Apply: api.ApplyResponse{PlanResponse: api.PlanResponse{SafeRelease: &api.ProjectSafeRelease{Verification: &api.ProjectReleaseVerification{Probes: []api.ProjectReleaseProbe{p}}}}, Builds: []api.AppliedBuild{{AppID: "app", DeploymentID: "candidate"}}}}
	baseline := state.ProjectReleaseSet{Members: []state.ProjectReleaseMember{{AppID: "app", DeploymentID: "stable"}}}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	evidence, err := s.probeProjectRelease(ctx, receipt, baseline)
	if err != nil || len(evidence) != 1 || evidence[0].CandidateRequests != 10 || evidence[0].StableRequests != 10 {
		t.Fatalf("evidence=%+v err=%v", evidence, err)
	}
	if got := state.ProjectReleaseProbeVerdict([]api.ProjectReleaseProbe{p}, evidence); got != "healthy" {
		t.Fatal(got)
	}
}
