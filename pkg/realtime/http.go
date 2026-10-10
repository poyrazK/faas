package realtime

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	defaultCallbackAttempts     = 3
	defaultCallbackRetryBackoff = 100 * time.Millisecond
	maxCallbackRetryBackoff     = 5 * time.Second
)

// HTTPHooks turns lifecycle events into ordinary POST callbacks. It is kept
// separate from Manager so deployments can replace it with a durable event
// sink (for example, a schedd/pkg/dispatch adapter) without changing socket
// ownership or management APIs.
type HTTPHooks struct {
	Client  *http.Client
	Headers func(Event) http.Header
	// DurableQueue persists message and disconnect callbacks before the first
	// HTTP attempt. Connect callbacks remain synchronous because their result
	// decides whether the WebSocket handshake is admitted.
	DurableQueue *CallbackOutbox
	// MaxAttempts is the total number of callback requests, including the
	// initial attempt. Zero uses the production default of three attempts.
	MaxAttempts int
	// RetryBackoff is the base delay for transient callback failures. Delays
	// grow exponentially and are capped at five seconds. Zero uses 100ms.
	RetryBackoff time.Duration
}

// OutboxStats exposes durable callback counters to Manager. Custom Hooks do
// not need to implement this optional observation seam.
func (h HTTPHooks) OutboxStats() CallbackOutboxStats {
	if h.DurableQueue == nil {
		return CallbackOutboxStats{}
	}
	return h.DurableQueue.Stats()
}

// ListCallbackDeadLetters exposes metadata-only dead-letter inspection through
// the daemon's private management socket.
func (h HTTPHooks) ListCallbackDeadLetters(after string, limit int) (CallbackDeadLetterPage, error) {
	if h.DurableQueue == nil {
		return CallbackDeadLetterPage{}, ErrCallbackOutboxUnavailable
	}
	return h.DurableQueue.ListDeadLetters(after, limit)
}

// ReplayCallbackDeadLetter requeues one retained event through the durable
// callback outbox.
func (h HTTPHooks) ReplayCallbackDeadLetter(id string) error {
	if h.DurableQueue == nil {
		return ErrCallbackOutboxUnavailable
	}
	return h.DurableQueue.ReplayDeadLetter(id)
}

// DiscardCallbackDeadLetter deletes one retained event after operator review.
func (h HTTPHooks) DiscardCallbackDeadLetter(id string) error {
	if h.DurableQueue == nil {
		return ErrCallbackOutboxUnavailable
	}
	return h.DurableQueue.DiscardDeadLetter(id)
}

func (h HTTPHooks) enqueueAndClaim(ctx context.Context, event Event) (bool, error) {
	for {
		claimed, err := h.DurableQueue.EnqueueAndClaim(event)
		if err == nil {
			return claimed, nil
		}
		if !errors.Is(err, ErrCallbackOutboxFull) {
			return false, errors.Join(ErrCallbackNotPersisted, ErrCallbackOutboxAdmission, err)
		}
		if ctx.Done() == nil {
			return false, errors.Join(ErrCallbackNotPersisted, ErrCallbackOutboxAdmission, err)
		}

		timer := time.NewTimer(h.DurableQueue.retryInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return false, errors.Join(ErrCallbackNotPersisted, ErrCallbackOutboxAdmission, ErrCallbackOutboxFull, ctx.Err())
		case <-timer.C:
		}
	}
}

func (h HTTPHooks) updateCallbackAuthToken(endpointID, token string) error {
	if h.DurableQueue == nil {
		return nil
	}
	return h.DurableQueue.updateCallbackAuthToken(endpointID, token)
}

func (h HTTPHooks) removeCallbackAuthToken(endpointID string) {
	if h.DurableQueue != nil {
		h.DurableQueue.removeCallbackAuthToken(endpointID)
	}
}

func (h HTTPHooks) Connect(ctx context.Context, event Event) (bool, error) {
	if event.CallbackURL == "" || event.CallbackPath == "" {
		return true, nil
	}
	response, err := h.deliver(ctx, event)
	if err != nil {
		return false, err
	}
	return response.statusCode >= http.StatusOK && response.statusCode < http.StatusMultipleChoices, nil
}

// AuthorizeChannel asks the application before realtimed reads retained
// history or admits a versioned live subscription. The fixed path avoids
// changing endpoint rows during the preview; applications implement this
// callback explicitly and may return any 2xx to grant read permission.
func (h HTTPHooks) AuthorizeChannel(ctx context.Context, event Event) (bool, error) {
	if event.CallbackURL == "" || event.Principal == "" || event.Channel == "" || event.Type != EventAuthorizeChannel {
		return false, nil
	}
	event.CallbackPath = "/realtime/authorize-channel"
	if event.ActivityScope != "" {
		event.Permission = "read_activity"
	} else {
		event.Permission = "read"
	}
	response, err := h.deliver(ctx, event)
	if err != nil {
		return false, err
	}
	return response.statusCode >= http.StatusOK && response.statusCode < http.StatusMultipleChoices, nil
}

func (h HTTPHooks) Message(ctx context.Context, event Event) error {
	if event.CallbackURL == "" || event.CallbackPath == "" {
		return nil
	}
	if h.DurableQueue != nil {
		claimed, err := h.enqueueAndClaim(ctx, event)
		if err != nil {
			return err
		}
		if !claimed {
			return nil
		}
		event, err = h.DurableQueue.claimedEvent(event.ID)
		if err != nil {
			h.DurableQueue.Release(event.ID)
			return err
		}
		err = h.deliverMessage(ctx, event)
		if err != nil {
			if ctx.Err() != nil {
				h.DurableQueue.Release(event.ID)
			} else if failErr := h.DurableQueue.FailWithRetryAfter(event.ID, callbackRetryAfter(err)); failErr != nil {
				return errors.Join(err, fmt.Errorf("persist callback failure: %w", failErr))
			}
			return err
		}
		if err := h.DurableQueue.Ack(event.ID); err != nil {
			h.DurableQueue.Release(event.ID)
			return err
		}
		return nil
	}
	err := h.deliverMessage(ctx, event)
	if err != nil {
		return errors.Join(ErrCallbackNotPersisted, err)
	}
	return nil
}

func (h HTTPHooks) deliverMessage(ctx context.Context, event Event) error {
	response, err := h.deliver(ctx, event)
	if err != nil {
		return err
	}
	if response.statusCode < http.StatusOK || response.statusCode >= http.StatusMultipleChoices {
		return &CallbackHTTPError{StatusCode: response.statusCode, RetryAfter: response.retryAfter}
	}
	return nil
}

func (h HTTPHooks) Disconnect(ctx context.Context, event Event) error {
	if event.CallbackURL == "" || event.CallbackPath == "" {
		return nil
	}
	if h.DurableQueue != nil {
		claimed, err := h.enqueueAndClaim(ctx, event)
		if err != nil {
			return err
		}
		if !claimed {
			return nil
		}
		event, err = h.DurableQueue.claimedEvent(event.ID)
		if err != nil {
			h.DurableQueue.Release(event.ID)
			return err
		}
		err = h.deliverDisconnect(ctx, event)
		if err != nil {
			if ctx.Err() != nil {
				h.DurableQueue.Release(event.ID)
			} else if failErr := h.DurableQueue.FailWithRetryAfter(event.ID, callbackRetryAfter(err)); failErr != nil {
				return errors.Join(err, fmt.Errorf("persist callback failure: %w", failErr))
			}
			return err
		}
		if err := h.DurableQueue.Ack(event.ID); err != nil {
			h.DurableQueue.Release(event.ID)
			return err
		}
		return nil
	}
	err := h.deliverDisconnect(ctx, event)
	if err != nil {
		return errors.Join(ErrCallbackNotPersisted, err)
	}
	return nil
}

func (h HTTPHooks) deliverDisconnect(ctx context.Context, event Event) error {
	response, err := h.deliver(ctx, event)
	if err != nil {
		return err
	}
	if response.statusCode < http.StatusOK || response.statusCode >= http.StatusMultipleChoices {
		return &CallbackHTTPError{StatusCode: response.statusCode, RetryAfter: response.retryAfter}
	}
	return nil
}

// Deliver sends one persisted message or disconnect event without touching a
// durable queue. It is used by CallbackOutbox.Run after a process restart.
func (h HTTPHooks) Deliver(ctx context.Context, event Event) error {
	if event.CallbackURL == "" || event.CallbackPath == "" {
		return nil
	}
	response, err := h.deliver(ctx, event)
	if err != nil {
		return err
	}
	if response.statusCode < http.StatusOK || response.statusCode >= http.StatusMultipleChoices {
		return &CallbackHTTPError{StatusCode: response.statusCode, RetryAfter: response.retryAfter}
	}
	return nil
}

type callbackResponse struct {
	statusCode int
	retryAfter time.Duration
}

// CallbackHTTPError reports a non-2xx callback response to the durable
// replay loop while preserving the status and bounded Retry-After hint.
type CallbackHTTPError struct {
	StatusCode int
	RetryAfter time.Duration
}

func (e *CallbackHTTPError) Error() string {
	if e == nil {
		return "realtime: callback returned a non-2xx response"
	}
	return fmt.Sprintf("realtime: callback returned HTTP %d", e.StatusCode)
}

func callbackRetryAfter(err error) time.Duration {
	var callbackErr *CallbackHTTPError
	if errors.As(err, &callbackErr) && callbackErr != nil {
		return callbackErr.RetryAfter
	}
	return 0
}

func (h HTTPHooks) deliver(ctx context.Context, event Event) (callbackResponse, error) {
	base, err := url.Parse(event.CallbackURL)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return callbackResponse{}, fmt.Errorf("realtime: invalid callback URL: %q", event.CallbackURL)
	}
	path := event.CallbackPath
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	base.Path = strings.TrimRight(base.Path, "/") + path
	body, err := json.Marshal(event)
	if err != nil {
		return callbackResponse{}, fmt.Errorf("realtime: encode callback: %w", err)
	}
	target := base.String()
	headers := make(http.Header)
	headers.Set("Content-Type", "application/json")
	headers.Set("X-Gregale-Realtime-Event-ID", event.ID)
	headers.Set("X-Gregale-Realtime-Connection-ID", event.ConnectionID)
	headers.Set("X-Gregale-Realtime-Event-Type", string(event.Type))
	if h.Headers != nil {
		for key, values := range h.Headers(event) {
			for _, value := range values {
				headers.Add(key, value)
			}
		}
	}
	if event.CallbackAuthToken != "" {
		headers.Set("Authorization", "Bearer "+event.CallbackAuthToken)
	}
	client := h.Client
	if client == nil {
		// Callback URLs are deployment configuration, not an open redirect
		// surface. Do not follow a 3xx response to a different host and leak
		// an event payload; the status is handled by the caller as a failure.
		client = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}}
	}
	attempts := h.MaxAttempts
	if attempts <= 0 {
		attempts = defaultCallbackAttempts
	}
	backoff := h.RetryBackoff
	if backoff <= 0 {
		backoff = defaultCallbackRetryBackoff
	}
	var response callbackResponse
	var retryAfter time.Duration
	for attempt := 1; attempt <= attempts; attempt++ {
		if attempt > 1 {
			if err := waitCallbackRetry(ctx, backoff, attempt-2, retryAfter); err != nil {
				return callbackResponse{}, fmt.Errorf("realtime: callback request: %w", err)
			}
			retryAfter = 0
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
		if err != nil {
			return callbackResponse{}, fmt.Errorf("realtime: build callback request: %w", err)
		}
		req.Header = headers.Clone()
		resp, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil || attempt == attempts {
				return callbackResponse{}, fmt.Errorf("realtime: callback request: %w", err)
			}
			continue
		}
		response = callbackResponse{
			statusCode: resp.StatusCode,
			retryAfter: parseCallbackRetryAfter(resp.StatusCode, resp.Header.Get("Retry-After"), time.Now()),
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		_ = resp.Body.Close()
		if !retryableCallbackStatus(response.statusCode) || attempt == attempts {
			return response, nil
		}
		// Keep long Retry-After hints in the durable schedule instead of
		// retrying inline after the shorter in-process wait cap.
		if response.retryAfter > maxCallbackRetryBackoff {
			return response, nil
		}
		retryAfter = response.retryAfter
	}
	return response, nil
}

func retryableCallbackStatus(status int) bool {
	return status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= 500
}

func parseCallbackRetryAfter(status int, value string, now time.Time) time.Duration {
	if status != http.StatusTooManyRequests && status != http.StatusServiceUnavailable {
		return 0
	}
	maximum := MaxCallbackOutboxMaxRetryInterval
	value = strings.TrimSpace(value)
	var parseErr *strconv.NumError
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds == 0 {
			return 0
		}
		if seconds < 0 {
			return 0
		}
		if seconds >= int64(maximum/time.Second) {
			return maximum
		}
		return time.Duration(seconds) * time.Second
	} else if errors.As(err, &parseErr) && errors.Is(parseErr.Err, strconv.ErrRange) {
		// A delta-seconds value outside int64 still represents a delay longer
		// than the maximum we honor. Clamp it without narrowing an unsigned value.
		if strings.HasPrefix(value, "-") {
			return 0
		}
		return maximum
	}
	date, err := http.ParseTime(value)
	if err != nil {
		return 0
	}
	delay := date.Sub(now)
	if delay <= 0 {
		return 0
	}
	if delay > maximum {
		return maximum
	}
	return delay
}

func waitCallbackRetry(ctx context.Context, base time.Duration, retry int, retryAfter time.Duration) error {
	delay := base
	for i := 0; i < retry && delay < maxCallbackRetryBackoff; i++ {
		if delay > maxCallbackRetryBackoff/2 {
			delay = maxCallbackRetryBackoff
			break
		}
		delay *= 2
	}
	if delay > maxCallbackRetryBackoff {
		delay = maxCallbackRetryBackoff
	}
	if retryAfter > delay {
		delay = retryAfter
	}
	if delay > maxCallbackRetryBackoff {
		delay = maxCallbackRetryBackoff
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// HealthHandler returns the handler for the daemon's health listener. It
// intentionally exposes only liveness/readiness probes; management routes
// must remain behind the DAC-protected Unix socket returned by HTTPHandler.
func (m *Manager) HealthHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		if m == nil || m.closed.Load() {
			http.Error(w, "unhealthy", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok\n")
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if m == nil || m.closed.Load() {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ready\n")
	})
	return mux
}

// HTTPHandler returns the daemon's combined handler. The public managed path
// is intentionally mounted beside private /internal management routes; the
// latter should be served only on a DAC-protected Unix socket.
func (m *Manager) HTTPHandler() http.Handler {
	mux := http.NewServeMux()
	health := m.HealthHandler()
	mux.Handle(ManagedPathPrefix, m)
	mux.Handle("/healthz", health)
	mux.Handle("/readyz", health)
	mux.Handle("/internal/", m.internalHandler())
	return mux
}

type endpointRequest struct {
	ID                         string            `json:"id"`
	AppID                      string            `json:"app_id"`
	AccountID                  string            `json:"account_id"`
	CallbackURL                string            `json:"callback_url"`
	ConnectPath                string            `json:"connect_path"`
	MessagePath                string            `json:"message_path"`
	DisconnectPath             string            `json:"disconnect_path"`
	CallbackAuthToken          string            `json:"callback_auth_token,omitempty"`
	AuthToken                  string            `json:"auth_token,omitempty"`
	AuthTokenPrevious          string            `json:"auth_token_previous,omitempty"`
	AuthTokenPreviousExpiresAt *time.Time        `json:"auth_token_previous_expires_at,omitempty"`
	AuthMode                   AuthMode          `json:"auth_mode,omitempty"`
	AuthIssuer                 string            `json:"auth_issuer,omitempty"`
	AuthJWKSURL                string            `json:"auth_jwks_url,omitempty"`
	AuthAudience               []string          `json:"auth_audience,omitempty"`
	AuthAlgorithms             []string          `json:"auth_algorithms,omitempty"`
	AuthRequiredClaims         map[string]string `json:"auth_required_claims,omitempty"`
	AllowedOrigins             []string          `json:"allowed_origins,omitempty"`
	MaxConnections             int               `json:"max_connections,omitempty"`
	MaxMessageBytes            int64             `json:"max_message_bytes,omitempty"`
	MaxConnectionAgeSeconds    int64             `json:"max_connection_age_seconds,omitempty"`
}

func (r endpointRequest) endpoint() Endpoint {
	return Endpoint{
		ID:                         r.ID,
		AppID:                      r.AppID,
		AccountID:                  r.AccountID,
		CallbackURL:                r.CallbackURL,
		ConnectPath:                r.ConnectPath,
		MessagePath:                r.MessagePath,
		DisconnectPath:             r.DisconnectPath,
		CallbackAuthToken:          r.CallbackAuthToken,
		AuthToken:                  r.AuthToken,
		AuthTokenPrevious:          r.AuthTokenPrevious,
		AuthTokenPreviousExpiresAt: timeOrZero(r.AuthTokenPreviousExpiresAt),
		ClientAuth: AuthPolicy{
			Mode: r.AuthMode, Issuer: r.AuthIssuer, JWKSURL: r.AuthJWKSURL,
			Audience:       append([]string(nil), r.AuthAudience...),
			Algorithms:     append([]string(nil), r.AuthAlgorithms...),
			RequiredClaims: cloneStringMap(r.AuthRequiredClaims),
		},
		AllowedOrigins:   append([]string(nil), r.AllowedOrigins...),
		MaxConnections:   r.MaxConnections,
		MaxMessageBytes:  r.MaxMessageBytes,
		MaxConnectionAge: time.Duration(r.MaxConnectionAgeSeconds) * time.Second,
	}
}

func optionalTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	copy := value
	return &copy
}

func timeOrZero(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return *value
}

type messageRequest struct {
	DataBase64       string `json:"data_base64"`
	Binary           bool   `json:"binary"`
	RetainedSequence int64  `json:"retained_sequence,omitempty"`
}

type closeRequest struct {
	Reason string `json:"reason,omitempty"`
}

func (r messageRequest) message() (Message, error) {
	data, err := base64.StdEncoding.DecodeString(r.DataBase64)
	if err != nil {
		return Message{}, fmt.Errorf("invalid data_base64: %w", err)
	}
	return Message{Data: data, Binary: r.Binary}, nil
}

func (m *Manager) internalHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if m == nil || m.closed.Load() {
			http.Error(w, "realtime service unavailable", http.StatusServiceUnavailable)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/internal/")
		switch {
		case path == "callbacks/dead-letters":
			m.handleCallbackDeadLetters(w, r)
		case strings.HasPrefix(path, "callbacks/dead-letters/"):
			m.handleCallbackDeadLetterRoute(w, r, strings.TrimPrefix(path, "callbacks/dead-letters/"))
		case path == "endpoints" && r.Method == http.MethodGet:
			writeJSON(w, http.StatusOK, m.EndpointIDs())
		case path == "endpoints" && r.Method == http.MethodPost:
			m.handleEndpointCreate(w, r)
		case strings.HasPrefix(path, "endpoints/"):
			m.handleEndpointRoute(w, r, strings.TrimPrefix(path, "endpoints/"))
		case path == "connections" && r.Method == http.MethodGet:
			writeJSON(w, http.StatusOK, m.Snapshot())
		case path == "channel-routes" && r.Method == http.MethodGet:
			writeJSON(w, http.StatusOK, m.ChannelRouteSnapshot())
		case path == "channel-route-revision" && r.Method == http.MethodGet:
			writeJSON(w, http.StatusOK, m.ChannelRouteRevision())
		case path == "stats" && r.Method == http.MethodGet:
			writeJSON(w, http.StatusOK, m.Stats())
		case strings.HasPrefix(path, "connections/"):
			m.handleConnectionRoute(w, r, strings.TrimPrefix(path, "connections/"))
		default:
			http.NotFound(w, r)
		}
	})
}

type callbackDeadLetterManagement interface {
	ListCallbackDeadLetters(after string, limit int) (CallbackDeadLetterPage, error)
	ReplayCallbackDeadLetter(id string) error
	DiscardCallbackDeadLetter(id string) error
}

func (m *Manager) handleCallbackDeadLetters(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	hooks, ok := m.hooks.(callbackDeadLetterManagement)
	if !ok {
		writeCallbackDeadLetterError(w, ErrCallbackOutboxUnavailable)
		return
	}
	after := r.URL.Query().Get("after")
	limit := DefaultCallbackDeadLetterPageSize
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > MaxCallbackDeadLetterPageSize {
			http.Error(w, "invalid limit", http.StatusBadRequest)
			return
		}
		limit = parsed
	}
	page, err := hooks.ListCallbackDeadLetters(after, limit)
	if err != nil {
		writeCallbackDeadLetterError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (m *Manager) handleCallbackDeadLetterRoute(w http.ResponseWriter, r *http.Request, path string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id, action, found := strings.Cut(path, ":")
	if !found || (action != "replay" && action != "discard") {
		http.NotFound(w, r)
		return
	}
	if !validCallbackOutboxID(id) {
		http.Error(w, "invalid callback dead-letter id", http.StatusBadRequest)
		return
	}
	hooks, ok := m.hooks.(callbackDeadLetterManagement)
	if !ok {
		writeCallbackDeadLetterError(w, ErrCallbackOutboxUnavailable)
		return
	}
	if action == "discard" {
		if err := hooks.DiscardCallbackDeadLetter(id); err != nil {
			writeCallbackDeadLetterError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"id": id, "status": "discarded"})
		return
	}
	if err := hooks.ReplayCallbackDeadLetter(id); err != nil {
		writeCallbackDeadLetterError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"id": id, "status": "pending"})
}

func writeCallbackDeadLetterError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrCallbackDeadLetterNotFound):
		http.Error(w, "callback dead letter not found", http.StatusNotFound)
	case errors.Is(err, ErrCallbackOutboxFull), errors.Is(err, ErrCallbackDeadLetterConflict):
		http.Error(w, "callback dead-letter replay conflicts with pending capacity or delivery", http.StatusConflict)
	case errors.Is(err, ErrCallbackOutboxUnavailable):
		http.Error(w, "callback outbox unavailable", http.StatusServiceUnavailable)
	case errors.Is(err, ErrCallbackOutboxItem):
		http.Error(w, "invalid callback dead-letter request", http.StatusBadRequest)
	default:
		http.Error(w, "callback dead-letter operation failed", http.StatusInternalServerError)
	}
}

func (m *Manager) handleEndpointCreate(w http.ResponseWriter, r *http.Request) {
	var request endpointRequest
	decoder := json.NewDecoder(io.LimitReader(r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		http.Error(w, "invalid endpoint JSON", http.StatusBadRequest)
		return
	}
	if err := m.RegisterEndpoint(request.endpoint()); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (m *Manager) handleEndpointRoute(w http.ResponseWriter, r *http.Request, path string) {
	parts := strings.Split(path, "/")
	if len(parts) == 1 && r.Method == http.MethodDelete {
		m.RemoveEndpoint(parts[0])
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if len(parts) == 2 && parts[1] == "principals:send" && r.Method == http.MethodPost {
		var request api.ManagedRealtimePrincipalMessageRequest
		if err := decodeJSONSized(r, &request, 16<<10); err != nil || api.ValidateRealtimePrincipal(request.Principal) != nil {
			http.Error(w, "invalid principal message request", http.StatusBadRequest)
			return
		}
		data, err := base64.StdEncoding.DecodeString(request.DataBase64)
		if err != nil || len(data) > api.RealtimePrincipalMessageMaxBytes {
			http.Error(w, "invalid principal message payload", http.StatusBadRequest)
			return
		}
		var status PrincipalSendStatus
		if request.Delivery == api.ManagedRealtimeDeliveryRetained {
			status, err = m.WakePrincipalInbox(parts[0], request.Principal)
		} else {
			status, err = m.SendToPrincipal(r.Context(), parts[0], request.Principal, request.MessageID, request.RequestReceipt, Message{Data: data, Binary: request.Binary})
		}
		if err != nil {
			writeOperationError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, status)
		return
	}
	if len(parts) == 3 && parts[1] == "channels" && strings.HasSuffix(parts[2], ":publish") && r.Method == http.MethodPost {
		channel := strings.TrimSuffix(parts[2], ":publish")
		var request messageRequest
		if err := decodeJSON(r, &request); err != nil {
			http.Error(w, "invalid message JSON", http.StatusBadRequest)
			return
		}
		message, err := request.message()
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if request.RetainedSequence < 0 {
			http.Error(w, "invalid retained sequence", http.StatusBadRequest)
			return
		}
		var status PublishStatus
		if request.RetainedSequence > 0 {
			status, err = m.PublishRetainedWithStatus(r.Context(), parts[0], channel, message, request.RetainedSequence)
		} else {
			status, err = m.PublishWithStatus(r.Context(), parts[0], channel, message)
		}
		if err != nil {
			writeOperationError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, status)
		return
	}
	if len(parts) == 3 && parts[1] == "channels" && strings.HasSuffix(parts[2], ":ephemeral") && r.Method == http.MethodPost {
		channel := strings.TrimSuffix(parts[2], ":ephemeral")
		var request struct {
			Frame EphemeralFrame `json:"frame"`
		}
		if err := decodeJSON(r, &request); err != nil || !ValidateEphemeralFrame(request.Frame) {
			http.Error(w, "invalid ephemeral event", http.StatusBadRequest)
			return
		}
		if err := m.BroadcastEphemeral(r.Context(), parts[0], channel, request.Frame); err != nil {
			writeOperationError(w, err)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		return
	}
	http.NotFound(w, r)
}

func (m *Manager) handleConnectionRoute(w http.ResponseWriter, r *http.Request, path string) {
	parts := strings.Split(path, "/")
	if len(parts) == 1 && strings.HasSuffix(parts[0], ":close") && r.Method == http.MethodPost {
		id := strings.TrimSuffix(parts[0], ":close")
		reason := "management API"
		if r.Body != nil && r.ContentLength != 0 {
			var request closeRequest
			if err := decodeJSON(r, &request); err != nil {
				http.Error(w, "invalid close JSON", http.StatusBadRequest)
				return
			}
			if request.Reason != "" {
				reason = request.Reason
			}
		}
		if err := m.CloseConnection(id, reason); err != nil {
			writeOperationError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if len(parts) == 1 && strings.HasSuffix(parts[0], ":send") && r.Method == http.MethodPost {
		id := strings.TrimSuffix(parts[0], ":send")
		var request messageRequest
		if err := decodeJSON(r, &request); err != nil {
			http.Error(w, "invalid message JSON", http.StatusBadRequest)
			return
		}
		message, err := request.message()
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := m.Send(r.Context(), id, message); err != nil {
			writeOperationError(w, err)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if len(parts) == 3 && parts[1] == "subscriptions" {
		channel := parts[2]
		var err error
		switch r.Method {
		case http.MethodPut:
			err = m.Subscribe(parts[0], channel)
		case http.MethodDelete:
			hasSubscribers, err := m.UnsubscribeWithRouteState(parts[0], channel)
			if err != nil {
				writeOperationError(w, err)
				return
			}
			if r.Header.Get(unsubscribeRouteStateHeader) == "1" {
				writeJSON(w, http.StatusOK, struct {
					HasSubscribers bool `json:"has_subscribers"`
				}{HasSubscribers: hasSubscribers})
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err != nil {
			writeOperationError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.NotFound(w, r)
}

func decodeJSONSized(r *http.Request, target any, limit int64) error {
	data, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil {
		return err
	}
	if int64(len(data)) > limit {
		return fmt.Errorf("JSON request exceeds size limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("expected a single JSON value")
	}
	return nil
}

func decodeJSON(r *http.Request, target any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 2<<20))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeOperationError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	switch {
	case errors.Is(err, ErrEndpointNotFound):
		status = http.StatusNotFound
	case errors.Is(err, ErrInvalidPrincipal):
		status = http.StatusBadRequest
	case errors.Is(err, ErrInvalidDirectMessageID):
		status = http.StatusBadRequest
	case errors.Is(err, ErrDirectMessageReceiptsUnavailable):
		status = http.StatusServiceUnavailable
	case errors.Is(err, ErrConnectionNotFound):
		status = http.StatusGone
	case errors.Is(err, ErrConnectionClosed):
		status = http.StatusGone
	case errors.Is(err, ErrOutboundQueueFull):
		status = http.StatusTooManyRequests
	case errors.Is(err, ErrTooManyConnections):
		status = http.StatusTooManyRequests
	}
	http.Error(w, err.Error(), status)
}
