package managedrealtime

import (
	"context"
	"fmt"
	"sync"

	"github.com/onebox-faas/faas/pkg/realtime"
	"github.com/onebox-faas/faas/pkg/state"
)

// Channel occupancy (ADR-1324): apid asks every active realtime node for its
// per-channel subscriber counts and sums them. Nodes are asked directly, not
// through the channel route directory, because SSE streams and ticket
// subscriptions do not register routes.

// realtimeNodeOccupancyReader is the node capability behind an occupancy read.
type realtimeNodeOccupancyReader interface {
	ChannelOccupancy(context.Context, string, []string) ([]realtime.ChannelOccupancy, error)
}

// realtimeOccupancyReader is the owner capability the API and the realtime
// fleet use.
type realtimeOccupancyReader interface {
	ChannelOccupancy(context.Context, string, []string) (realtime.OccupancyInventory, error)
}

func (o LocalRealtimeNodeOperator) ChannelOccupancy(ctx context.Context, endpointID string, channels []string) ([]realtime.ChannelOccupancy, error) {
	return o.Client.ChannelOccupancy(ctx, endpointID, channels)
}

func (o remoteRealtimeNodeOperator) ChannelOccupancy(ctx context.Context, endpointID string, channels []string) ([]realtime.ChannelOccupancy, error) {
	return o.client.ChannelOccupancy(ctx, endpointID, channels)
}

// localOccupancyNode names the single node of a single-box deployment.
const localOccupancyNode = "local"

// ChannelOccupancy reads the single local node.
func (o LocalRealtimeOwner) ChannelOccupancy(ctx context.Context, endpointID string, channels []string) (realtime.OccupancyInventory, error) {
	counts, err := o.Client.ChannelOccupancy(ctx, endpointID, channels)
	if err != nil {
		return realtime.OccupancyInventory{NodesUnavailable: 1}, fmt.Errorf("%w: %w", errManagedRealtimeOwnerUnavailable, err)
	}
	return realtime.OccupancyInventory{Nodes: map[string]map[string]int{localOccupancyNode: occupancyByChannel(counts)}, NodesQueried: 1}, nil
}

// ChannelOccupancy reads every active realtime node concurrently. An
// unreachable node is reported as unavailable, never guessed.
func (o *leasedRealtimeOwner) ChannelOccupancy(ctx context.Context, endpointID string, channels []string) (realtime.OccupancyInventory, error) {
	inventory := realtime.OccupancyInventory{Nodes: map[string]map[string]int{}}
	if o.nodes == nil {
		return inventory, errManagedRealtimeOwnerUnavailable
	}
	nodes, err := o.nodes.ActiveComputeNodes(ctx)
	if err != nil {
		return inventory, fmt.Errorf("%w: list active nodes: %w", errManagedRealtimeOwnerUnavailable, err)
	}
	var mu sync.Mutex
	var workers sync.WaitGroup
	jobs := make(chan state.ComputeNode)
	for range min(len(nodes), maxConcurrentRealtimePublishes) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for node := range jobs {
				counts, ok := o.nodeOccupancy(ctx, node, endpointID, channels)
				mu.Lock()
				if ok {
					inventory.NodesQueried++
					inventory.Nodes[node.ID] = counts
				} else {
					inventory.NodesUnavailable++
				}
				mu.Unlock()
			}
		}()
	}
	for _, node := range nodes {
		jobs <- node
	}
	close(jobs)
	workers.Wait()
	if inventory.NodesQueried == 0 && inventory.NodesUnavailable > 0 {
		return inventory, fmt.Errorf("%w: no active realtime nodes reported occupancy", errManagedRealtimeOwnerUnavailable)
	}
	return inventory, nil
}

func (o *leasedRealtimeOwner) nodeOccupancy(ctx context.Context, node state.ComputeNode, endpointID string, channels []string) (map[string]int, bool) {
	op, err := o.nodeOperator(node)
	if err != nil {
		return nil, false
	}
	reader, ok := op.(realtimeNodeOccupancyReader)
	if !ok {
		return nil, false
	}
	counts, err := reader.ChannelOccupancy(ctx, endpointID, channels)
	if err != nil {
		return nil, false
	}
	return occupancyByChannel(counts), true
}

func occupancyByChannel(counts []realtime.ChannelOccupancy) map[string]int {
	out := make(map[string]int, len(counts))
	for _, count := range counts {
		out[count.Channel] = count.Subscribers
	}
	return out
}
