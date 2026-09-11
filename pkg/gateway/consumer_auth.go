package gateway

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
)

// ConsumerAuthStore is the small state.Store slice needed by the public edge.
// The gateway owns the request policy; cmd/gatewayd-internal adapts the
// concrete state.Store so this package does not depend on sqlc/state types.
type ConsumerAuthStore interface {
	ConsumerKeyByAppAndPrefix(ctx context.Context, accountID, appID, prefix string) (ConsumerAuthKey, error)
	GetAPIConsumerByID(ctx context.Context, accountID, consumerID string) (ConsumerAuthConsumer, error)
	TouchConsumerKeyLastUsed(ctx context.Context, keyID string) error
}

// ConsumerAuthKey is the read-only key shape required by the gateway. Hash is
// the SHA-256 of the complete plaintext credential; the plaintext never enters
// this type or the request context.
type ConsumerAuthKey struct {
	ID         string
	AccountID  string
	AppID      string
	ConsumerID string
	Prefix     string
	Hash       []byte
	Scopes     []string
	ExpiresAt  *time.Time
	RevokedAt  *time.Time
}

// Active reports whether a key is eligible for authentication at now. The
// lookup deliberately returns revoked/expired rows so the caller can retain a
// single, non-enumerating inactive-key response.
func (k ConsumerAuthKey) Active(now time.Time) bool {
	if k.RevokedAt != nil {
		return false
	}
	return k.ExpiresAt == nil || k.ExpiresAt.After(now)
}

// ConsumerAuthConsumer is the stable customer identity associated with a key.
type ConsumerAuthConsumer struct {
	ID        string
	AccountID string
	AppID     string
	Status    string
	RevokedAt *time.Time
}

func (c ConsumerAuthConsumer) Active() bool {
	return c.Status == "active" && c.RevokedAt == nil
}

// ErrConsumerAuthNotFound is the adapter-level equivalent of state.ErrNotFound.
// It intentionally collapses unknown app/account/key combinations to the same
// customer-facing 401 so a foreign prefix cannot be used as an existence oracle.
var ErrConsumerAuthNotFound = errors.New("gateway: consumer authentication record not found")

// consumerKeyToucher is a process-local 60-second debouncer for last_used_at.
// Touching is observational only and never participates in the authorization
// decision, so a failed asynchronous write must not fail an otherwise valid
// request.
type consumerKeyToucher struct {
	mu   sync.Mutex
	last map[string]time.Time
}

func newConsumerKeyToucher() *consumerKeyToucher {
	return &consumerKeyToucher{last: make(map[string]time.Time)}
}

func (t *consumerKeyToucher) touch(store ConsumerAuthStore, keyID string) {
	if t == nil || store == nil || keyID == "" {
		return
	}
	now := time.Now()
	t.mu.Lock()
	last, seen := t.last[keyID]
	if seen && now.Sub(last) < time.Minute {
		t.mu.Unlock()
		return
	}
	t.last[keyID] = now
	t.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = store.TouchConsumerKeyLastUsed(ctx, keyID)
	}()
}

// enforceConsumerAuth applies ADR-120's app-level consumer-key matrix. It
// returns true when the request may continue and false after writing the
// customer-facing problem response. A present-but-malformed credential is
// always rejected, even in optional mode; only an absent credential is
// anonymous pass-through in that mode.
func (h *Handler) enforceConsumerAuth(w http.ResponseWriter, r *http.Request, rec *statusRecorder, app App) bool {
	mode := app.ConsumerAuthMode
	if mode == "" {
		mode = api.ConsumerAuthModeOptional
	}
	if mode != api.ConsumerAuthModeOptional && mode != api.ConsumerAuthModeRequired {
		if h.log != nil {
			h.log.Warn("gateway: unknown consumer auth mode; failing closed", "app_id", app.ID, "mode", mode)
		}
		return h.consumerAuthUnavailable(w, r, rec, app)
	}

	raw := r.Header.Get("Authorization")
	if strings.TrimSpace(raw) == "" {
		if mode == api.ConsumerAuthModeOptional {
			return true
		}
		api.WriteProblem(w, api.ErrConsumerKeyRequired())
		rec.status = http.StatusUnauthorized
		h.observe(r, rec.status, app.ID, string(app.Plan), false, Target{})
		return false
	}

	token := bearerTokenFromHeader(raw)
	if token == "" || !api.ValidConsumerKeyFormat(token) {
		api.WriteProblem(w, api.ErrConsumerKeyInvalid())
		rec.status = http.StatusUnauthorized
		h.observe(r, rec.status, app.ID, string(app.Plan), false, Target{})
		return false
	}
	if h.consumerAuthStore == nil || app.AccountID == "" || app.ID == "" {
		return h.consumerAuthUnavailable(w, r, rec, app)
	}

	prefix := consumerKeyPrefixFromPlaintext(token)
	key, err := h.consumerAuthStore.ConsumerKeyByAppAndPrefix(r.Context(), app.AccountID, app.ID, prefix)
	if err != nil {
		if !errors.Is(err, ErrConsumerAuthNotFound) {
			if h.log != nil {
				h.log.Warn("gateway: consumer key lookup failed", "app_id", app.ID, "err", err)
			}
			return h.consumerAuthUnavailable(w, r, rec, app)
		}
		api.WriteProblem(w, api.ErrConsumerKeyInvalid())
		rec.status = http.StatusUnauthorized
		h.observe(r, rec.status, app.ID, string(app.Plan), false, Target{})
		return false
	}

	// Active is checked before the hash comparison by contract. This preserves
	// the passive store lookup and gives revoked/expired rows one stable outcome.
	if !key.Active(time.Now()) {
		api.WriteProblem(w, api.ErrConsumerKeyInactive())
		rec.status = http.StatusUnauthorized
		h.observe(r, rec.status, app.ID, string(app.Plan), false, Target{})
		return false
	}
	expectedHash := api.HashConsumerKey(token)
	if subtle.ConstantTimeCompare(key.Hash, expectedHash) != 1 || key.AccountID != app.AccountID || key.AppID != app.ID || key.Prefix != prefix || key.ConsumerID == "" {
		api.WriteProblem(w, api.ErrConsumerKeyInvalid())
		rec.status = http.StatusUnauthorized
		h.observe(r, rec.status, app.ID, string(app.Plan), false, Target{})
		return false
	}

	consumer, err := h.consumerAuthStore.GetAPIConsumerByID(r.Context(), app.AccountID, key.ConsumerID)
	if err != nil {
		if !errors.Is(err, ErrConsumerAuthNotFound) {
			if h.log != nil {
				h.log.Warn("gateway: consumer identity lookup failed", "app_id", app.ID, "err", err)
			}
			return h.consumerAuthUnavailable(w, r, rec, app)
		}
		api.WriteProblem(w, api.ErrConsumerKeyInvalid())
		rec.status = http.StatusUnauthorized
		h.observe(r, rec.status, app.ID, string(app.Plan), false, Target{})
		return false
	}
	if !consumer.Active() {
		api.WriteProblem(w, api.ErrConsumerKeyInactive())
		rec.status = http.StatusUnauthorized
		h.observe(r, rec.status, app.ID, string(app.Plan), false, Target{})
		return false
	}
	if consumer.ID != key.ConsumerID || consumer.AccountID != app.AccountID || consumer.AppID != app.ID {
		api.WriteProblem(w, api.ErrConsumerKeyInvalid())
		rec.status = http.StatusUnauthorized
		h.observe(r, rec.status, app.ID, string(app.Plan), false, Target{})
		return false
	}
	if !consumerScopeAllows(key.Scopes, r.Method) {
		api.WriteProblem(w, api.ErrConsumerScopeMissing(r.Method))
		rec.status = http.StatusForbidden
		h.observe(r, rec.status, app.ID, string(app.Plan), false, Target{})
		return false
	}

	authenticated := authenticatedFrom(r.Context())
	authenticated.ConsumerID = consumer.ID
	authenticated.ConsumerKeyID = key.ID
	ctx := authmw.WithConsumer(r.Context(), authmw.ConsumerIdentity{
		ID:     consumer.ID,
		AppID:  app.ID,
		KeyID:  key.ID,
		Scopes: key.Scopes,
	})
	ctx = withAuthenticated(ctx, authenticated)
	*r = *r.WithContext(ctx)
	if h.consumerKeyTouches == nil {
		h.consumerKeyTouches = newConsumerKeyToucher()
	}
	h.consumerKeyTouches.touch(h.consumerAuthStore, key.ID) //nolint:contextcheck // last_used_at is best-effort and intentionally outlives the request context.
	return true
}

func (h *Handler) consumerAuthUnavailable(w http.ResponseWriter, r *http.Request, rec *statusRecorder, app App) bool {
	api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, api.CodeCapacity,
		"Consumer authentication unavailable", "this app's consumer authentication is temporarily unavailable"))
	rec.status = http.StatusServiceUnavailable
	h.observe(r, rec.status, app.ID, string(app.Plan), false, Target{})
	return false
}

func consumerKeyPrefixFromPlaintext(token string) string {
	body := strings.TrimPrefix(token, api.ConsumerKeyPrefix)
	if idx := strings.IndexByte(body, '_'); idx > 0 {
		return body[:idx]
	}
	return ""
}

func consumerScopeAllows(scopes []string, method string) bool {
	readOnly := method == http.MethodGet
	for _, scope := range scopes {
		switch scope {
		case "admin":
			return true
		case "read":
			if readOnly {
				return true
			}
		case "write":
			// write is the all-method app scope. The read scope is the
			// GET-only restriction.
			return true
		}
	}
	return false
}
