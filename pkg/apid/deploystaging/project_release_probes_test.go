package deploystaging

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routeprobe"
	"github.com/onebox-faas/faas/pkg/state"
)

type projectProbeTestSender struct {
	mu        sync.Mutex
	tokens    map[string]string
	candidate routeprobe.Outcome
	calls     int
}

func (*projectProbeTestSender) Configured() bool { return true }
func (s *projectProbeTestSender) ProbeCritical(_ context.Context, slug, id, token, method, path string) (routeprobe.Outcome, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if slug != "api" || method != "GET" || path != "/health" || token == "" || token != s.tokens[id] {
		return routeprobe.Unattributed, fmt.Errorf("wrong critical probe target")
	}
	if id == "candidate" {
		return s.candidate, nil
	}
	if id == "baseline" {
		return routeprobe.Success, nil
	}
	return routeprobe.Unattributed, fmt.Errorf("unknown deployment")
}
func TestProjectCriticalProbeChallengesAndAttribution(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		outcome    routeprobe.Outcome
	}{
		{"healthy", "healthy", routeprobe.Success}, {"candidate failing", "regressed", routeprobe.ServerError}, {"missing auth", "unknown", routeprobe.Unauthenticated}, {"wrong status", "unknown", routeprobe.UnexpectedStatus}, {"unattributed", "unknown", routeprobe.Unattributed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sender := &projectProbeTestSender{tokens: map[string]string{}, candidate: tc.outcome}
			probe := api.ProjectReleaseProbe{AppID: "app", Slug: "api", Method: "GET", Path: "/health"}
			receipt := api.ProjectSubmissionReceipt{Apply: api.ApplyResponse{PlanResponse: api.PlanResponse{SafeRelease: &api.ProjectSafeRelease{Verification: &api.ProjectReleaseVerification{Probes: []api.ProjectReleaseProbe{probe}}}}, Builds: []api.AppliedBuild{{AppID: "app", DeploymentID: "candidate"}}}}
			baseline := state.ProjectReleaseSet{Members: []state.ProjectReleaseMember{{AppID: "app", DeploymentID: "baseline"}}}
			notify := func(_ context.Context, payload string) error {
				var challenge struct {
					Scope        string `json:"scope"`
					AppID        string `json:"app_id"`
					DeploymentID string `json:"deployment_id"`
					Token        string `json:"token"`
				}
				if err := json.Unmarshal([]byte(payload), &challenge); err != nil {
					return err
				}
				if challenge.Scope != "production" || challenge.AppID != "app" {
					return fmt.Errorf("unscoped challenge")
				}
				sender.tokens[challenge.DeploymentID] = challenge.Token
				return nil
			}
			evidence, err := ProbeProjectRelease(t.Context(), receipt, baseline, sender, notify, 0)
			if err != nil {
				t.Fatal(err)
			}
			if len(sender.tokens) != 2 || sender.calls != 20 {
				t.Fatalf("challenges=%d requests=%d", len(sender.tokens), sender.calls)
			}
			if got := state.ProjectReleaseProbeVerdict([]api.ProjectReleaseProbe{probe}, evidence); got != tc.want {
				t.Fatalf("verdict=%s want=%s evidence=%+v", got, tc.want, evidence)
			}
		})
	}
}

func TestContractProbesCurrentGraphOnce(t *testing.T) {
	for _, outcome := range []routeprobe.Outcome{routeprobe.Success, routeprobe.ServerError, routeprobe.Unauthenticated} {
		t.Run(fmt.Sprintf("outcome-%d", outcome), func(t *testing.T) {
			sender := &projectProbeTestSender{tokens: map[string]string{}, candidate: outcome}
			probe := api.ProjectReleaseProbe{AppID: "app", Slug: "api", Method: "GET", Path: "/health"}
			receipt := api.ProjectSubmissionReceipt{Apply: api.ApplyResponse{PlanResponse: api.PlanResponse{SafeRelease: &api.ProjectSafeRelease{Verification: &api.ProjectReleaseVerification{Probes: []api.ProjectReleaseProbe{probe}}}}, Builds: []api.AppliedBuild{{AppID: "app", DeploymentID: "candidate"}}}}
			current := state.ProjectReleaseSet{Members: []state.ProjectReleaseMember{{AppID: "app", DeploymentID: "candidate"}}}
			evidence, err := ProbeProjectRelease(t.Context(), receipt, current, sender, func(_ context.Context, body string) error {
				var c struct {
					DeploymentID string `json:"deployment_id"`
					Token        string `json:"token"`
				}
				if err := json.Unmarshal([]byte(body), &c); err != nil {
					return err
				}
				sender.tokens[c.DeploymentID] = c.Token
				return nil
			}, 0)
			if err != nil || len(evidence) != 1 || sender.calls != api.RouteHealthProbeRequestsPerMinute || evidence[0].StableRequests != api.RouteHealthProbeRequestsPerMinute {
				t.Fatalf("current attribution: %+v %v calls=%d", evidence, err, sender.calls)
			}
			if outcome == routeprobe.ServerError && evidence[0].StableErrors == 0 {
				t.Fatal("current server failures lost")
			}
			if outcome == routeprobe.Unauthenticated && evidence[0].Unauthenticated == 0 {
				t.Fatal("auth failure counted as health")
			}
		})
	}
}

func TestNamedEnvironmentCriticalProbeScope(t *testing.T) {
	sender := &projectProbeTestSender{tokens: map[string]string{}, candidate: routeprobe.Success}
	p := api.ProjectReleaseProbe{AppID: "app", Slug: "api", Method: "GET", Path: "/health"}
	r := api.ProjectSubmissionReceipt{Apply: api.ApplyResponse{PlanResponse: api.PlanResponse{SafeRelease: &api.ProjectSafeRelease{Verification: &api.ProjectReleaseVerification{Probes: []api.ProjectReleaseProbe{p}}}}, Builds: []api.AppliedBuild{{AppID: "app", DeploymentID: "candidate"}}}}
	baseline := state.ProjectReleaseSet{Members: []state.ProjectReleaseMember{{AppID: "app", DeploymentID: "baseline"}}}
	evidence, err := ProbeProjectReleaseForScope(t.Context(), "staging", r, baseline, sender, func(_ context.Context, payload string) error {
		var challenge struct {
			Scope        string `json:"scope"`
			DeploymentID string `json:"deployment_id"`
			Token        string `json:"token"`
		}
		if err := json.Unmarshal([]byte(payload), &challenge); err != nil {
			return err
		}
		if challenge.Scope != "staging" {
			return fmt.Errorf("destination scope lost")
		}
		sender.tokens[challenge.DeploymentID] = challenge.Token
		return nil
	}, 0)
	if err != nil || state.ProjectReleaseProbeVerdict([]api.ProjectReleaseProbe{p}, evidence) != "healthy" {
		t.Fatalf("evidence=%+v err=%v", evidence, err)
	}
}
