package devbridge

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Activity is an informational window observed by the single apid owner.
// Unknown means no live connection has been observed since startup/eviction.
type Activity struct {
	ConnectionState string          `json:"connection_state"`
	ConnectedAt     *time.Time      `json:"connected_at,omitempty"`
	DisconnectedAt  *time.Time      `json:"disconnected_at,omitempty"`
	Requests        []RequestRecord `json:"requests"`
}

type observedSession struct {
	activity   Activity
	inspector  *Inspector
	generation uint64
	expiresAt  time.Time
	updatedAt  time.Time
}

// Observer holds no credentials or payloads. Replacing a connection fences
// old Close callbacks, and bounded admission prevents account/session churn
// from creating an unbounded telemetry cache.
type Observer struct {
	mu                      sync.Mutex
	sessions                map[string]*observedSession
	capacity, records, path int
}

func NewObserver(capacity, records, pathBytes int) *Observer {
	if capacity < 1 {
		capacity = 1
	}
	return &Observer{sessions: make(map[string]*observedSession), capacity: capacity, records: records, path: pathBytes}
}

func (o *Observer) entry(session Session) *observedSession {
	if existing := o.sessions[session.ID]; existing != nil {
		existing.updatedAt = time.Now()
		return existing
	}
	now := time.Now()
	for id, item := range o.sessions {
		if !now.Before(item.expiresAt) {
			delete(o.sessions, id)
		}
	}
	if len(o.sessions) >= o.capacity {
		var oldest string
		for id, item := range o.sessions {
			if oldest == "" || item.updatedAt.Before(o.sessions[oldest].updatedAt) {
				oldest = id
			}
		}
		delete(o.sessions, oldest)
	}
	item := &observedSession{activity: Activity{ConnectionState: "unknown"}, inspector: NewInspector(o.records, o.path), expiresAt: session.ExpiresAt, updatedAt: now}
	o.sessions[session.ID] = item
	return item
}

func (o *Observer) Snapshot(session Session) Activity {
	o.mu.Lock()
	defer o.mu.Unlock()
	activity := Activity{ConnectionState: "unknown", Requests: []RequestRecord{}}
	if item := o.sessions[session.ID]; item != nil {
		activity = item.activity
		activity.Requests = item.inspector.Snapshot()
		for i := range activity.Requests {
			if activity.Requests[i].Error == "local_transport_failed" {
				activity.Requests[i].Error = "relay_transport_failed"
			}
		}
	}
	if session.RevokedAt != nil {
		activity.ConnectionState = "revoked"
	} else if !time.Now().Before(session.ExpiresAt) {
		activity.ConnectionState = "expired"
	}
	return activity
}

func (o *Observer) ConnectionTransport(session Session, base http.RoundTripper) http.RoundTripper {
	return roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		response, err := base.RoundTrip(r)
		if err != nil || response.StatusCode != http.StatusSwitchingProtocols {
			return response, err
		}
		body, ok := response.Body.(io.ReadWriteCloser)
		if !ok {
			return response, nil
		}
		o.mu.Lock()
		item := o.entry(session)
		item.generation++
		generation := item.generation
		now := time.Now().UTC()
		item.activity.ConnectionState, item.activity.ConnectedAt, item.activity.DisconnectedAt = "connected", &now, nil
		o.mu.Unlock()
		response.Body = &observedConnection{ReadWriteCloser: body, close: func() {
			o.mu.Lock()
			defer o.mu.Unlock()
			if o.sessions[session.ID] != item || item.generation != generation {
				return
			}
			now := time.Now().UTC()
			item.activity.ConnectionState, item.activity.DisconnectedAt = "disconnected", &now
		}}
		return response, nil
	})
}

func (o *Observer) RequestTransport(session Session, base http.RoundTripper) http.RoundTripper {
	o.mu.Lock()
	inspector := o.entry(session).inspector
	o.mu.Unlock()
	return inspector.transport(base, func(r *http.Request) *http.Request {
		view := r.Clone(r.Context())
		prefix := "/v1/dev/bridges/" + session.ID + "/traffic"
		view.URL.Path = strings.TrimPrefix(view.URL.Path, prefix)
		view.URL.RawPath = strings.TrimPrefix(view.URL.RawPath, prefix)
		if view.URL.Path == "" {
			view.URL.Path = "/"
		}
		return view
	})
}

type observedConnection struct {
	io.ReadWriteCloser
	once  sync.Once
	close func()
}

func (c *observedConnection) Close() error {
	err := c.ReadWriteCloser.Close()
	c.once.Do(c.close)
	return err
}
