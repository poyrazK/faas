package realtime

// Preview features need the node's flag and an endpoint marked by apid for an
// account in the realtime preview rollout (ADR-20261019102657).

func (m *Manager) resumePreview(e Endpoint) bool    { return m.cfg.ResumePreview && e.Preview }
func (m *Manager) ticketPreview(e Endpoint) bool    { return m.cfg.TicketPreview && e.Preview }
func (m *Manager) accessPreview(e Endpoint) bool    { return m.cfg.AccessPreview && e.Preview }
func (m *Manager) occupancyPreview(e Endpoint) bool { return m.cfg.OccupancyPreview && e.Preview }
