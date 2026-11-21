// Package toolconnection validates immutable execution-only OAuth authority.
// Routine access/refresh token renewal never changes its authorization digest.
package toolconnection

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/toolgrant"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

func ValidURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && len(raw) <= api.ToolOAuthURLMaxBytes && u.Scheme == "https" && u.Host != "" && u.User == nil && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && u.Opaque == "" && api.ValidMCPProbeHost(u.Host)
}

func ValidCallback(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > api.ToolOAuthURLMaxBytes || u.Scheme != "http" || u.User != nil || u.Path != "/callback" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return false
	}
	host, port, err := net.SplitHostPort(u.Host)
	n, e := strconv.Atoi(port)
	return err == nil && e == nil && host == "127.0.0.1" && n > 0 && n <= api.MCPCatalogOAuthMaxCallbackPort && u.Host == net.JoinHostPort(host, strconv.Itoa(n))
}

func ValidAuthorizationState(raw string) bool {
	b, err := base64.RawURLEncoding.DecodeString(raw)
	return len(raw) == 43 && err == nil && len(b) == 32
}

func ValidScopes(scopes []string) bool {
	if len(scopes) == 0 || len(scopes) > api.ToolOAuthScopesMax {
		return false
	}
	seen := map[string]bool{}
	for _, scope := range scopes {
		if scope == "" || len(scope) > api.ToolOAuthScopeMaxBytes || seen[scope] {
			return false
		}
		seen[scope] = true
		for _, r := range scope {
			if r < 0x21 || r > 0x7e || r == '"' || r == '\\' {
				return false
			}
		}
	}
	return true
}

func ValidateIntent(in api.ToolOAuthConnectionIntent) error {
	if !api.ValidAppSlug(in.App) || in.Scope != api.DefaultEnvScope || !ValidURL(in.Issuer) || !ValidURL(in.TokenEndpoint) || !ValidURL(in.Resource) || len(in.ClientID) == 0 || len(in.ClientID) > api.ToolOAuthClientIDMaxBytes || strings.ContainsAny(in.ClientID, "\x00\r\n") || !ValidScopes(in.Scopes) {
		return errors.New("invalid execution OAuth connection")
	}
	if in.Guided {
		if !ValidURL(in.AuthorizationEndpoint) || in.RefreshTokenSecretRef != "" || in.ClientSecretRef != "" || in.TokenAuthMethod != "none" {
			return errors.New("guided execution OAuth requires public-client PKCE without source secret references")
		}
	} else if in.AuthorizationEndpoint != "" || !toolgrant.ValidCredentialRef(in.RefreshTokenSecretRef) {
		return errors.New("invalid execution OAuth source")
	}
	if in.TokenAuthMethod != "none" && in.TokenAuthMethod != "client_secret_basic" {
		return errors.New("unsupported token authentication method")
	}
	if in.TokenAuthMethod == "none" && in.ClientSecretRef != "" || in.TokenAuthMethod == "client_secret_basic" && !toolgrant.ValidCredentialRef(in.ClientSecretRef) {
		return errors.New("invalid client credential reference")
	}
	raw, _ := json.Marshal(in)
	if len(raw) > api.ToolOAuthConnectionIntentMaxBytes {
		return errors.New("connection intent exceeds review bounds")
	}
	return nil
}

func Digest(r api.ToolOAuthConnectionRecord) string {
	in := r.Intent
	in.Scopes = append([]string{}, in.Scopes...)
	slices.Sort(in.Scopes)
	raw, _ := json.Marshal(struct {
		ID, Account, App, Project, Source, ClientSource string
		Intent                                          api.ToolOAuthConnectionIntent
	}{r.ID, r.AccountID, r.AppID, r.ProjectID, r.SourceSHA256, r.ClientSourceSHA256, in})
	return toolpublication.Digest(raw)
}

func ValidateRecord(r api.ToolOAuthConnectionRecord) error {
	if r.Intent.Guided && r.SourceSHA256 != GuidedSourceDigest() {
		return errors.New("invalid guided execution OAuth source")
	}
	if !toolgrant.ValidID(r.ID) || !toolgrant.ValidResourceID(r.AccountID) || !toolgrant.ValidResourceID(r.AppID) || r.ProjectID != "" && !toolgrant.ValidResourceID(r.ProjectID) || ValidateIntent(r.Intent) != nil || r.CreatedAt.IsZero() || !toolpublication.ValidID(r.SourceSHA256) || r.Intent.ClientSecretRef != "" && !toolpublication.ValidID(r.ClientSourceSHA256) || r.Intent.ClientSecretRef == "" && r.ClientSourceSHA256 != "" || r.BindingSHA256 != Digest(r) {
		return errors.New("invalid execution OAuth binding")
	}
	return nil
}

// GuidedSourceDigest identifies the credential source mode, not a token. The
// immutable connection ID and complete provider intent remain in Digest.
func GuidedSourceDigest() string {
	return toolpublication.Digest([]byte("gregale-guided-tool-oauth-v1"))
}

func SameIntent(a, b api.ToolOAuthConnectionIntent) bool {
	a.Scopes = append([]string{}, a.Scopes...)
	b.Scopes = append([]string{}, b.Scopes...)
	slices.Sort(a.Scopes)
	slices.Sort(b.Scopes)
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
func Covers(approved, required []string) bool {
	for _, scope := range required {
		if !slices.Contains(approved, scope) {
			return false
		}
	}
	return true
}

func View(r api.ToolOAuthConnectionRecord, lease *time.Time, now time.Time) api.ToolOAuthConnectionRecord {
	if r.RevokedAt != nil {
		r.State = "revoked"
	} else if r.State == "refreshing" && lease != nil && !now.Before(*lease) {
		r.State = "reconnect_required"
	} else if r.State == "ready" && (r.ExpiresAt == nil || !r.ExpiresAt.After(now.Add(api.ToolOAuthRefreshSkew))) {
		r.State = "refresh_required"
	}
	if r.State == "reconnect_required" {
		r.RecoveryAction = "reconnect"
	}
	if r.State == "revoked" {
		r.RecoveryAction = "review_connection"
	}
	return r
}
