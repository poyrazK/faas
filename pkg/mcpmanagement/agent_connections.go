package mcpmanagement

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/safetext"
)

// Connected agents (ADR-20261030133644): customers list and revoke hosted
// connections through apid by their mcp-login keys. The bridge names each
// key's client, retires the key when the connection ends, and lets go of a
// connection whose key apid has revoked.

// agentClientSite is where the client was verified: the host serving its
// metadata document, else the host of its HTTPS redirect URI, the only place
// the authorization code can go. A loopback client has none.
func agentClientSite(c *oauthClient, redirect string) string {
	raw := redirect
	if c != nil && c.Metadata {
		raw = c.ID
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// agentClientName is the name consent showed, printable and bounded as apid
// requires.
func agentClientName(name string) string {
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, safetext.Clean(name))
	return strings.TrimSpace(safetext.TruncateRunes(strings.TrimSpace(name), api.AgentClientNameMax))
}

// nameAgentClient labels a new hosted connection's key with its client. It
// is best effort: sign-in goes on when apid refuses or predates the label.
func (r *remoteServer) nameAgentClient(ctx context.Context, client *api.Client, flow *authorizationRequest) {
	r.mu.Lock()
	c := r.clients[flow.ClientID]
	name := ""
	if c != nil {
		name = c.Name
	}
	site := agentClientSite(c, flow.Redirect)
	r.mu.Unlock()
	if name = agentClientName(name); name == "" {
		name = site
	}
	if name != "" {
		_ = client.NameAgentClient(ctx, api.AgentClientRequest{ClientName: name, ClientSite: site})
	}
}

// retireCredentialLocked revokes the mcp-login key of an ending connection
// in the background, so apid lists only live connections. A connection made
// with the customer's own key (api-key onboarding) keeps it.
func (r *remoteServer) retireCredentialLocked(g *oauthGrant) {
	if !g.renewable || g.client == nil || !time.Now().Before(g.credentialExpires) {
		return
	}
	client, timeout := g.client, r.opts.Timeout
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		_ = client.DisconnectAgent(ctx)
	}()
}

// keyRevoked reports whether apid refused a connection's key as revoked or
// expired, as opposed to failing to answer.
func keyRevoked(err error) bool {
	var apiErr *api.APIError
	return errors.As(err, &apiErr) && apiErr.Problem.Status == http.StatusUnauthorized
}

// dropRevoked lets go of a connection whose key apid refused.
func (r *remoteServer) dropRevoked(g *oauthGrant) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.grants[g.id] != g || g.revoked {
		return
	}
	// The key is already gone; there is nothing to retire.
	g.credentialExpires = time.Now()
	r.revokeLocked(g)
	// A commit failure disables the store; the hosted bridge then restarts.
	_ = r.opts.StateStore.update("oauth", r.snapshotLocked())
}

// releaseRevokedGrants re-checks an account's hosted connections with apid
// before a sign-in is refused at the per-account limit, so a connection the
// customer revoked frees its slot.
func (r *remoteServer) releaseRevokedGrants(ctx context.Context, accountID string) {
	r.mu.Lock()
	if !r.opts.Hosted || r.accountGrantsLocked(accountID) < hostedGrantsPerAccount {
		r.mu.Unlock()
		return
	}
	var held []*oauthGrant
	for _, g := range r.grants {
		if g.accountID == accountID && !g.revoked && g.renewable {
			held = append(held, g)
		}
	}
	r.mu.Unlock()
	for _, g := range held {
		if _, err := g.client.Whoami(ctx); keyRevoked(err) {
			r.dropRevoked(g)
		}
	}
}
