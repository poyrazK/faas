package managedrealtime

import (
	"fmt"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

const realtimeOccupancyDocs = "https://gregale.dev/docs/realtime#channel-occupancy"

// GetManagedRealtimeOccupancy reports how many subscriptions and presence
// members each requested channel has across the fleet (ADR-1324).
func (s *Service) GetManagedRealtimeOccupancy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if s.previewUnavailable(w, acct.ID, s.OccupancyPreviewEnabled, "channel occupancy preview unavailable") {
		return
	}
	row, _, ok := s.loadManagedRealtimeEndpoint(w, r, acct)
	if !ok {
		return
	}
	channels, problem := occupancyChannels(r.URL.Query()["channel"])
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	reader, ok := s.Owner.(realtimeOccupancyReader)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("managed realtime owner unavailable"))
		return
	}
	inventory, err := reader.ChannelOccupancy(r.Context(), row.ID, channels)
	if err != nil && inventory.NodesQueried == 0 {
		api.WriteProblem(w, api.ErrCapacity("managed realtime owner unavailable"))
		return
	}
	presence, _ := s.Host.Store().(state.ManagedRealtimePresenceStore)
	out := make([]api.ManagedRealtimeChannelOccupancy, 0, len(channels))
	for _, channel := range channels {
		entry := api.ManagedRealtimeChannelOccupancy{Channel: channel, Subscribers: inventory.Subscribers(channel, "")}
		if presence != nil {
			// The snapshot holds one entry per member, whatever its connections.
			members, err := presence.ReadManagedRealtimePresenceSnapshot(r.Context(), row.ID, channel)
			if err != nil {
				api.WriteProblem(w, api.ErrCapacity("managed realtime presence unavailable"))
				return
			}
			entry.PresenceMembers = len(members)
		}
		out = append(out, entry)
	}
	httpkit.WriteJSON(w, http.StatusOK, api.ManagedRealtimeOccupancyResponse{
		Channels:         out,
		Partial:          inventory.NodesUnavailable > 0,
		NodesQueried:     inventory.NodesQueried,
		NodesUnavailable: inventory.NodesUnavailable,
	})
}

// occupancyChannels validates the requested channels and drops repeats,
// keeping the order asked.
func occupancyChannels(values []string) ([]string, *api.Problem) {
	if len(values) == 0 || len(values) > api.RealtimeOccupancyChannelsMax {
		return nil, api.ErrRealtimeInvalid(fmt.Sprintf("occupancy reads 1 to %d channels per request; got %d", api.RealtimeOccupancyChannelsMax, len(values))).
			WithLimit(api.RealtimeOccupancyChannelsMax, int64(len(values))).
			WithDocs(realtimeOccupancyDocs)
	}
	seen := make(map[string]struct{}, len(values))
	channels := make([]string, 0, len(values))
	for _, channel := range values {
		if problem := validateManagedRealtimeChannel(channel); problem != nil {
			return nil, problem
		}
		if _, ok := seen[channel]; ok {
			continue
		}
		seen[channel] = struct{}{}
		channels = append(channels, channel)
	}
	return channels, nil
}
