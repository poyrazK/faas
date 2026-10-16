package mcpmanagement

// Audit records contain server-owned identifiers and fixed event names. Keys,
// OAuth tokens, browser codes, requested arguments and app logs are excluded.
func (r *remoteServer) audit(event string, g *oauthGrant) {
	if r.opts.AuditLogger == nil {
		return
	}
	r.opts.AuditLogger.Info("management MCP authorization", "event", event, "grant_id", g.id, "client_id", g.clientID, "account_id", g.accountID)
}
