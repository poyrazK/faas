package managedrealtime

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// Realtime capability availability (ADR-20261019102657, ADR 20261021080640).
// GET /v1/capabilities, the CLI and the MCP gregale_context tool read the
// capability registry; it must say what the realtime routes would answer for
// the account, not only what the capability flag says. Each decision below
// is the gate the capability's handlers apply.

// realtimePreviewCapabilities are the realtime capabilities whose routes
// answer as unregistered outside the preview rollout.
var realtimePreviewCapabilities = []string{
	"realtime-access",
	"realtime-channels",
	"realtime-occupancy",
	"realtime-publishers",
	"realtime-push",
	"realtime-tickets",
	"realtime-workers",
}

// CapabilityAvailable reports whether a realtime capability's routes serve
// the account on this control plane. owned is false for keys that are not
// realtime capabilities, which the caller decides itself.
func (s *Service) CapabilityAvailable(r *http.Request, acct state.Account, key string) (available, owned bool) {
	switch key {
	case "realtime", "realtime-connections":
		// Without a realtime node apid persists endpoints nobody serves.
		return s.Registrar != nil, true
	case "realtime-channels", "realtime-workers", "realtime-push", "realtime-publishers":
		// Retained history and everything built on it.
		return s.Registrar != nil && s.PreviewVisible(acct.ID) && s.HistoryPreviewEnabled, true
	case "realtime-occupancy":
		return s.Registrar != nil && s.PreviewVisible(acct.ID) &&
			s.previewOn(r, acct, platformflags.RealtimeOccupancy, s.OccupancyPreviewEnabled), true
	case "realtime-tickets":
		return s.Registrar != nil && s.PreviewVisible(acct.ID) &&
			s.previewOn(r, acct, platformflags.RealtimeTickets, s.TicketsPreviewEnabled), true
	case "realtime-access":
		return s.Registrar != nil && s.PreviewVisible(acct.ID) &&
			s.previewOn(r, acct, platformflags.RealtimeAccess, s.AccessPreviewEnabled), true
	}
	return false, false
}

// PreviewHiddenCapabilities lists the realtime capabilities hidden from an
// account outside the preview rollout. Their routes answer as unregistered,
// so clients hide them as they hide a capability whose flag is off.
func (s *Service) PreviewHiddenCapabilities(accountID string) []string {
	if s.PreviewVisible(accountID) {
		return nil
	}
	return append([]string(nil), realtimePreviewCapabilities...)
}
