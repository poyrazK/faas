package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// The fleet list reads resident instances on every node, filters by state,
// node, app and account, and caps at limit; the lookup finds one instance in
// any state (ADR-20261121161842).
func TestObsFleetInstancesListAndLookup(t *testing.T) {
	e := apidserver.NewObsEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	ctx := context.Background()
	store := *e.Store()
	seed := func(node string) state.ComputeNode {
		n, err := store.UpsertComputeNodeFromOperator(ctx, state.ComputeNode{Name: node, TargetURL: "unix:///run/faas/" + node + ".sock",
			VPCPUs: 8, MemMB: 16384, MaxConcurrency: 32, AdmissionCeilingMB: 12000})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	nodeA, nodeB := seed("fleet-a"), seed("fleet-b")
	app := func(slug string) (state.App, state.Deployment) {
		a, err := store.CreateApp(ctx, state.App{AccountID: (*e.Acct()).ID, Slug: slug, Type: state.AppTypeApp})
		if err != nil {
			t.Fatal(err)
		}
		d, err := store.CreateDeployment(ctx, state.Deployment{ID: uuid.NewString(), AppID: a.ID, Status: state.DeployLive})
		if err != nil {
			t.Fatal(err)
		}
		return a, d
	}
	shop, shopDep := app("fleet-shop")
	blog, blogDep := app("fleet-blog")
	instance := func(a state.App, d state.Deployment, node state.ComputeNode, st state.State) state.Instance {
		ins, err := store.CreateInstance(ctx, a.ID, d.ID, string(st), 512, node.ID, uuid.NewString())
		if err != nil {
			t.Fatal(err)
		}
		return ins
	}
	shopRunning := instance(shop, shopDep, nodeA, state.StateRunning)
	instance(shop, shopDep, nodeA, state.StateFrozen)
	parked := instance(shop, shopDep, nodeA, state.StateParked)
	blogRunning := instance(blog, blogDep, nodeB, state.StateRunning)

	list := func(query string, want int, ids ...string) api.ObsInstanceListResponse {
		t.Helper()
		rec := e.DoOperator(t, http.MethodGet, "/v1/admin/obs/instances"+query, nil, nil)
		if rec.Code != want {
			t.Fatalf("%s: status %d body=%s", query, rec.Code, rec.Body.String())
		}
		var out api.ObsInstanceListResponse
		if want == http.StatusOK {
			if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
			if ids != nil {
				got := []string{}
				for _, item := range out.Items {
					got = append(got, item.ID)
				}
				sort.Strings(got)
				sort.Strings(ids)
				if len(got) != len(ids) {
					t.Fatalf("%s: ids = %v, want %v", query, got, ids)
				}
				for i := range ids {
					if got[i] != ids[i] {
						t.Fatalf("%s: ids = %v, want %v", query, got, ids)
					}
				}
			}
		}
		return out
	}

	out := list("", http.StatusOK, shopRunning.ID, blogRunning.ID)
	for _, item := range out.Items {
		if item.NodeName == "" || item.AppSlug == "" || item.AccountID != (*e.Acct()).ID {
			t.Fatalf("row lacks node, app or account: %+v", item)
		}
	}
	if out.Limit != api.ObsInstanceListDefault || out.Truncated {
		t.Fatalf("limit = %d truncated = %v", out.Limit, out.Truncated)
	}
	if resident := list("?state=resident", http.StatusOK); len(resident.Items) != 3 {
		t.Fatalf("resident = %+v", resident.Items)
	}
	list("?node=fleet-a", http.StatusOK, shopRunning.ID)
	list("?app_id="+blog.ID, http.StatusOK, blogRunning.ID)
	list("?account_id="+uuid.NewString(), http.StatusOK, []string{}...)
	if capped := list("?limit=1", http.StatusOK); len(capped.Items) != 1 || !capped.Truncated {
		t.Fatalf("limit=1 = %+v", capped)
	}
	list("?state=parked", http.StatusBadRequest)
	list("?app_id=nope", http.StatusBadRequest)

	get := func(id string, want int) api.ObsInstanceRow {
		t.Helper()
		rec := e.DoOperator(t, http.MethodGet, "/v1/admin/obs/instances/"+id, nil, nil)
		if rec.Code != want {
			t.Fatalf("get %s: status %d body=%s", id, rec.Code, rec.Body.String())
		}
		var row api.ObsInstanceRow
		if want == http.StatusOK {
			if err := json.Unmarshal(rec.Body.Bytes(), &row); err != nil {
				t.Fatal(err)
			}
		}
		return row
	}
	if row := get(shopRunning.ID, http.StatusOK); row.NodeName != "fleet-a" || row.AppSlug != "fleet-shop" || row.WakeID == "" {
		t.Fatalf("instance = %+v", row)
	}
	if row := get(parked.ID, http.StatusOK); row.State != string(state.StateParked) {
		t.Fatalf("parked instance = %+v", row)
	}
	get(uuid.NewString(), http.StatusNotFound)
	get("nope", http.StatusBadRequest)
}
