package state

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var ErrRealtimeWorkerCredentialDenied = errors.New("state: worker credential revoked, expired or outside permission scope")

func realtimeWorkerPermissionAllowed(permission string) bool {
	switch permission {
	case "release", "heartbeat", "status", "claim", "renew", "complete", "retry", "dead_letters", "replay", "resync", "delete", "pause", "resume", "drain":
		return true
	default:
		return false
	}
}

type RealtimeWorkerCredential struct {
	ID            string     `json:"id"`
	GroupID       string     `json:"group_id"`
	Name          string     `json:"name"`
	Permissions   []string   `json:"permissions"`
	Generation    string     `json:"generation"`
	IssuedAt      time.Time  `json:"issued_at"`
	ExpiresAt     time.Time  `json:"expires_at"`
	RevokedAt     *time.Time `json:"revoked_at"`
	LastUsedAt    *time.Time `json:"last_used_at"`
	LastOperation *string    `json:"last_operation"`
	UseCount      string     `json:"use_count"`
}
type RealtimeWorkerCredentialRequest struct {
	Name        string   `json:"name"`
	Permissions []string `json:"permissions"`
	TTLSeconds  int      `json:"ttl_seconds,omitempty"`
}
type RealtimeWorkerCredentialIssued struct {
	Credential string                   `json:"credential"`
	Info       RealtimeWorkerCredential `json:"info"`
}
type RealtimeWorkerGrant struct {
	Info                                         RealtimeWorkerCredential
	AccountID, AppID, EndpointID, Channel, Group string
	hash                                         []byte
}
type realtimeWorkerContext struct {
	grant     RealtimeWorkerGrant
	operation string
}
type realtimeWorkerContextKey struct{}

// Grant is produced only by authenticating the plaintext credential in the store.
func WithRealtimeWorkerGrant(ctx context.Context, grant RealtimeWorkerGrant, operation string) context.Context {
	return context.WithValue(ctx, realtimeWorkerContextKey{}, realtimeWorkerContext{grant, operation})
}
func realtimeWorkerFromContext(ctx context.Context) (realtimeWorkerContext, bool) {
	value, ok := ctx.Value(realtimeWorkerContextKey{}).(realtimeWorkerContext)
	return value, ok
}

type RealtimeWorkerCredentialStore interface {
	IssueRealtimeWorkerCredential(context.Context, string, string, string, RealtimeWorkerCredentialRequest) (RealtimeWorkerCredentialIssued, error)
	ListRealtimeWorkerCredentials(context.Context, string, string, string) ([]RealtimeWorkerCredential, error)
	RotateRealtimeWorkerCredential(context.Context, string, string, string, string, int) (RealtimeWorkerCredentialIssued, error)
	RevokeRealtimeWorkerCredential(context.Context, string, string, string, string) error
	AuthenticateRealtimeWorkerCredential(context.Context, string, string) (RealtimeWorkerGrant, error)
}

func realtimeWorkerTokenHash(token string) ([]byte, error) {
	if len(token) != len("grw_")+base64.RawURLEncoding.EncodedLen(32) || !strings.HasPrefix(token, "grw_") {
		return nil, ErrRealtimeWorkerCredentialDenied
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(token, "grw_"))
	if err != nil || len(raw) != 32 {
		return nil, ErrRealtimeWorkerCredentialDenied
	}
	hash := sha256.Sum256([]byte(token))
	return hash[:], nil
}
func newRealtimeWorkerToken() (string, []byte, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, err
	}
	token := "grw_" + base64.RawURLEncoding.EncodeToString(raw)
	hash, _ := realtimeWorkerTokenHash(token)
	return token, hash, nil
}
func validateRealtimeWorkerCredentialRequest(req RealtimeWorkerCredentialRequest) error {
	if !validRealtimeWorkerName(req.Name, api.RealtimeWorkerCredentialNameMaxBytes) || len(req.Permissions) < 1 || len(req.Permissions) > api.RealtimeWorkerCredentialPermissionsMax || req.TTLSeconds < 1 || req.TTLSeconds > api.RealtimeWorkerCredentialTTLMaxSeconds {
		return ErrRealtimeConsumerGroupInvalid
	}
	seen := map[string]bool{}
	for _, permission := range req.Permissions {
		if !realtimeWorkerPermissionAllowed(permission) || seen[permission] {
			return ErrRealtimeConsumerGroupInvalid
		}
		seen[permission] = true
	}
	return nil
}

func requireRealtimeWorkerOperation(ctx context.Context, operation string) error {
	worker, ok := realtimeWorkerFromContext(ctx)
	if !ok {
		return nil
	}
	if worker.operation != operation || !realtimeWorkerPermissionAllowed(operation) {
		return ErrRealtimeWorkerCredentialDenied
	}
	return nil
}
