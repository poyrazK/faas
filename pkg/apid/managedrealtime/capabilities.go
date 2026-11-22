package managedrealtime

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// CapabilityAvailable reports whether the account is served a realtime
// preview capability by the same gates its routes apply: the preview rollout,
// the feature's platform flag, and apid's own daemon switch. ok is false for
// keys realtime does not own. GET /v1/capabilities (and gregale_context,
// which agents treat as truth) checked only the platform flag, so agents
// built against retained history, tickets and occupancy that answered 404.
func (s *Service) CapabilityAvailable(r *http.Request, acct state.Account, key string) (available, ok bool) {
	visible := s.PreviewVisible(acct.ID)
	switch key {
	case "realtime-access":
		return visible && s.previewOn(r, acct, platformflags.RealtimeAccess, s.AccessPreviewEnabled), true
	case "realtime-tickets":
		return visible && s.previewOn(r, acct, platformflags.RealtimeTickets, s.TicketsPreviewEnabled), true
	case "realtime-occupancy":
		return visible && s.previewOn(r, acct, platformflags.RealtimeOccupancy, s.OccupancyPreviewEnabled), true
	case "realtime-channels", "realtime-workers", "realtime-push":
		// Retained history, consumer groups, workers and push all answer
		// "preview unavailable" while the retained-history switch is off.
		return visible && s.HistoryPreviewEnabled, true
	case "realtime-publishers":
		return visible, true
	}
	return false, false
}
