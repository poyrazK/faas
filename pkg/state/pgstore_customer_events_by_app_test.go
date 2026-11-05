package state_test

import (
	"encoding/json"
	"sort"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// The app drill-down drove each relation branch from events, scanning
// every event with an instance_id (220k on production-us) and timing out
// as "Briefly at capacity" (Hunt 12 H12-44). Each branch now starts from
// the app's deployments, builds and instances; this pins that every branch
// still finds the app's events and never another app's.
func TestPg_ListCustomerEventsByAppCoversEveryRelation(t *testing.T) {
	store, ctx := pgStore(t)
	suffix := uuid.NewString()[:8]
	account, err := store.CreateAccount(ctx, "events-by-app-"+suffix+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	role := "compute-node"
	node, err := store.CreateComputeNode(ctx, state.ComputeNode{
		Name: "events-by-app-" + suffix, TargetURL: "tcp://127.0.0.1:50051",
		VPCPUs: 2, MemMB: 4096, MaxConcurrency: 10, AdmissionCeilingMB: 2048,
		VCPUBudget: 2, Active: true, Role: &role,
	})
	if err != nil {
		t.Fatal(err)
	}
	type rel struct{ app, deployment, build, instance string }
	seed := func(slug string) rel {
		t.Helper()
		app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: slug + "-" + suffix,
			Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 256, CPUMillicores: 1000, MaxConcurrency: 2})
		if err != nil {
			t.Fatal(err)
		}
		dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball,
			ImageDigest: "sha256:" + slug, Status: state.DeployLive})
		if err != nil {
			t.Fatal(err)
		}
		build, err := store.CreateBuild(ctx, dep.ID, dep.Kind, 1, "")
		if err != nil {
			t.Fatal(err)
		}
		inst, err := store.CreateInstance(ctx, app.ID, dep.ID, string(state.StateRunning), 256, node.ID, "")
		if err != nil {
			t.Fatal(err)
		}
		return rel{app: app.ID, deployment: dep.ID, build: build.ID, instance: inst.ID}
	}
	mine, other := seed("mine"), seed("other")
	for _, r := range []rel{mine, other} {
		for kind, data := range map[string]map[string]any{
			"test.by_app":        {"app_id": r.app},
			"test.by_deployment": {"deployment_id": r.deployment},
			"test.by_build":      {"build_id": r.build},
			"test.by_instance":   {"instance_id": r.instance},
			"test.by_instance_2": {"instance": r.instance},
		} {
			payload, err := json.Marshal(data)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.AppendEvent(ctx, "test", kind, &account.ID, payload); err != nil {
				t.Fatal(err)
			}
		}
	}
	rows, err := store.ListCustomerEvents(ctx, state.CustomerEventFilter{AccountID: account.ID, AppID: mine.app, KindPrefix: "test.", Limit: 50})
	if err != nil {
		t.Fatalf("ListCustomerEvents: %v", err)
	}
	var kinds []string
	for _, row := range rows {
		if string(row.Data) != "" && (json.Valid(row.Data)) {
			var data map[string]string
			_ = json.Unmarshal(row.Data, &data)
			for _, v := range data {
				if v == other.app || v == other.deployment || v == other.build || v == other.instance {
					t.Fatalf("another app's event leaked: %s %s", row.Kind, row.Data)
				}
			}
		}
		kinds = append(kinds, row.Kind)
	}
	sort.Strings(kinds)
	want := []string{"test.by_app", "test.by_build", "test.by_deployment", "test.by_instance", "test.by_instance_2"}
	if len(kinds) != len(want) {
		t.Fatalf("kinds = %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("kinds = %v, want %v", kinds, want)
		}
	}
}
