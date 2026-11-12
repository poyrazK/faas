package apidserver

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/onebox-faas/faas/pkg/apid/agentundo"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261112080601: undo what an agent changed. The capture and the undo
// both run apid's own handlers through agentUndoDispatch, so they read and
// write exactly as an API request would, with the caller's access.

// agentUndoHandlers are the handlers undo reads and writes through, by
// route pattern. Each pattern must be one apid serves (agent_undo_test).
func (s *server) agentUndoHandlers() map[string]accountHandler {
	return map[string]accountHandler{
		"GET /v1/apps/{slug}":                             s.getApp,
		"PATCH /v1/apps/{slug}":                           s.updateApp,
		"GET /v1/apps/{slug}/scaling":                     s.appScalingHandler(),
		"POST /v1/apps/{slug}/scaling":                    s.appScalingHandler(),
		"DELETE /v1/apps/{slug}/prewarms/{id}":            s.cancelPrewarm,
		"GET /v1/apps/{slug}/config/revisions":            s.listConfigRevisions,
		"GET /v1/apps/{slug}/config/revisions/{revision}": s.getConfigRevision,
		"GET /v1/apps/{slug}/deployments":                 s.listAppDeployments,
		"POST /v1/apps/{slug}/deployments/{id}/cancel":    s.handleCancelDeployment,
		"POST /v1/apps/{slug}/rollback":                   s.rollbackApp,
		"DELETE /v1/apps/{slug}":                          s.deleteApp,
		"DELETE /v1/apps/{slug}/buckets/{bucket}":         s.object.DeleteBucket,
		"DELETE /v1/crons/{id}":                           s.deleteCron,
		"DELETE /v1/domains/{domain}":                     s.deleteDomain,
		"DELETE /v1/postgres/databases/{id}":              s.deleteManagedPostgresDatabase,
		"POST /v1/apps/{slug}/config/rollback":            s.rollbackConfig,
	}
}

// agentUndoEngine is the engine apid captures and undoes with; nil when the
// store keeps no undo records.
func (s *server) agentUndoEngine() *agentundo.Engine {
	store, ok := s.store.(state.AgentUndoStore)
	if !ok {
		return nil
	}
	handlers := s.agentUndoHandlers()
	return &agentundo.Engine{Store: store, Dispatch: func(ctx context.Context, base *http.Request, acct state.Account, method, target string, body []byte) (int, []byte) {
		return agentUndoDispatch(ctx, handlers, base, acct, method, target, body)
	}}
}

// wireAgentUndo hands the engine to the auth middleware, which captures
// each agent write.
func (s *server) wireAgentUndo(engine *agentundo.Engine) {
	if engine == nil {
		return
	}
	s.authMw.UndoCapture = func(r *http.Request, acct state.Account, key state.APIKey) authmw.AgentUndoPending {
		return engine.Begin(r, acct, key)
	}
}

// agentUndoDispatch runs the handler for method and target with base's
// request context (its principal) and answers its status and body.
func agentUndoDispatch(ctx context.Context, handlers map[string]accountHandler, base *http.Request, acct state.Account, method, target string, body []byte) (int, []byte) {
	u, err := url.Parse(target)
	if err != nil {
		return http.StatusBadRequest, nil
	}
	for pattern, h := range handlers {
		values, ok := matchRoute(pattern, method, u.Path)
		if !ok {
			continue
		}
		req, err := http.NewRequestWithContext(ctx, method, u.RequestURI(), bytes.NewReader(body))
		if err != nil {
			return http.StatusBadRequest, nil
		}
		req.Pattern = pattern
		req.RemoteAddr = base.RemoteAddr
		req.Header.Set("User-Agent", base.UserAgent())
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		for k, v := range values {
			req.SetPathValue(k, v)
		}
		rec := &undoRecorder{header: http.Header{}}
		h(rec, req, acct)
		if rec.status == 0 {
			rec.status = http.StatusOK
		}
		return rec.status, rec.body.Bytes()
	}
	return http.StatusNotFound, nil
}

// matchRoute matches "METHOD /a/{x}/b" against method and path.
func matchRoute(pattern, method, path string) (map[string]string, bool) {
	m, p, ok := strings.Cut(pattern, " ")
	if !ok || m != method {
		return nil, false
	}
	want, got := strings.Split(p, "/"), strings.Split(path, "/")
	if len(want) != len(got) {
		return nil, false
	}
	values := map[string]string{}
	for i, seg := range want {
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
			if got[i] == "" {
				return nil, false
			}
			values[seg[1:len(seg)-1]] = got[i]
			continue
		}
		if seg != got[i] {
			return nil, false
		}
	}
	return values, true
}

// undoRecorder keeps a handler's answer.
type undoRecorder struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (u *undoRecorder) Header() http.Header { return u.header }

func (u *undoRecorder) WriteHeader(status int) {
	if u.status == 0 {
		u.status = status
	}
}

func (u *undoRecorder) Write(b []byte) (int, error) {
	if u.status == 0 {
		u.status = http.StatusOK
	}
	if u.body.Len() > 4<<20 {
		return len(b), nil
	}
	return u.body.Write(b)
}

var _ io.Writer = (*undoRecorder)(nil)
