// project_release_graph_routing_e2e_test.go holds the client and its internal
// service calls on one immutable project release across an environment cutover.
//
// Build tag: (none). CI-safe. Requires Postgres, but no KVM: public ingress uses
// the normal-path harness and internal service forwarding is observed at the
// production ServiceProxy boundary.
package e2e_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

type releaseGraphEndpointProvider struct {
	snapshot gateway.ServiceEndpointsSnapshot
}

func (p releaseGraphEndpointProvider) ServiceEndpoints(context.Context, string) (gateway.ServiceEndpointsSnapshot, error) {
	return p.snapshot, nil
}

func createReleaseGraphApp(t *testing.T, f *normalPathFixture, accountID, projectID, slug, workload string) state.App {
	t.Helper()
	app, err := f.store.CreateApp(f.ctx, state.App{
		AccountID: accountID, ProjectID: projectID, Slug: slug,
		Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 256,
		IdleTimeoutS: 3600, MaxConcurrency: 1, WorkloadName: workload,
		Manifest: state.AppManifest{RevisionPinTTLSeconds: api.RevisionPinMaxTTLSeconds},
	})
	if err != nil {
		t.Fatalf("create project workload %q: %v", slug, err)
	}
	return app
}

func setReleaseGraphCallerIP(t *testing.T, f *normalPathFixture, instance state.Instance, hostIP string) {
	t.Helper()
	if err := f.store.SetInstanceRuntime(f.ctx, instance.ID, "fc-"+instance.ID, hostIP, 20000); err != nil {
		t.Fatalf("set caller instance network identity: %v", err)
	}
}

func createReleaseGraphLiveDeployment(t *testing.T, f *normalPathFixture, appID, version string) (state.Deployment, state.Instance) {
	t.Helper()
	digestByte := "1"
	if version == "v2" {
		digestByte = "2"
	}
	deployment, err := f.store.CreateDeployment(f.ctx, state.Deployment{
		AppID: appID, Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:" + strings.Repeat(digestByte, 64), Scope: "production",
	})
	if err != nil {
		t.Fatalf("create %s production deployment: %v", version, err)
	}
	if err := f.store.MarkDeploymentLive(f.ctx, deployment.ID); err != nil {
		t.Fatalf("mark %s production deployment live: %v", version, err)
	}
	publishNormalPathLayer(t, f, deployment.ID)
	instance, err := f.store.CreateInstance(f.ctx, appID, deployment.ID, string(state.StateRunning),
		e2etest.FakeSnapshotRAMMB, f.nodeID, "")
	if err != nil {
		t.Fatalf("create %s production instance: %v", version, err)
	}
	return deployment, instance
}

func releaseGraphServiceProxy(t *testing.T, f *normalPathFixture, accountID string, callerApp, targetApp state.App, endpoints ...gateway.ServiceEndpoint) *gateway.ServiceProxy {
	t.Helper()
	return gateway.NewServiceProxy(gateway.ServiceProxyConfig{
		Provider: releaseGraphEndpointProvider{snapshot: gateway.ServiceEndpointsSnapshot{
			AppID: targetApp.ID, Endpoints: endpoints,
		}},
		Resolve: func(_ context.Context, caller, service string) (gateway.ServiceTarget, bool, error) {
			return gateway.ServiceTarget{AppID: targetApp.ID}, caller == callerApp.ID && service == targetApp.Slug, nil
		},
		Authorize: func(_ context.Context, caller, target string) (gateway.ServiceCaller, error) {
			if caller != callerApp.ID || target != targetApp.ID {
				return gateway.ServiceCaller{}, gateway.ErrServiceProxyDenied
			}
			return gateway.ServiceCaller{AppID: caller, AccountID: accountID}, nil
		},
		ResolveCallerIdentity: func(ctx context.Context, remoteAddr string) (string, string, error) {
			host, _, err := net.SplitHostPort(remoteAddr)
			if err != nil {
				return "", "", fmt.Errorf("parse caller address: %w", err)
			}
			instances, err := f.store.LiveInstancesByHostIP(ctx, state.DefaultLocalNodeName, host)
			if err != nil {
				return "", "", err
			}
			if len(instances) != 1 {
				return "", "", fmt.Errorf("caller address %s resolved to %d live instances", host, len(instances))
			}
			return instances[0].AppID, instances[0].DeploymentID, nil
		},
		ResolveRelease: func(ctx context.Context, callerID, callerDeploymentID, targetID, requestedID string) (string, string, error) {
			releaseID, deploymentID, err := f.store.ResolveServiceRelease(ctx, callerID, callerDeploymentID, targetID, requestedID)
			if errors.Is(err, state.ErrNotFound) {
				return "", "", gateway.ErrReleaseGone
			}
			if errors.Is(err, state.ErrConflict) {
				return "", "", gateway.ErrReleaseConflict
			}
			return releaseID, deploymentID, err
		},
		Forward: func(target gateway.Target) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Test-Selected-Deployment", target.DeploymentID)
				w.Header().Set("X-Test-Forwarded-Release", r.Header.Get(api.ReleaseHeader))
				w.Header().Set("X-Test-Forwarded-Revision", r.Header.Get(api.RevisionHeader))
				_, _ = w.Write([]byte(target.DeploymentID))
			})
		},
	})
}

func projectReleaseIngress(t *testing.T, f *normalPathFixture, host, releaseID string, wantStatus int, wantBody string) (http.Header, []byte) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	var headers http.Header
	var body []byte
	var status int
	for time.Now().Before(deadline) {
		extra := map[string]string{}
		if releaseID != "" {
			extra[api.ReleaseHeader] = releaseID
		}
		headers, body, status = doReqHeaders(t, f.h, host, http.MethodGet, "/checkout", nil, extra)
		if status == wantStatus && (wantBody == "" || strings.Contains(string(body), wantBody)) {
			return headers, body
		}
		time.Sleep(100 * time.Millisecond)
	}
	lastVMMDRequest := f.vmmd.LastRequest()
	t.Fatalf("ingress host=%q release=%q: status=%d headers=%v body=%q, want status=%d body containing %q (vmmd forwards=%d last request=%v)",
		host, releaseID, status, headers, body, wantStatus, wantBody, f.vmmd.ForwardCount(), lastVMMDRequest)
	return nil, nil
}

func projectReleaseServiceCall(t *testing.T, proxy *gateway.ServiceProxy, callerAppID, callerHostIP, service, releaseID string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "http://gateway/v1/internal/services/"+service+"/charge", nil)
	request.RemoteAddr = net.JoinHostPort(callerHostIP, "42000")
	request.Header.Set(gateway.ServiceProxyCallerAppHeader, callerAppID)
	request.Header.Set(api.ReleaseHeader, releaseID)
	response := httptest.NewRecorder()
	proxy.ServeHTTP(response, request)
	return response
}

func TestE2E_ProjectReleaseGraphPinsClientAndServiceCallsAcrossCutoverAndExpiry(t *testing.T) {
	f := newNormalPathFixture(t, "release-graph-skew")
	if f == nil {
		return
	}
	accountApp, err := f.store.AppByID(f.ctx, f.app.ID)
	if err != nil {
		t.Fatalf("load fixture app: %v", err)
	}
	suffix := fmt.Sprintf("%x", time.Now().UnixNano())
	project, err := f.store.CreateProject(f.ctx, state.Project{
		AccountID: accountApp.AccountID, Slug: "skew-" + suffix, ProductionBranch: "main",
	})
	if err != nil {
		t.Fatalf("create release graph project: %v", err)
	}
	apiApp := createReleaseGraphApp(t, f, accountApp.AccountID, project.ID, "skewapi-"+suffix, "api")
	billingApp := createReleaseGraphApp(t, f, accountApp.AccountID, project.ID, "skewbilling-"+suffix, "billing")

	apiV1, apiInstanceV1 := createReleaseGraphLiveDeployment(t, f, apiApp.ID, "v1")
	billingV1, billingInstanceV1 := createReleaseGraphLiveDeployment(t, f, billingApp.ID, "v1")
	f.vmmd.SetVersion(apiInstanceV1.ID, "api-v1")
	setReleaseGraphCallerIP(t, f, apiInstanceV1, "10.100.0.21")

	ctx := f.ctx
	graphA, err := f.store.PublishProjectReleaseSet(ctx, accountApp.AccountID, project.ID, "production", 3600, []state.ProjectReleaseMember{
		{AppID: apiApp.ID, DeploymentID: apiV1.ID},
		{AppID: billingApp.ID, DeploymentID: billingV1.ID},
	})
	if err != nil {
		t.Fatalf("publish initial release graph: %v", err)
	}
	resolvedReleaseID, resolvedDeploymentID, err := f.store.ResolveProjectRelease(ctx, apiApp.ID, "production", "")
	if err != nil || resolvedReleaseID != graphA.ID || resolvedDeploymentID != apiV1.ID {
		t.Fatalf("initial API release resolution = (%q, %q, %v), want (%q, %q, nil)",
			resolvedReleaseID, resolvedDeploymentID, err, graphA.ID, apiV1.ID)
	}

	initialHeaders, initialBody := projectReleaseIngress(t, f, apiApp.Slug+".apps.test.example", "", http.StatusOK, "api-v1")
	if got := initialHeaders.Get(api.ReleaseHeader); got != graphA.ID {
		t.Fatalf("initial bootstrap release = %q, want %q", got, graphA.ID)
	}
	if got := initialHeaders.Get(api.RevisionHeader); got != apiV1.ID {
		t.Fatalf("initial bootstrap deployment = %q, want %q (body %q)", got, apiV1.ID, initialBody)
	}
	oldClientRelease := initialHeaders.Get(api.ReleaseHeader)

	proxy := releaseGraphServiceProxy(t, f, accountApp.AccountID, apiApp, billingApp,
		gateway.ServiceEndpoint{InstanceID: billingInstanceV1.ID, NodeID: f.nodeID, DeploymentID: billingV1.ID, Port: 8080},
	)
	oldServiceCall := projectReleaseServiceCall(t, proxy, apiApp.ID, "10.100.0.21", billingApp.Slug, oldClientRelease)
	if oldServiceCall.Code != http.StatusOK || oldServiceCall.Body.String() != billingV1.ID ||
		oldServiceCall.Header().Get("X-Test-Forwarded-Release") != graphA.ID || oldServiceCall.Header().Get("X-Test-Forwarded-Revision") != "" {
		t.Fatalf("initial internal call = %d body=%q release=%q revision=%q; want billing deployment %q on graph %q",
			oldServiceCall.Code, oldServiceCall.Body.String(), oldServiceCall.Header().Get("X-Test-Forwarded-Release"),
			oldServiceCall.Header().Get("X-Test-Forwarded-Revision"), billingV1.ID, graphA.ID)
	}

	apiV2, apiInstanceV2 := createReleaseGraphLiveDeployment(t, f, apiApp.ID, "v2")
	billingV2, billingInstanceV2 := createReleaseGraphLiveDeployment(t, f, billingApp.ID, "v2")
	f.vmmd.SetVersion(apiInstanceV2.ID, "api-v2")
	setReleaseGraphCallerIP(t, f, apiInstanceV2, "10.100.0.22")
	if _, err := f.store.PublishProjectReleaseSet(ctx, accountApp.AccountID, project.ID, "production", 3600, []state.ProjectReleaseMember{
		{AppID: apiApp.ID, DeploymentID: apiV2.ID},
		{AppID: billingApp.ID, DeploymentID: billingV2.ID},
	}); err != nil {
		t.Fatalf("promote replacement release graph: %v", err)
	}
	proxy = releaseGraphServiceProxy(t, f, accountApp.AccountID, apiApp, billingApp,
		gateway.ServiceEndpoint{InstanceID: billingInstanceV1.ID, NodeID: f.nodeID, DeploymentID: billingV1.ID, Port: 8080},
		gateway.ServiceEndpoint{InstanceID: billingInstanceV2.ID, NodeID: f.nodeID, DeploymentID: billingV2.ID, Port: 8080},
	)

	newHeaders, newBody := projectReleaseIngress(t, f, apiApp.Slug+".apps.test.example", "", http.StatusOK, "api-v2")
	if got := newHeaders.Get(api.ReleaseHeader); got == "" || got == oldClientRelease {
		t.Fatalf("new client bootstrap release = %q, want a new active graph (old body %q)", got, newBody)
	}
	if got := newHeaders.Get(api.RevisionHeader); got != apiV2.ID {
		t.Fatalf("new client deployment = %q, want %q", got, apiV2.ID)
	}

	oldHeaders, oldBody := projectReleaseIngress(t, f, apiApp.Slug+".apps.test.example", oldClientRelease, http.StatusOK, "api-v1")
	if got := oldHeaders.Get(api.RevisionHeader); got != apiV1.ID {
		t.Fatalf("old client deployment after cutover = %q, want %q (body %q)", got, apiV1.ID, oldBody)
	}
	oldServiceCall = projectReleaseServiceCall(t, proxy, apiApp.ID, "10.100.0.21", billingApp.Slug, oldClientRelease)
	if oldServiceCall.Code != http.StatusOK || oldServiceCall.Body.String() != billingV1.ID || oldServiceCall.Header().Get("X-Test-Forwarded-Release") != oldClientRelease {
		t.Fatalf("old client's post-cutover service call = %d body=%q release=%q, want old billing deployment %q on graph %q",
			oldServiceCall.Code, oldServiceCall.Body.String(), oldServiceCall.Header().Get("X-Test-Forwarded-Release"), billingV1.ID, oldClientRelease)
	}

	newServiceCall := projectReleaseServiceCall(t, proxy, apiApp.ID, "10.100.0.22", billingApp.Slug, newHeaders.Get(api.ReleaseHeader))
	if newServiceCall.Code != http.StatusOK || newServiceCall.Body.String() != billingV2.ID || newServiceCall.Header().Get("X-Test-Forwarded-Release") != newHeaders.Get(api.ReleaseHeader) {
		t.Fatalf("new client's service call = %d body=%q release=%q, want new billing deployment %q on graph %q",
			newServiceCall.Code, newServiceCall.Body.String(), newServiceCall.Header().Get("X-Test-Forwarded-Release"), billingV2.ID, newHeaders.Get(api.ReleaseHeader))
	}

	if _, err := f.h.Pool.Exec(ctx, `update project_release_sets set expires_at = now() - interval '1 second' where id = $1`, graphA.ID); err != nil {
		t.Fatalf("expire previous release graph: %v", err)
	}
	projectReleaseIngress(t, f, apiApp.Slug+".apps.test.example", oldClientRelease, http.StatusGone, "")
	expiredServiceCall := projectReleaseServiceCall(t, proxy, apiApp.ID, "10.100.0.21", billingApp.Slug, oldClientRelease)
	if expiredServiceCall.Code != http.StatusGone {
		t.Fatalf("expired internal graph call = %d body=%q, want 410", expiredServiceCall.Code, expiredServiceCall.Body.String())
	}
	newServiceCall = projectReleaseServiceCall(t, proxy, apiApp.ID, "10.100.0.22", billingApp.Slug, newHeaders.Get(api.ReleaseHeader))
	if newServiceCall.Code != http.StatusOK || newServiceCall.Body.String() != billingV2.ID {
		t.Fatalf("current graph after old pin expiry = %d body=%q, want billing deployment %q", newServiceCall.Code, newServiceCall.Body.String(), billingV2.ID)
	}
}
