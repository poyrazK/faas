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
	"strings"
	"time"
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

func (h HTTPHooks) Connect(ctx context.Context, event Event) (bool, error) {
	if event.CallbackURL == "" || event.CallbackPath == "" {
		return true, nil
	}
	status, err := h.deliver(ctx, event)
	if err != nil {
		return false, err
	}
	return status >= http.StatusOK && status < http.StatusMultipleChoices, nil
}

func (h HTTPHooks) Message(ctx context.Context, event Event) error {
	if event.CallbackURL == "" || event.CallbackPath == "" {
		return nil
	}
	if h.DurableQueue != nil {
		claimed, err := h.DurableQueue.EnqueueAndClaim(event)
		if err != nil {
			return err
		}
		if !claimed {
			return nil
		}
		err = h.deliverMessage(ctx, event)
		if err != nil {
			if ctx.Err() != nil {
				h.DurableQueue.Release(event.ID)
			} else if failErr := h.DurableQueue.Fail(event.ID); failErr != nil {
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
	return h.deliverMessage(ctx, event)
}

func (h HTTPHooks) deliverMessage(ctx context.Context, event Event) error {
	status, err := h.deliver(ctx, event)
	if err != nil {
		return err
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return fmt.Errorf("realtime: message callback returned HTTP %d", status)
	}
	return nil
}

func (h HTTPHooks) Disconnect(ctx context.Context, event Event) error {
	if event.CallbackURL == "" || event.CallbackPath == "" {
		return nil
	}
	if h.DurableQueue != nil {
		claimed, err := h.DurableQueue.EnqueueAndClaim(event)
		if err != nil {
			return err
		}
		if !claimed {
			return nil
		}
		err = h.deliverDisconnect(ctx, event)
		if err != nil {
			if ctx.Err() != nil {
				h.DurableQueue.Release(event.ID)
			} else if failErr := h.DurableQueue.Fail(event.ID); failErr != nil {
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
	return h.deliverDisconnect(ctx, event)
}

func (h HTTPHooks) deliverDisconnect(ctx context.Context, event Event) error {
	status, err := h.deliver(ctx, event)
	if err != nil {
		return err
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return fmt.Errorf("realtime: disconnect callback returned HTTP %d", status)
	}
	return nil
}

// Deliver sends one persisted message or disconnect event without touching a
// durable queue. It is used by CallbackOutbox.Run after a process restart.
func (h HTTPHooks) Deliver(ctx context.Context, event Event) error {
	if event.CallbackURL == "" || event.CallbackPath == "" {
		return nil
	}
	status, err := h.deliver(ctx, event)
	if err != nil {
		return err
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return &CallbackHTTPError{StatusCode: status}
	}
	return nil
}

// CallbackHTTPError reports a non-2xx callback response to the durable
// replay loop while preserving the status for diagnostics.
type CallbackHTTPError struct {
	StatusCode int
}

func (e *CallbackHTTPError) Error() string {
	if e == nil {
		return "realtime: callback returned a non-2xx response"
	}
	return fmt.Sprintf("realtime: callback returned HTTP %d", e.StatusCode)
}

func (h HTTPHooks) deliver(ctx context.Context, event Event) (int, error) {
	base, err := url.Parse(event.CallbackURL)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return 0, fmt.Errorf("realtime: invalid callback URL: %q", event.CallbackURL)
	}
	path := event.CallbackPath
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	base.Path = strings.TrimRight(base.Path, "/") + path
	body, err := json.Marshal(event)
	if err != nil {
		return 0, fmt.Errorf("realtime: encode callback: %w", err)
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
	var status int
	for attempt := 1; attempt <= attempts; attempt++ {
		if attempt > 1 {
			if err := waitCallbackRetry(ctx, backoff, attempt-2); err != nil {
				return 0, fmt.Errorf("realtime: callback request: %w", err)
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
		if err != nil {
			return 0, fmt.Errorf("realtime: build callback request: %w", err)
		}
		req.Header = headers.Clone()
		resp, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil || attempt == attempts {
				return 0, fmt.Errorf("realtime: callback request: %w", err)
			}
			continue
		}
		status = resp.StatusCode
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		_ = resp.Body.Close()
		if !retryableCallbackStatus(status) || attempt == attempts {
			return status, nil
		}
	}
	return status, nil
}

func retryableCallbackStatus(status int) bool {
	return status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= 500
}

func waitCallbackRetry(ctx context.Context, base time.Duration, retry int) error {
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
	DataBase64 string `json:"data_base64"`
	Binary     bool   `json:"binary"`
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
		case path == "endpoints" && r.Method == http.MethodPost:
			m.handleEndpointCreate(w, r)
		case strings.HasPrefix(path, "endpoints/"):
			m.handleEndpointRoute(w, r, strings.TrimPrefix(path, "endpoints/"))
		case path == "connections" && r.Method == http.MethodGet:
			writeJSON(w, http.StatusOK, m.Snapshot())
		case path == "stats" && r.Method == http.MethodGet:
			writeJSON(w, http.StatusOK, m.Stats())
		case strings.HasPrefix(path, "connections/"):
			m.handleConnectionRoute(w, r, strings.TrimPrefix(path, "connections/"))
		default:
			http.NotFound(w, r)
		}
	})
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
		queued, err := m.Publish(r.Context(), parts[0], channel, message)
		if err != nil {
			writeOperationError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]int{"queued": queued})
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
			err = m.Unsubscribe(parts[0], channel)
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
