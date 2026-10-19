package receivers

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/realtime"
	"github.com/onebox-faas/faas/pkg/state"
)

type fakeOccupancyOwner struct {
	inventory realtime.OccupancyInventory
	err       error
	calls     int
}

func (f *fakeOccupancyOwner) ChannelOccupancy(context.Context, string, []string) (realtime.OccupancyInventory, error) {
	f.calls++
	return f.inventory, f.err
}

// adr: 1324 — a node gets every other node's counts, from a cache shared by
// nodes asking about the same channel.
func TestOccupancyOperationRPC(t *testing.T) {
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
	endpoint := func(enabled bool) state.ManagedRealtimeEndpoint {
		ep, err := store.CreateManagedRealtimeEndpointIfUnderQuota(ctx, state.ManagedRealtimeEndpoint{
			AccountID: acct.ID, AppID: app.ID, CallbackURL: "https://example.com/cb", Enabled: enabled,
		}, 10, 10)
		if err != nil {
			t.Fatal(err)
		}
		return ep
	}
	enabled, disabled := endpoint(true), endpoint(false)
	owner := &fakeOccupancyOwner{inventory: realtime.OccupancyInventory{
		Nodes: map[string]map[string]int{"n1": {"lobby": 2}, "n2": {"lobby": 5, "game": 1}}, NodesQueried: 2,
	}}
	receiver := &realtimeFleetReceiver{store: store, nodes: fakeUsageNodes{}, occupancy: owner, occupancyCache: newRealtimeOccupancyCache()}
	client := newUsageTestClient(t, receiver, "node-a") // node-a is n1

	counts, err := client.ReadRemoteOccupancy(ctx, enabled.ID, []string{"lobby", "game"})
	if err != nil {
		t.Fatal(err)
	}
	if counts["lobby"] != 5 || counts["game"] != 1 {
		t.Fatalf("counts = %v, want the other node's lobby 5 and game 1", counts)
	}
	if _, err := client.ReadRemoteOccupancy(ctx, enabled.ID, []string{"lobby"}); err != nil || owner.calls != 1 {
		t.Fatalf("second read within the cache TTL: err %v, fleet reads %d, want 1", err, owner.calls)
	}

	tooMany := strings.Fields(strings.Repeat("c ", api.RealtimeOccupancyChannelsMax+1))
	for name, channels := range map[string][]string{"no channel": nil, "too many": tooMany, "invalid": {"a/b"}} {
		if _, err := client.ReadRemoteOccupancy(ctx, enabled.ID, channels); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("%s: err = %v, want InvalidArgument", name, err)
		}
	}
	if _, err := client.ReadRemoteOccupancy(ctx, disabled.ID, []string{"lobby"}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("disabled endpoint err = %v, want FailedPrecondition", err)
	}
	unknown := newUsageTestClient(t, receiver, "node-x")
	if _, err := unknown.ReadRemoteOccupancy(ctx, enabled.ID, []string{"lobby"}); status.Code(err) != codes.NotFound {
		t.Fatalf("unregistered node err = %v, want NotFound", err)
	}
	owner.inventory, owner.err = realtime.OccupancyInventory{NodesUnavailable: 2}, errors.New("no node answered")
	if _, err := client.ReadRemoteOccupancy(ctx, enabled.ID, []string{"uncached"}); status.Code(err) != codes.Unavailable {
		t.Fatalf("fleet read failure err = %v, want Unavailable", err)
	}
}
