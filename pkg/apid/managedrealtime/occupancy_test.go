package managedrealtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/realtime"
	"github.com/onebox-faas/faas/pkg/state"
)

// fakeOccupancyNode answers occupancy reads with fixed counts.
type fakeOccupancyNode struct {
	*fakeRealtimeNode
	counts map[string]int
	err    error
}

func (n *fakeOccupancyNode) ChannelOccupancy(_ context.Context, _ string, channels []string) ([]realtime.ChannelOccupancy, error) {
	if n.err != nil {
		return nil, n.err
	}
	out := make([]realtime.ChannelOccupancy, 0, len(channels))
	for _, channel := range channels {
		out = append(out, realtime.ChannelOccupancy{Channel: channel, Subscribers: n.counts[channel]})
	}
	return out, nil
}

func TestLeasedRealtimeOwnerSumsChannelOccupancy(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	var nodes []state.ComputeNode
	for _, name := range []string{"node-a", "node-b", "node-c"} {
		node, err := store.CreateComputeNode(ctx, state.ComputeNode{Name: name, Active: true})
		if err != nil {
			t.Fatal(err)
		}
		nodes = append(nodes, node)
	}
	fakes := map[string]RealtimeNodeOperator{
		nodes[0].ID: &fakeOccupancyNode{fakeRealtimeNode: &fakeRealtimeNode{}, counts: map[string]int{"lobby": 2, "game": 1}},
		nodes[1].ID: &fakeOccupancyNode{fakeRealtimeNode: &fakeRealtimeNode{}, counts: map[string]int{"lobby": 3}},
		nodes[2].ID: &fakeOccupancyNode{fakeRealtimeNode: &fakeRealtimeNode{}, err: errors.New("node unavailable")},
	}
	owner := NewLeasedRealtimeOwner(store, store, "", nil, nil)
	owner.clientFor = func(node state.ComputeNode) (RealtimeNodeOperator, error) {
		if fake, ok := fakes[node.ID]; ok {
			return fake, nil
		}
		return nil, errors.New("unknown node")
	}
	inventory, err := owner.ChannelOccupancy(ctx, "endpoint-1", []string{"lobby", "game"})
	if err != nil {
		t.Fatal(err)
	}
	active, err := store.ActiveComputeNodes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if inventory.NodesQueried != 2 || inventory.NodesUnavailable != len(active)-2 {
		t.Fatalf("queried %d unavailable %d, want 2 and %d", inventory.NodesQueried, inventory.NodesUnavailable, len(active)-2)
	}
	if lobby, game := inventory.Subscribers("lobby", ""), inventory.Subscribers("game", ""); lobby != 5 || game != 1 {
		t.Fatalf("lobby %d game %d, want 5 and 1", lobby, game)
	}
	if others := inventory.Subscribers("lobby", nodes[0].ID); others != 3 {
		t.Fatalf("lobby without node-a = %d, want 3", others)
	}
}

// occupancyOwner serves fixed occupancy from a fake fleet.
type occupancyOwner struct {
	*fakeRealtimeNode
	inventory realtime.OccupancyInventory
	err       error
	asked     []string
}

func (o *occupancyOwner) ChannelOccupancy(_ context.Context, _ string, channels []string) (realtime.OccupancyInventory, error) {
	o.asked = channels
	return o.inventory, o.err
}

func TestGetManagedRealtimeOccupancy(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "occupancy-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "rooms", Status: state.AppActive, RAMMB: 512})
	if err != nil {
		t.Fatal(err)
	}
	ep, err := store.CreateManagedRealtimeEndpointIfUnderQuota(ctx, state.ManagedRealtimeEndpoint{
		AccountID: acct.ID, AppID: app.ID, CallbackURL: "https://example.com/cb", Enabled: true,
	}, 10, 10)
	if err != nil {
		t.Fatal(err)
	}
	// Two connections of one principal-scoped member, and a second member.
	for i, lease := range []struct{ member, connection string }{{"alice", "c1"}, {"alice", "c2"}, {"bob", "c3"}} {
		if _, err := store.UpsertManagedRealtimePresenceLease(ctx, state.ManagedRealtimePresenceLease{
			EndpointID: ep.ID, Channel: "lobby", NodeID: fmt.Sprintf("node-%d", i), ConnectionID: lease.connection,
			MemberID: lease.member, State: []byte(`{}`), ExpiresAt: time.Now().Add(time.Minute),
		}); err != nil {
			t.Fatal(err)
		}
	}
	owner := &occupancyOwner{fakeRealtimeNode: &fakeRealtimeNode{}, inventory: realtime.OccupancyInventory{
		Nodes:        map[string]map[string]int{"a": {"lobby": 4}, "b": {"lobby": 2, "game": 7}},
		NodesQueried: 2, NodesUnavailable: 1,
	}}
	svc := &Service{Host: &ticketTestHost{store: store, app: app}, Owner: owner, Preview: everyonePreview(t)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/apps/{slug}/realtime/endpoints/{id}/occupancy", func(w http.ResponseWriter, r *http.Request) { svc.GetManagedRealtimeOccupancy(w, r, acct) })
	get := func(query string) *httptest.ResponseRecorder {
		t.Helper()
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/apps/rooms/realtime/endpoints/"+ep.ID+"/occupancy"+query, nil))
		return rec
	}

	if rec := get("?channel=lobby"); rec.Code != http.StatusNotFound {
		t.Fatalf("occupancy without the preview = %d %s, want 404", rec.Code, rec.Body)
	}
	svc.OccupancyPreviewEnabled = true
	rec := get("?channel=lobby&channel=game&channel=lobby")
	var out api.ManagedRealtimeOccupancyResponse
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
		t.Fatalf("occupancy = %d %s", rec.Code, rec.Body)
	}
	if strings.Join(owner.asked, ",") != "lobby,game" {
		t.Fatalf("asked %v, want repeats dropped in order", owner.asked)
	}
	want := []api.ManagedRealtimeChannelOccupancy{{Channel: "lobby", Subscribers: 6, PresenceMembers: 2}, {Channel: "game", Subscribers: 7}}
	if len(out.Channels) != 2 || out.Channels[0] != want[0] || out.Channels[1] != want[1] {
		t.Fatalf("channels = %+v, want %+v", out.Channels, want)
	}
	if !out.Partial || out.NodesQueried != 2 || out.NodesUnavailable != 1 {
		t.Fatalf("partial fleet = %+v", out)
	}

	tooMany := "?channel=" + strings.Repeat("c&channel=", api.RealtimeOccupancyChannelsMax) + "c"
	for name, query := range map[string]string{"no channel": "", "too many": tooMany, "invalid channel": "?channel=a%2Fb"} {
		if rec := get(query); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s = %d %s, want 400", name, rec.Code, rec.Body)
		}
	}
	var problem api.Problem
	if rec := get(tooMany); json.Unmarshal(rec.Body.Bytes(), &problem) != nil || problem.Limit == nil || *problem.Limit != api.RealtimeOccupancyChannelsMax {
		t.Fatalf("limit problem = %s", rec.Body)
	}

	owner.inventory, owner.err = realtime.OccupancyInventory{NodesUnavailable: 3}, errManagedRealtimeOwnerUnavailable
	if rec := get("?channel=lobby"); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("no node answered = %d, want 503", rec.Code)
	}
}
