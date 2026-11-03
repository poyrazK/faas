// adr: 20261102081618
// edge_local_first_test.go pins local first: a request a compute edge
// forwarded uses this node's instances while one has a free slot, the
// response names every node of the app, and scale-out reads demand from this
// gateway's share.
package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// spreadFixture is an app with two deployments: dep-1 has two ready
// instances on node-a, one on node-b and one withdrawn on node-a; dep-2 has
// one on node-b.
func spreadFixture(t *testing.T) (*PGBackend, *Handler) {
	t.Helper()
	b := NewPGBackend(nil, nil, nil)
	b.appsPicker["app-1"] = &appPicker{sets: map[string]*targetSet{
		"dep-1": {entries: []Target{
			{NodeID: "node-a", InstanceID: "a1"},
			{NodeID: "node-a", InstanceID: "a2"},
			{NodeID: "node-b", InstanceID: "b1"},
			{NodeID: "node-a", InstanceID: "a3", PlacementUnavailable: true},
		}},
		"dep-2": {entries: []Target{{NodeID: "node-b", InstanceID: "b2"}}},
	}}
	h := NewHandlerWith(b, NewMetrics(), nil).WithEdgeLocalFirst(func(context.Context) (string, error) { return "node-a", nil })
	return b, h
}

func edgeAsked(ctx context.Context) context.Context {
	return context.WithValue(ctx, edgePlacementKey{}, true)
}

func TestReadyNodeCountsAndLocalReadyTargets(t *testing.T) {
	b, _ := spreadFixture(t)
	if got := fmt.Sprint(b.ReadyNodeCounts("app-1")); got != "[{node-a 2} {node-b 2}]" {
		t.Fatalf("ReadyNodeCounts = %s", got)
	}
	var firsts []string
	for range 2 {
		local := b.LocalReadyTargets("app-1", "dep-1", "node-a")
		if len(local) != 2 || local[0].DeploymentID != "dep-1" {
			t.Fatalf("LocalReadyTargets = %+v, want a1 and a2 in dep-1", local)
		}
		firsts = append(firsts, local[0].InstanceID)
	}
	if firsts[0] == firsts[1] {
		t.Fatalf("local first does not rotate: started at %v", firsts)
	}
	if got := b.LocalReadyTargets("app-1", "dep-2", "node-a"); len(got) != 0 {
		t.Fatalf("dep-2 has no instance on node-a, got %+v", got)
	}
}

func TestFormatNodeCounts(t *testing.T) {
	if got := formatNodeCounts([]NodeCount{{"node-b", 1}, {"node-a", 2}, {"node-c", 0}}); got != "node-a=2,node-b=1" {
		t.Fatalf("formatNodeCounts = %q", got)
	}
	var many []NodeCount
	for i := range 20 {
		many = append(many, NodeCount{NodeID: fmt.Sprintf("node-%02d", i), Ready: 1})
	}
	if got := strings.Count(formatNodeCounts(many), "="); got != maxInstanceNodes {
		t.Fatalf("formatNodeCounts kept %d nodes, want %d", got, maxInstanceNodes)
	}
}

func TestAcquireLocalTargetPrefersThisNodeWhileASlotIsFree(t *testing.T) {
	_, h := spreadFixture(t)
	app := App{ID: "app-1", Plan: api.PlanPro}
	remote := PickResult{OK: true, Picked: "dep-1", Target: Target{NodeID: "node-b", InstanceID: "b1", DeploymentID: "dep-1"}}

	if _, _, ok := h.acquireLocalTarget(h.localFirstContext(context.Background()), app, remote, 1); ok {
		t.Fatal("a request no edge forwarded used local first")
	}
	ctx := h.localFirstContext(edgeAsked(context.Background()))
	var releases []func()
	for range 2 {
		local, release, ok := h.acquireLocalTarget(ctx, app, remote, 1)
		if !ok || local.Target.NodeID != "node-a" || local.Picked != "dep-1" {
			t.Fatalf("acquireLocalTarget = %+v %v, want a node-a instance", local, ok)
		}
		releases = append(releases, release)
	}
	// Both local instances hold their one slot: the request goes elsewhere.
	if _, _, ok := h.acquireLocalTarget(ctx, app, remote, 1); ok {
		t.Fatal("local first took a full instance")
	}
	releases[0]()
	if _, _, ok := h.acquireLocalTarget(ctx, app, remote, 1); !ok {
		t.Fatal("a freed local slot was not used")
	}
}

func TestLocalFirstNeedsTheNodeID(t *testing.T) {
	b, _ := spreadFixture(t)
	h := NewHandlerWith(b, NewMetrics(), nil).WithEdgeLocalFirst(func(context.Context) (string, error) { return "", errors.New("registry down") })
	if ctx := h.localFirstContext(edgeAsked(context.Background())); ctx.Value(localFirstKey{}) != nil {
		t.Fatal("local first armed without this node's id")
	}
	if got := h.scaleByLocalShare(context.Background(), "app-1", 3, 20); got != 3 {
		t.Fatalf("scaleByLocalShare = %d without a node id, want 3", got)
	}
}

func TestScaleByLocalShare(t *testing.T) {
	_, h := spreadFixture(t)
	// node-a holds 2 of the app's 4 ready instances.
	for _, tc := range []struct{ desired, max, want int }{
		{desired: 3, max: 20, want: 6},
		{desired: 3, max: 5, want: 5},
		{desired: 0, max: 20, want: 0},
	} {
		if got := h.scaleByLocalShare(context.Background(), "app-1", tc.desired, tc.max); got != tc.want {
			t.Errorf("scaleByLocalShare(%d, max %d) = %d, want %d", tc.desired, tc.max, got, tc.want)
		}
	}
	if got := h.scaleByLocalShare(context.Background(), "app-2", 3, 20); got != 3 {
		t.Errorf("an app with no instance here kept %d, want 3", got)
	}
}

func TestInstanceNodesStampedOnlyWhenTheEdgeAsked(t *testing.T) {
	_, h := spreadFixture(t)
	for _, asked := range []bool{false, true} {
		ctx := context.Background()
		if asked {
			ctx = edgeAsked(ctx)
		}
		header := http.Header{}
		stampInstanceNode(h.instanceNodesContext(ctx, "app-1"), header, "node-a")
		if got, want := header.Get(InstanceNodesHeader), map[bool]string{true: "node-a=2,node-b=2"}[asked]; got != want {
			t.Errorf("asked=%v: %s = %q, want %q", asked, InstanceNodesHeader, got, want)
		}
	}
	// Without the switch the handler names only the instance's node.
	plain := NewHandlerWith(NewPGBackend(nil, nil, nil), NewMetrics(), nil)
	header := http.Header{}
	stampInstanceNode(plain.instanceNodesContext(edgeAsked(context.Background()), "app-1"), header, "node-a")
	if header.Get(InstanceNodesHeader) != "" || header.Get(InstanceNodeHeader) != "node-a" {
		t.Fatalf("headers without the switch = %v", header)
	}
}

func TestEdgeStripsTheNodeSet(t *testing.T) {
	p := &InternalReverseProxy{}
	header := http.Header{InstanceNodesHeader: {"node-a=2"}, InstanceNodeHeader: {"node-a"}}
	p.takeInstanceNode(httptest.NewRequest(http.MethodGet, "/", nil), header)
	if len(header) != 0 {
		t.Fatalf("the edge passed on %v", header)
	}
}
