package mcpmanagement

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

// These bound one bridge's in-memory authorization state, not account quotas.
const maxOAuthEntries = 256
const maxRefreshHistory = 4096
const oauthFlowTTL = 5 * time.Minute
const oauthCodeTTL = time.Minute
const oauthAccessTTL = 15 * time.Minute
const oauthGrantTTL = 8 * time.Hour

type oauthClient struct {
	ID        string
	Name      string
	Redirects []string
	Expires   time.Time
}

type authorizationRequest struct {
	ID         string
	ClientID   string
	Redirect   string
	State      string
	Challenge  string
	Scopes     []string
	CSRFHash   string
	CookieHash string
	GrantID    string
	Expires    time.Time
	NativeCode string
	NativeURL  string
	// Capabilities are the extra scopes chosen before sign-in; the browser
	// code carries exactly these (ADR-20261026195952).
	Capabilities []string
}

type authorizationCode struct {
	ClientID  string
	Redirect  string
	Challenge string
	GrantID   string
	Expires   time.Time
}

type oauthGrant struct {
	id        string
	clientID  string
	accountID string
	scopes    []string
	// approved is set once consent completes. server and management are
	// built on first use and dropped when idle (grant_servers.go); inflight
	// counts the requests using them.
	approved              bool
	server                *mcp.Server
	inflight              int
	lastUsed              time.Time
	client                *api.Client
	expires               time.Time
	accessExpires         time.Time
	accessHash            string
	refreshHash           string
	revoked               bool
	management            *management
	allowedApps           map[string]string
	availableApps         []api.AppResponse
	credentialExpires     time.Time
	environments          *EnvironmentConsent
	availableEnvironments []EnvironmentGrant
	capabilities          []string
}

func randomToken(prefix string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(b), nil
}

func tokenHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func (r *remoteServer) pruneLocked() {
	now := time.Now()
	for id, c := range r.clients {
		if now.After(c.Expires) {
			delete(r.clients, id)
		}
	}
	for id, request := range r.requests {
		if now.After(request.Expires) {
			delete(r.requests, id)
		}
	}
	for id, code := range r.codes {
		if now.After(code.Expires) {
			if g := r.grants[code.GrantID]; g != nil && g.accessHash == "" {
				r.revokeLocked(g)
			}
			delete(r.codes, id)
		}
	}
	for id, g := range r.grants {
		if now.After(g.expires) || g.revoked {
			r.revokeLocked(g)
			delete(r.grants, id)
		}
	}
	for key, g := range r.usedRefresh {
		if g.revoked || now.After(g.expires) {
			delete(r.usedRefresh, key)
		}
	}
	r.dropIdleServersLocked(now)
}

func (r *remoteServer) revokeLocked(g *oauthGrant) {
	r.audit("mcp.grant.revoked", g)
	g.revoked = true
	g.management.stopResourceSubscriptions()
	if g.management != nil && g.management.grantLifecycle != nil {
		g.management.grantLifecycle.cancelAll()
	}
	delete(r.grants, g.id)
	delete(r.access, g.accessHash)
	delete(r.refresh, g.refreshHash)
	for key, used := range r.usedRefresh {
		if used == g {
			delete(r.usedRefresh, key)
		}
	}
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}

func (r *remoteServer) mintLocked(g *oauthGrant) (tokenResponse, error) {
	access, err := randomToken("gmcp_at_")
	if err != nil {
		return tokenResponse{}, err
	}
	refresh, err := randomToken("gmcp_rt_")
	if err != nil {
		return tokenResponse{}, err
	}
	delete(r.access, g.accessHash)
	delete(r.refresh, g.refreshHash)
	g.accessHash, g.refreshHash = tokenHash(access), tokenHash(refresh)
	g.accessExpires = minTime(time.Now().Add(oauthAccessTTL), g.expires)
	r.access[g.accessHash], r.refresh[g.refreshHash] = g, g
	return tokenResponse{AccessToken: access, TokenType: "Bearer", ExpiresIn: int(time.Until(g.accessExpires).Seconds()), RefreshToken: refresh, Scope: strings.Join(g.scopes, " ")}, nil
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
