package apidserver

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestEnvironmentCacheConvergenceHTTP(t *testing.T) {
	e := setup(t, api.PlanPro)
	ctx := t.Context()
	p, err := e.store.CreateProject(ctx, state.Project{AccountID: e.acct.ID, Slug: "cache-status"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := e.store.CreateApp(ctx, state.App{AccountID: e.acct.ID, ProjectID: p.ID, Slug: "cache-status", Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	env, err := e.store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: e.acct.ID, ProjectID: p.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	var zero int64
	_, err = e.store.SetEnvironmentCachePolicy(ctx, e.acct.ID, app.ID, "staging", api.SetEnvironmentCachePolicyRequest{AppID: app.ID, ProjectID: p.ID, EnvironmentID: env.ID, RequestID: uuid.NewString(), ExpectedRevision: &zero, Rules: []api.EnvironmentCacheRule{}})
	if err != nil {
		t.Fatal(err)
	}
	path := "/v1/apps/cache-status/cache-policy/convergence"
	for _, q := range []string{"", "?scope=default", "?environment=staging&environment=staging"} {
		if r := e.do(t, http.MethodGet, path+q, nil, nil); r.Code != 400 {
			t.Fatalf("selector %s=%d", q, r.Code)
		}
	}
	read := func(want string) {
		t.Helper()
		r := e.do(t, http.MethodGet, path+"?environment=staging", nil, nil)
		var out api.EnvironmentCacheConvergence
		if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &out) != nil || out.Status != want || out.EnvironmentID != env.ID || out.Revision != 1 {
			t.Fatalf("read=%d %s", r.Code, r.Body)
		}
		if strings.Contains(r.Body.String(), "private-observer") {
			t.Fatal("status exposed fleet identities")
		}
	}
	read("degraded")
	role, target := "compute-node", "tcp://private-observer:8080"
	node, err := e.store.CreateComputeNode(ctx, state.ComputeNode{Name: "private-observer", TargetURL: "tcp://127.0.0.1:50051", GatewayTargetURL: &target, Active: true, Role: &role, VPCPUs: 1, MemMB: 1024, MaxConcurrency: 10, AdmissionCeilingMB: 512, VCPUBudget: 1})
	if err != nil {
		t.Fatal(err)
	}
	session := uuid.NewString()
	if err = e.store.BeginEnvironmentCacheGatewaySession(ctx, node.Name, session, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	read("pending")
	if err = e.store.RecordEnvironmentCacheGatewayObserved(ctx, node.Name, session, app.ID, env.ID, 1); err != nil {
		t.Fatal(err)
	}
	read("converged")
}
