package api

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

// RealtimeLimits describes the managed realtime endpoint allowance for a plan.
// It is kept separate from Limits so adding the opt-in realtime surface does
// not change the long-lived Limits struct consumed by every daemon.
type RealtimeLimits struct {
	Plan                Plan
	EndpointsPerApp     int
	EndpointsPerAccount int
}

var realtimeLimits = map[Plan]RealtimeLimits{
	PlanFree:  {Plan: PlanFree},
	PlanHobby: {Plan: PlanHobby, EndpointsPerApp: 2, EndpointsPerAccount: 10},
	PlanPro:   {Plan: PlanPro, EndpointsPerApp: 10, EndpointsPerAccount: 50},
	PlanScale: {Plan: PlanScale, EndpointsPerApp: 25, EndpointsPerAccount: 250},
}

func RealtimeLimitsFor(p Plan) (RealtimeLimits, bool) {
	l, ok := realtimeLimits[p]
	return l, ok
}

const (
	RealtimeCallbackAuthTokenMaxBytes = 256
	RealtimeAuthTokenMaxBytes         = 256
	RealtimePathMaxBytes              = 256
	RealtimeCallbackURLMaxBytes       = 2048
	RealtimeOriginMaxBytes            = 2048
	RealtimeMaxAllowedOrigins         = 16
	RealtimeMaxConnections            = 10_000
	RealtimeMaxConnectionAgeSeconds   = 7 * 24 * 60 * 60
	// RealtimeMessageMaxBytes is the maximum decoded payload accepted by the
	// public connection-management API. The daemon enforces its own limit too;
	// keeping the API bound explicit prevents oversized requests from reaching
	// an owner node.
	RealtimeMessageMaxBytes                       = 1 << 20
	RealtimeSecretMasked                          = "***"
	RealtimeAuthRotationDefaultGraceSeconds int64 = 300
	RealtimeAuthRotationMaxGraceSeconds     int64 = 24 * 60 * 60
)

const (
	RealtimeAuthModeNone         = "none"
	RealtimeAuthModeStaticBearer = "static_bearer"
	RealtimeAuthModeOIDCJWT      = "oidc_jwt"
)

const (
	DefaultRealtimeConnectPath    = "/realtime/connect"
	DefaultRealtimeMessagePath    = "/realtime/message"
	DefaultRealtimeDisconnectPath = "/realtime/disconnect"
)

type CreateManagedRealtimeEndpointRequest struct {
	CallbackURL             string            `json:"callback_url"`
	ConnectPath             string            `json:"connect_path,omitempty"`
	MessagePath             string            `json:"message_path,omitempty"`
	DisconnectPath          string            `json:"disconnect_path,omitempty"`
	CallbackAuthToken       string            `json:"callback_auth_token"`
	AuthToken               string            `json:"auth_token,omitempty"`
	AuthMode                string            `json:"auth_mode,omitempty"`
	AuthIssuer              string            `json:"auth_issuer,omitempty"`
	AuthJWKSURL             string            `json:"auth_jwks_url,omitempty"`
	AuthAudience            []string          `json:"auth_audience,omitempty"`
	AuthAlgorithms          []string          `json:"auth_algorithms,omitempty"`
	AuthRequiredClaims      map[string]string `json:"auth_required_claims,omitempty"`
	AllowedOrigins          []string          `json:"allowed_origins,omitempty"`
	MaxConnections          int               `json:"max_connections,omitempty"`
	MaxMessageBytes         int64             `json:"max_message_bytes,omitempty"`
	MaxConnectionAgeSeconds int64             `json:"max_connection_age_seconds,omitempty"`
	Enabled                 *bool             `json:"enabled,omitempty"`
}

type UpdateManagedRealtimeEndpointRequest struct {
	CallbackURL             *string            `json:"callback_url,omitempty"`
	ConnectPath             *string            `json:"connect_path,omitempty"`
	MessagePath             *string            `json:"message_path,omitempty"`
	DisconnectPath          *string            `json:"disconnect_path,omitempty"`
	CallbackAuthToken       *string            `json:"callback_auth_token,omitempty"`
	AuthToken               *string            `json:"auth_token,omitempty"`
	AuthMode                *string            `json:"auth_mode,omitempty"`
	AuthIssuer              *string            `json:"auth_issuer,omitempty"`
	AuthJWKSURL             *string            `json:"auth_jwks_url,omitempty"`
	AuthAudience            *[]string          `json:"auth_audience,omitempty"`
	AuthAlgorithms          *[]string          `json:"auth_algorithms,omitempty"`
	AuthRequiredClaims      *map[string]string `json:"auth_required_claims,omitempty"`
	AllowedOrigins          *[]string          `json:"allowed_origins,omitempty"`
	MaxConnections          *int               `json:"max_connections,omitempty"`
	MaxMessageBytes         *int64             `json:"max_message_bytes,omitempty"`
	MaxConnectionAgeSeconds *int64             `json:"max_connection_age_seconds,omitempty"`
	Enabled                 *bool              `json:"enabled,omitempty"`
}

// RotateManagedRealtimeAuthRequest starts a bounded overlap window for a new
// static bearer credential. The caller supplies the new credential so it can
// stage clients before the predecessor expires; neither credential is ever
// returned by the API.
type RotateManagedRealtimeAuthRequest struct {
	NewAuthToken       string `json:"new_auth_token"`
	GracePeriodSeconds *int64 `json:"grace_period_seconds,omitempty"`
}

type RotateManagedRealtimeAuthResponse struct {
	EndpointID             string  `json:"endpoint_id"`
	AuthMode               string  `json:"auth_mode"`
	PreviousTokenExpiresAt *string `json:"previous_token_expires_at"`
}

type FinalizeManagedRealtimeAuthResponse struct {
	EndpointID             string  `json:"endpoint_id"`
	AuthMode               string  `json:"auth_mode"`
	PreviousTokenExpiresAt *string `json:"previous_token_expires_at"`
}

// ManagedRealtimeMessageRequest is a binary-safe message sent to one live
// connection or published to an endpoint channel. DataBase64 is decoded
// before the request is forwarded to the realtime owner.
type ManagedRealtimeMessageRequest struct {
	DataBase64 string `json:"data_base64"`
	Binary     bool   `json:"binary,omitempty"`
}

// ManagedRealtimeCloseRequest optionally supplies the WebSocket close reason.
type ManagedRealtimeCloseRequest struct {
	Reason string `json:"reason,omitempty"`
}

// ManagedRealtimePublishResponse reports how many local owner queues accepted
// a published message. A cross-node owner may return a different aggregate
// after the leased registry is enabled.
type ManagedRealtimePublishResponse struct {
	Queued int `json:"queued"`
}

type ManagedRealtimeEndpointResponse struct {
	ID                         string            `json:"id"`
	AppID                      string            `json:"app_id"`
	AccountID                  string            `json:"account_id"`
	CallbackURL                string            `json:"callback_url"`
	ConnectPath                string            `json:"connect_path"`
	MessagePath                string            `json:"message_path"`
	DisconnectPath             string            `json:"disconnect_path"`
	CallbackAuthTokenMasked    string            `json:"callback_auth_token_masked"`
	AuthTokenMasked            string            `json:"auth_token_masked"`
	AuthTokenPreviousExpiresAt *string           `json:"auth_token_previous_expires_at,omitempty"`
	AuthMode                   string            `json:"auth_mode"`
	AuthIssuer                 string            `json:"auth_issuer,omitempty"`
	AuthJWKSURL                string            `json:"auth_jwks_url,omitempty"`
	AuthAudience               []string          `json:"auth_audience,omitempty"`
	AuthAlgorithms             []string          `json:"auth_algorithms,omitempty"`
	AuthRequiredClaims         map[string]string `json:"auth_required_claims,omitempty"`
	AllowedOrigins             []string          `json:"allowed_origins"`
	MaxConnections             int               `json:"max_connections"`
	MaxMessageBytes            int64             `json:"max_message_bytes"`
	MaxConnectionAgeSeconds    int64             `json:"max_connection_age_seconds"`
	Enabled                    bool              `json:"enabled"`
	CreatedAt                  string            `json:"created_at"`
	UpdatedAt                  string            `json:"updated_at"`
}

func ManagedRealtimeEndpointResponseFromRow(id, appID, accountID, callbackURL, connectPath, messagePath, disconnectPath, authMode, authIssuer, authJWKSURL string, authAudience, authAlgorithms []string, authRequiredClaims map[string]string, authTokenPreviousExpiresAt *time.Time, allowedOrigins []string, maxConnections int, maxMessageBytes, maxConnectionAgeSeconds int64, enabled bool, createdAt, updatedAt time.Time) ManagedRealtimeEndpointResponse {
	origins := append([]string{}, allowedOrigins...)
	var previousExpiresAt *string
	if authTokenPreviousExpiresAt != nil && !authTokenPreviousExpiresAt.IsZero() {
		value := authTokenPreviousExpiresAt.UTC().Format(time.RFC3339)
		previousExpiresAt = &value
	}
	return ManagedRealtimeEndpointResponse{
		ID:                         id,
		AppID:                      appID,
		AccountID:                  accountID,
		CallbackURL:                callbackURL,
		ConnectPath:                connectPath,
		MessagePath:                messagePath,
		DisconnectPath:             disconnectPath,
		CallbackAuthTokenMasked:    RealtimeSecretMasked,
		AuthTokenMasked:            RealtimeSecretMasked,
		AuthTokenPreviousExpiresAt: previousExpiresAt,
		AuthMode:                   authMode,
		AuthIssuer:                 authIssuer,
		AuthJWKSURL:                authJWKSURL,
		AuthAudience:               append([]string(nil), authAudience...),
		AuthAlgorithms:             append([]string(nil), authAlgorithms...),
		AuthRequiredClaims:         cloneStringMap(authRequiredClaims),
		AllowedOrigins:             origins,
		MaxConnections:             maxConnections,
		MaxMessageBytes:            maxMessageBytes,
		MaxConnectionAgeSeconds:    maxConnectionAgeSeconds,
		Enabled:                    enabled,
		CreatedAt:                  FormatAlertTime(createdAt),
		UpdatedAt:                  FormatAlertTime(updatedAt),
	}
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

// NormalizeRealtimeAuthMode applies the compatibility default used by old
// clients that only supplied auth_token.
func NormalizeRealtimeAuthMode(mode string, hasStaticToken bool) (string, error) {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		if hasStaticToken {
			return RealtimeAuthModeStaticBearer, nil
		}
		return RealtimeAuthModeNone, nil
	}
	switch mode {
	case RealtimeAuthModeNone, RealtimeAuthModeStaticBearer, RealtimeAuthModeOIDCJWT:
		return mode, nil
	default:
		return "", fmt.Errorf("auth_mode must be one of %q, %q, or %q", RealtimeAuthModeNone, RealtimeAuthModeStaticBearer, RealtimeAuthModeOIDCJWT)
	}
}

// ValidateRealtimeAuth validates public endpoint auth configuration. The
// caller supplies whether a static token exists without exposing plaintext.
func ValidateRealtimeAuth(mode string, hasStaticToken bool, issuer, jwksURL string, audience, algorithms []string, requiredClaims map[string]string) error {
	if mode == RealtimeAuthModeNone {
		if hasStaticToken {
			return fmt.Errorf("auth_mode none cannot include auth_token")
		}
		if issuer != "" || jwksURL != "" || len(audience) > 0 || len(algorithms) > 0 || len(requiredClaims) > 0 {
			return fmt.Errorf("auth settings require auth_mode oidc_jwt")
		}
		return nil
	}
	if mode == RealtimeAuthModeStaticBearer {
		if !hasStaticToken {
			return fmt.Errorf("auth_mode static_bearer requires auth_token")
		}
		if issuer != "" || jwksURL != "" || len(audience) > 0 || len(algorithms) > 0 || len(requiredClaims) > 0 {
			return fmt.Errorf("oidc auth settings cannot be combined with auth_mode static_bearer")
		}
		return nil
	}
	if mode != RealtimeAuthModeOIDCJWT {
		return fmt.Errorf("unsupported auth_mode %q", mode)
	}
	if hasStaticToken {
		return fmt.Errorf("auth_mode oidc_jwt cannot include auth_token")
	}
	if issuer == "" || !strings.HasPrefix(issuer, "https://") || len(issuer) > 2048 {
		return fmt.Errorf("auth_issuer must be an https URL of at most 2048 bytes")
	}
	u, err := url.Parse(jwksURL)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !strings.HasPrefix(jwksURL, "https://") {
		return fmt.Errorf("auth_jwks_url must be an https URL without credentials, query, or fragment")
	}
	lower := strings.ToLower(jwksURL)
	for _, prefix := range []string{"https://localhost", "https://localhost.", "https://127.", "https://10.", "https://192.168.", "https://169.254.", "https://[::", "https://[fc", "https://[fd"} {
		if strings.HasPrefix(lower, prefix) {
			return fmt.Errorf("auth_jwks_url must not point to a private or loopback address")
		}
	}
	if len(audience) > 16 || len(algorithms) == 0 || len(algorithms) > 16 {
		return fmt.Errorf("auth_audience and auth_algorithms are bounded to 16 entries")
	}
	allowed := map[string]struct{}{"RS256": {}, "RS384": {}, "RS512": {}, "ES256": {}, "ES384": {}, "ES512": {}}
	seen := map[string]struct{}{}
	for _, algorithm := range algorithms {
		if _, ok := allowed[algorithm]; !ok {
			return fmt.Errorf("auth_algorithms contains unsupported algorithm %q", algorithm)
		}
		if _, ok := seen[algorithm]; ok {
			return fmt.Errorf("auth_algorithms must be unique")
		}
		seen[algorithm] = struct{}{}
	}
	if len(requiredClaims) > 16 {
		return fmt.Errorf("auth_required_claims are bounded to 16 entries")
	}
	for key, value := range requiredClaims {
		if strings.TrimSpace(key) != key || key == "" || len(key) > 128 || len(value) > 256 {
			return fmt.Errorf("auth_required_claims contains an invalid entry")
		}
	}
	return nil
}

// ValidateRealtimeOrigins validates exact browser origins. Wildcards are
// deliberately not accepted in the first policy version; callers can add
// explicit origins without creating an implicit cross-tenant trust boundary.
func ValidateRealtimeOrigins(origins []string) error {
	if len(origins) > RealtimeMaxAllowedOrigins {
		return fmt.Errorf("allowed_origins: too many origins")
	}
	seen := make(map[string]struct{}, len(origins))
	for _, raw := range origins {
		if len(raw) == 0 || len(raw) > RealtimeOriginMaxBytes || strings.TrimSpace(raw) != raw {
			return fmt.Errorf("allowed_origins: origins must be non-empty and within the size limit")
		}
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("allowed_origins: origins must be absolute http(s) origins without paths or credentials")
		}
		normalized := strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host)
		if _, ok := seen[normalized]; ok {
			return fmt.Errorf("allowed_origins: origins must be unique")
		}
		seen[normalized] = struct{}{}
	}
	return nil
}
