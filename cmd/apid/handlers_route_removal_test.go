package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/openapidiff"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRouteRemovalCompatibility(t *testing.T) {
	baseline := `{"openapi":"3.0.3","info":{"title":"routes","version":"1"},"paths":{"/old":{"get":{"responses":{"200":{"description":"ok"}}}},"/new":{"get":{"responses":{"200":{"description":"ok"}}}}}}`
	candidate := `{"openapi":"3.0.3","info":{"title":"routes","version":"2"},"paths":{"/new":{"get":{"responses":{"200":{"description":"ok"}}}}}}`
	mapping := []api.RouteRemovalMapping{{Method: "GET", Path: "/old", SuccessorMethod: "GET", SuccessorPath: "/new"}}
	for _, tc := range []struct {
		name, baseline, candidate string
		mappings                  []api.RouteRemovalMapping
		pass                      bool
	}{
		{"compatible", baseline, candidate, mapping, true},
		{"missing_mapping", baseline, candidate, nil, false},
		{"missing_staged_successor", candidate, candidate, mapping, false},
		{"undeclared_response", baseline, strings.ReplaceAll(candidate, `"responses":{"200":{"description":"ok"}}`, `"responses":{}`), mapping, false},
		{"required_successor_input", baseline, strings.ReplaceAll(candidate, `"get":{`, `"get":{"parameters":[{"name":"new","in":"query","required":true,"schema":{"type":"string"}}],`), mapping, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateRouteRemovalCompatibility(state.RouteRemovalContract{Document: []byte(tc.baseline)}, state.RouteRemovalContract{Document: []byte(tc.candidate)}, tc.mappings)
			if (err == nil) != tc.pass {
				t.Fatalf("pass=%v: %v", tc.pass, err)
			}
		})
	}
}

func TestContractTrafficContextUsesServingBaseline(t *testing.T) {
	t.Setenv("FAAS_API_CONTRACT_DIFF_ENABLED", "1")
	e := setup(t, api.PlanPro)
	app := seedApp(t, e, "contract-traffic-baseline")
	ctx := context.Background()
	baseline, err := e.store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "prod", Kind: state.DeploymentKindImage, Status: state.DeployLive, TrafficPercent: 100, TrafficPercentExplicit: true})
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := e.store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "prod", Kind: state.DeploymentKindImage, TrafficPercent: 0, TrafficPercentExplicit: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []state.Deployment{baseline, candidate} {
		if err = e.store.SetDeploymentRootfs(ctx, d.ID, "/test/"+d.ID, "test/"+d.ID, 1024); err != nil {
			t.Fatal(err)
		}
		if err = e.store.UpdateDeploymentStatus(ctx, d.ID, state.DeployLive, ""); err != nil {
			t.Fatal(err)
		}
	}
	baselineDoc := []byte(`{"openapi":"3.0.3","paths":{"/old":{"get":{"responses":{"200":{"description":"ok"}}}},"/new":{"get":{"responses":{"200":{"description":"ok"}}}}}}`)
	candidateDoc := []byte(`{"openapi":"3.0.3","paths":{"/new":{"get":{"responses":{"200":{"description":"ok"}}}}}}`)
	for _, item := range []struct {
		deployment state.Deployment
		doc        []byte
	}{{baseline, baselineDoc}, {candidate, candidateDoc}} {
		snapshot, _, err := openapidiff.SnapshotFromDocument(item.deployment.ID, app.ID, "prod", item.doc, nil)
		if err != nil {
			t.Fatal(err)
		}
		if item.deployment.ID == candidate.ID {
			snapshot.CapturedAt = time.Now().Add(time.Minute)
		}
		if err = e.store.UpdateDeploymentOpenAPISnapshot(ctx, snapshot); err != nil {
			t.Fatal(err)
		}
	}
	candidate, err = e.store.DeploymentByID(ctx, candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	serving, err := e.store.DeploymentByID(ctx, baseline.ID)
	if err != nil || serving.TrafficPercent != 100 || candidate.TrafficPercent != 0 || candidate.Scope != "prod" {
		t.Fatalf("invalid serving fixture: %+v %+v %v", serving, candidate, err)
	}
	_, problem := (&server{store: e.store}).contractTrafficContext(ctx, app, candidate)
	if problem == nil || problem.Code != api.CodeAPIContractBreakingChange {
		t.Fatalf("dark candidate displaced serving contract: %+v", problem)
	}
}
