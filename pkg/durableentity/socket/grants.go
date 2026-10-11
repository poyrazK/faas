package socket

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/durableentity"
)

const BrowserProtocol = "gregale.entity.v1"
const CredentialProtocol = "gregale.grant."

var ErrGrantDenied = errors.New("session grant denied")

// Grant is a server-issued capability, not an end-user identity assertion.
// Credentials and owner API key hashes are never persisted in this record.
type Grant struct {
	ID               string           `json:"id"`
	Entity           durableentity.ID `json:"entity"`
	Environment      string           `json:"environment,omitempty"`
	PlatformTenantID string           `json:"platform_tenant_id,omitempty"`
	IssuerID         string           `json:"issuer_id"`
	SessionID        string           `json:"session_id"`
	Generation       uint64           `json:"generation"`
	Origin           string           `json:"origin"`
	Commands         []string         `json:"commands"`
	ExpiresAt        time.Time        `json:"expires_at"`
	Digest           string           `json:"digest"`
	Revoked          bool             `json:"revoked"`
}
type Grants struct {
	Store  durableentity.ObjectStore
	Quotas *Quotas
}

func ValidOrigin(origin string) bool {
	u, err := url.Parse(origin)
	return err == nil && u.Host != "" && u.User == nil && u.Path == "" && u.RawQuery == "" && u.Fragment == "" && u.String() == origin && (u.Scheme == "https" || (u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1")))
}
func grantIdentity(s string) bool {
	return strings.TrimSpace(s) != "" && len(s) <= api.MaxDurableEntityIdentityBytes && utf8.ValidString(s) && !strings.ContainsFunc(s, unicode.IsControl)
}
func sessionUUID(s string) bool {
	u, err := uuid.Parse(s)
	return err == nil && u.Version() == 4 && u.String() == s
}
func (g *Grants) Mint(ctx context.Context, grant Grant, ttl time.Duration) (Grant, string, error) {
	if !ValidOrigin(grant.Origin) || !sessionUUID(grant.SessionID) || grant.Generation > 9007199254740991 || ttl < time.Second || ttl > 5*time.Minute || grant.IssuerID == "" || !grantIdentity(grant.Entity.AccountID) || !grantIdentity(grant.Entity.AppID) || !grantIdentity(grant.Entity.Namespace) || !grantIdentity(grant.Entity.Key) || len(grant.Commands) == 0 || len(grant.Commands) > 4 {
		return Grant{}, "", ErrGrantDenied
	}
	seen := map[string]bool{}
	for _, c := range grant.Commands {
		if seen[c] || (c != "open" && c != "message" && c != "close" && c != "status") {
			return Grant{}, "", ErrGrantDenied
		}
		seen[c] = true
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return Grant{}, "", err
	}
	grant.ID = uuid.NewString()
	if g.Quotas != nil {
		scope, err := scopeFor(grant.Entity)
		if err != nil {
			return Grant{}, "", err
		}
		grant.ID = scopeKey(scope) + "." + grant.ID
	}
	now := time.Now().UTC()
	if g.Quotas != nil {
		now = g.Quotas.now()
	}
	grant.ExpiresAt = now.Add(ttl)
	grant.Revoked = false
	token := grant.ID + "." + base64.RawURLEncoding.EncodeToString(secret)
	digest := sha256.Sum256([]byte(token))
	grant.Digest = hex.EncodeToString(digest[:])
	if g.Quotas != nil {
		committed, err := g.Quotas.mint(ctx, grant)
		if err != nil {
			return Grant{}, "", err
		}
		return committed, token, nil
	}
	body, err := json.Marshal(grant)
	if err != nil {
		return Grant{}, "", err
	}
	if _, err = g.Store.Put(ctx, "session-grants/v1/"+grant.ID, body, ""); err != nil {
		return Grant{}, "", err
	}
	return grant, token, nil
}
func (g *Grants) Read(ctx context.Context, id string) (Grant, string, error) {
	if g.Quotas != nil {
		return g.Quotas.readGrant(ctx, id)
	}
	if !sessionUUID(id) {
		return Grant{}, "", ErrGrantDenied
	}
	body, version, err := g.Store.Get(ctx, "session-grants/v1/"+id, 16<<10)
	if errors.Is(err, durableentity.ErrNotFound) {
		return Grant{}, "", ErrGrantDenied
	}
	if err != nil {
		return Grant{}, "", err
	}
	var grant Grant
	if json.Unmarshal(body, &grant) != nil || grant.ID != id {
		return Grant{}, "", ErrGrantDenied
	}
	return grant, version, nil
}
func (g *Grants) Authenticate(ctx context.Context, token, origin string) (Grant, error) {
	parts := strings.Split(token, ".")
	if (len(parts) != 2 && len(parts) != 3) || len(parts[len(parts)-1]) != 43 {
		return Grant{}, ErrGrantDenied
	}
	secret, err := base64.RawURLEncoding.DecodeString(parts[len(parts)-1])
	if err != nil || len(secret) != 32 {
		return Grant{}, ErrGrantDenied
	}
	grant, _, err := g.Read(ctx, strings.Join(parts[:len(parts)-1], "."))
	if err != nil {
		return Grant{}, err
	}
	digest := sha256.Sum256([]byte(token))
	expected, err := hex.DecodeString(grant.Digest)
	now := time.Now()
	if g.Quotas != nil {
		now = g.Quotas.now()
	}
	if err != nil || subtle.ConstantTimeCompare(digest[:], expected) != 1 || grant.Revoked || !now.Before(grant.ExpiresAt) || !ValidOrigin(origin) || origin != grant.Origin {
		return Grant{}, ErrGrantDenied
	}
	return grant, nil
}
func (g *Grants) Revoke(ctx context.Context, id, account, app string) error {
	if g.Quotas != nil {
		return g.Quotas.revoke(ctx, id, account, app)
	}
	for attempt := 0; attempt < 3; attempt++ {
		grant, version, err := g.Read(ctx, id)
		if err != nil {
			return err
		}
		if grant.Entity.AccountID != account || grant.Entity.AppID != app {
			return ErrGrantDenied
		}
		if grant.Revoked {
			return nil
		}
		grant.Revoked = true
		body, err := json.Marshal(grant)
		if err != nil {
			return err
		}
		_, err = g.Store.Put(ctx, "session-grants/v1/"+id, body, version)
		if errors.Is(err, durableentity.ErrConflict) {
			continue
		}
		return err
	}
	return durableentity.ErrConflict
}

// Authorize checks the command before invocation or receipt replay. Service-only
// advance/reclaim and legacy unfenced commands can never enter through a grant.
func (g Grant) Authorize(frame Frame) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(frame.Payload, &fields) != nil {
		return false
	}
	var kind, session, query string
	if json.Unmarshal(fields["kind"], &kind) != nil || json.Unmarshal(fields["session_id"], &session) != nil || session != g.SessionID {
		return false
	}
	if kind != "open" && kind != "message" && kind != "close" && kind != "status" {
		return false
	}
	allowed := false
	for _, c := range g.Commands {
		if c == kind {
			allowed = true
		}
	}
	if !allowed {
		return false
	}
	expected := ""
	count := 0
	if kind == "status" {
		if json.Unmarshal(fields["query_id"], &query) != nil || !sessionUUID(query) {
			return false
		}
		count = 3
		expected = "session/status/" + session + "/" + query
	} else {
		var generation, sequence uint64
		if json.Unmarshal(fields["generation"], &generation) != nil || generation != g.Generation {
			return false
		}
		expected = fmt.Sprintf("session/g%d/%s/%s", generation, session, kind)
		if kind == "open" {
			count = 3
		} else {
			if json.Unmarshal(fields["sequence"], &sequence) != nil || sequence == 0 || sequence > 9007199254740991 {
				return false
			}
			expected += fmt.Sprintf("/%d", sequence)
			count = 4
			if kind == "message" {
				if _, ok := fields["payload"]; !ok {
					return false
				}
				count++
			}
		}
	}
	return len(fields) == count && frame.RequestID == expected
}
