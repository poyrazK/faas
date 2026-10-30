package mcpmanagement

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// slideCredential follows apid's slide of a used mcp-login key
// (ADR-20261028205920). apid extends the key on each authenticated use,
// including the check every request makes, to a week from now, never past 30
// days after it was issued. The grant mirrors that bound after a verified
// request. Every request still verifies the key with apid, so the mirror only
// bounds tokens; it never extends access. State is written once the expiry
// has moved by credentialRenewInterval.
func (r *remoteServer) slideCredential(g *oauthGrant) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !g.renewable || g.revoked || r.grants[g.id] != g {
		return
	}
	next := minTime(time.Now().Add(api.MCPLoginKeyIdle), g.credentialIssued.Add(api.MCPLoginKeyMaxAge))
	if next.Sub(g.credentialExpires) < credentialRenewInterval {
		return
	}
	g.credentialExpires, g.expires = next, next
	// A commit failure disables the store; the hosted bridge then restarts.
	_ = r.opts.StateStore.update("oauth", r.snapshotLocked())
}
