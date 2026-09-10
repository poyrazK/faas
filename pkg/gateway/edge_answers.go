package gateway

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/wire"
)

// EdgeAnswerKind is the bounded label vocabulary for gateway answers that do
// not reach an app instance.
type EdgeAnswerKind string

const (
	EdgeAnswerFavicon EdgeAnswerKind = "favicon"
	EdgeAnswerRobots  EdgeAnswerKind = "robots"
	EdgeAnswerHead    EdgeAnswerKind = "head"

	// EdgeFaviconMaxBytes keeps an uploaded icon from becoming an unbounded
	// response or gateway-memory allocation.
	EdgeFaviconMaxBytes = 32 * 1024

	edgeHeadHeaderCacheCap = 10000
	edgeHeadHeaderMaxBytes = 64 * 1024

	defaultRobotsTxt = "User-agent: *\nAllow: /\n"
)

const defaultHealthPath = "/healthz"

type healthSnapshot struct {
	ready  bool
	reason string
}

func normalizeHealthPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return defaultHealthPath
	}
	if path[0] != '/' {
		return "/" + path
	}
	return path
}

func (h *Handler) markHealthReady(appID string) {
	if h == nil || appID == "" {
		return
	}
	h.healthState.Store(appID, healthSnapshot{ready: true, reason: "live"})
}

func (h *Handler) markHealthFailure(appID string, err error) {
	if h == nil || appID == "" {
		return
	}
	reason := "wake_failed"
	if prob := api.AsProblem(err); prob != nil && prob.Code != "" {
		reason = prob.Code
	} else if errors.Is(err, context.DeadlineExceeded) {
		reason = "wake_timeout"
	} else if errors.Is(err, context.Canceled) {
		reason = "wake_canceled"
	}
	h.healthState.Store(appID, healthSnapshot{reason: reason})
}

func (h *Handler) healthSnapshotFor(app App) healthSnapshot {
	if h != nil && h.backend != nil && h.backend.HealthyCount(app.ID) > 0 {
		return healthSnapshot{ready: true, reason: "live"}
	}
	if h != nil {
		if value, ok := h.healthState.Load(app.ID); ok {
			if snapshot, ok := value.(healthSnapshot); ok {
				return snapshot
			}
		}
	}
	return healthSnapshot{reason: "unknown"}
}

// edgeHeadHeaderCache is intentionally small and process-local. Header values
// are copied before the origin writer is reused, and only response headers
// that are safe to replay on a synthetic HEAD are retained.
type edgeHeadHeaderCache struct {
	mu      sync.RWMutex
	entries map[string]http.Header
}

func newEdgeHeadHeaderCache() *edgeHeadHeaderCache {
	return &edgeHeadHeaderCache{entries: make(map[string]http.Header)}
}

func (c *edgeHeadHeaderCache) put(appID string, header http.Header) {
	if c == nil || appID == "" || len(header) == 0 {
		return
	}
	copyHeader := make(http.Header)
	totalBytes := 0
	for key, values := range header {
		canonical := http.CanonicalHeaderKey(key)
		if !edgeHeadHeaderAllowed(canonical) || len(values) == 0 {
			continue
		}
		for _, value := range values {
			// Header values are copied as-is, but cap the aggregate replay
			// size so a guest cannot turn the cache into a memory sink.
			if len(value) > 8*1024 || totalBytes+len(value) > edgeHeadHeaderMaxBytes {
				continue
			}
			copyHeader[canonical] = append(copyHeader[canonical], value)
			totalBytes += len(value)
		}
	}
	if len(copyHeader) == 0 {
		return
	}
	c.mu.Lock()
	if len(c.entries) >= edgeHeadHeaderCacheCap {
		for evictID := range c.entries {
			delete(c.entries, evictID)
			break
		}
	}
	c.entries[appID] = copyHeader
	c.mu.Unlock()
}

func (c *edgeHeadHeaderCache) get(appID string) (http.Header, bool) {
	if c == nil || appID == "" {
		return nil, false
	}
	c.mu.RLock()
	header, ok := c.entries[appID]
	if ok {
		header = header.Clone()
	}
	c.mu.RUnlock()
	return header, ok
}

func edgeHeadHeaderAllowed(key string) bool {
	if isPerRequestPlatformHeader(key) {
		return false
	}
	switch strings.ToLower(key) {
	case "connection", "keep-alive", "proxy-authenticate", "proxy-authorization",
		"te", "trailer", "transfer-encoding", "upgrade", "set-cookie",
		"authorization", "www-authenticate",
		"strict-transport-security", "x-frame-options", "x-content-type-options",
		"referrer-policy", "permissions-policy":
		return false
	default:
		return true
	}
}

// isPerRequestPlatformHeader identifies response metadata that belongs to the
// current request. Replaying one of these values from a previous origin request
// makes correlation and wake diagnostics ambiguous. The handler regenerates
// the relevant values for every request, including synthetic HEAD and response
// cache hits.
func isPerRequestPlatformHeader(key string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(key)), "x-faas-")
}

// serveEdgeAnswer handles the three M1 paths after host resolution and before
// any auth, limiter, or wake work. It returns true when the request is fully
// answered at the gateway. Static answers intentionally use the normal
// request observation funnel so they remain visible as requests while never
// creating an instance transition or usage row.
func (h *Handler) serveEdgeAnswer(w http.ResponseWriter, r *http.Request, app App) bool {
	if h == nil || r == nil {
		return false
	}
	// The generic handler starts with "hot" so early auth and policy exits
	// expose a bounded wake value. Edge answers do not consult a VM at all.
	w.Header().Del(wire.WakeHeader)

	if r.URL.Path == normalizeHealthPath(app.HealthPath) && !app.HealthPathWakes {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			return false
		}
		snapshot := h.healthSnapshotFor(app)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Faas-Health-Source", "edge")
		if snapshot.ready {
			w.Header().Set("Content-Type", "application/json")
			body := `{"status":"ok","source":"edge"}`
			w.Header().Set("Content-Length", itoa(uint64(len(body))))
			w.WriteHeader(http.StatusOK)
			if r.Method == http.MethodGet {
				_, _ = w.Write([]byte(body))
			}
			if h.metrics != nil {
				h.metrics.ObserveHealthEdgeAnswered(app.ID, "healthy")
			}
			return true
		}
		reason := snapshot.reason
		if reason == "" {
			reason = "unknown"
		}
		w.Header().Set("X-Faas-Health-Reason", reason)
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable,
				api.CodeAppHealthUnavailable, "App health is unavailable", "last wake status: "+reason))
		}
		if h.metrics != nil {
			h.metrics.ObserveHealthEdgeAnswered(app.ID, "unhealthy")
		}
		return true
	}

	switch r.URL.Path {
	case "/favicon.ico":
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			return false
		}
		favicon := app.Favicon
		if len(favicon) == 0 || len(favicon) > EdgeFaviconMaxBytes {
			w.WriteHeader(http.StatusNoContent)
		} else {
			w.Header().Set("Content-Type", "image/x-icon")
			w.Header().Set("Cache-Control", "public, max-age=3600")
			w.Header().Set("Content-Length", itoa(uint64(len(favicon))))
			w.WriteHeader(http.StatusOK)
			if r.Method == http.MethodGet {
				_, _ = w.Write(favicon)
			}
		}
		h.observeEdgeAnswer(w, r, app, EdgeAnswerFavicon)
		return true

	case "/robots.txt":
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			return false
		}
		body := app.RobotsTxt
		if body == "" {
			body = defaultRobotsTxt
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=300")
		w.Header().Set("Content-Length", itoa(uint64(len(body))))
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(body))
		}
		h.observeEdgeAnswer(w, r, app, EdgeAnswerRobots)
		return true

	case "/":
		if r.Method != http.MethodHead || app.HeadWakes || h.headWakes {
			return false
		}
		if h.backend.HealthyCount(app.ID) != 0 {
			return false
		}
		if headers, ok := h.headHeaders.get(app.ID); ok {
			for key, values := range headers {
				for _, value := range values {
					w.Header().Add(key, value)
				}
			}
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusNoContent)
		}
		h.observeEdgeAnswer(w, r, app, EdgeAnswerHead)
		return true
	}
	return false
}

func (h *Handler) observeEdgeAnswer(w http.ResponseWriter, r *http.Request, app App, kind EdgeAnswerKind) {
	if h.metrics != nil {
		h.metrics.ObserveEdgeAnswered(string(kind))
	}
	status := http.StatusOK
	if rec, ok := w.(*statusRecorder); ok && rec != nil {
		status = rec.status
	}
	h.observe(r, status, app.ID, string(app.Plan), false, Target{})
}

// cacheHeadResponse stores the response headers from a successful live
// response. It is called only after the origin leg returns, so a parked HEAD
// can reuse the last known shape without replaying a body or waking a VM.
func (h *Handler) cacheHeadResponse(appID string, rec *statusRecorder) {
	if h == nil || h.headHeaders == nil || rec == nil || rec.status < 200 || rec.status >= 400 {
		return
	}
	h.headHeaders.put(appID, rec.Header())
}

// WithHeadWakes enables the legacy process-wide HEAD wake behaviour. Per-app
// App.HeadWakes remains the preferred setting; this method is useful for a
// compatibility rollout and tests.
func (h *Handler) WithHeadWakes(enabled bool) *Handler {
	if h != nil {
		h.headWakes = enabled
	}
	return h
}
