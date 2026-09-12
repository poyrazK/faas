package realtime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// HTTPHooks turns lifecycle events into ordinary POST callbacks. It is kept
// separate from Manager so deployments can replace it with a durable event
// sink (for example, a schedd/pkg/dispatch adapter) without changing socket
// ownership or management APIs.
type HTTPHooks struct {
	Client  *http.Client
	Headers func(Event) http.Header
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
	status, err := h.deliver(ctx, event)
	if err != nil {
		return err
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return fmt.Errorf("realtime: disconnect callback returned HTTP %d", status)
	}
	return nil
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base.String(), strings.NewReader(string(body)))
	if err != nil {
		return 0, fmt.Errorf("realtime: build callback request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Gregale-Realtime-Event-ID", event.ID)
	req.Header.Set("X-Gregale-Realtime-Connection-ID", event.ConnectionID)
	req.Header.Set("X-Gregale-Realtime-Event-Type", string(event.Type))
	if h.Headers != nil {
		for key, values := range h.Headers(event) {
			for _, value := range values {
				req.Header.Add(key, value)
			}
		}
	}
	if event.CallbackAuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+event.CallbackAuthToken)
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
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("realtime: callback request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	return resp.StatusCode, nil
}

// HTTPHandler returns the daemon's combined handler. The public managed path
// is intentionally mounted beside private /internal management routes; the
// latter should be served only on a DAC-protected Unix socket.
func (m *Manager) HTTPHandler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle(ManagedPathPrefix, m)
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
	mux.Handle("/internal/", m.internalHandler())
	return mux
}

type endpointRequest struct {
	ID                string `json:"id"`
	AppID             string `json:"app_id"`
	AccountID         string `json:"account_id"`
	CallbackURL       string `json:"callback_url"`
	ConnectPath       string `json:"connect_path"`
	MessagePath       string `json:"message_path"`
	DisconnectPath    string `json:"disconnect_path"`
	CallbackAuthToken string `json:"callback_auth_token,omitempty"`
	AuthToken         string `json:"auth_token,omitempty"`
}

func (r endpointRequest) endpoint() Endpoint {
	return Endpoint{
		ID:                r.ID,
		AppID:             r.AppID,
		AccountID:         r.AccountID,
		CallbackURL:       r.CallbackURL,
		ConnectPath:       r.ConnectPath,
		MessagePath:       r.MessagePath,
		DisconnectPath:    r.DisconnectPath,
		CallbackAuthToken: r.CallbackAuthToken,
		AuthToken:         r.AuthToken,
	}
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
