package middleware

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// AgentActivityRecorder records an agent key's write requests
// (ADR-20261107220000). A store without it records none.
type AgentActivityRecorder interface {
	RecordAgentActivity(ctx context.Context, row state.AgentActivity) error
}

// maxAgentActivityPath caps the recorded path; the column holds 512 bytes.
const maxAgentActivityPath = 512

// agentActivityNote carries what the approval gate learned about a request
// to the recorder: the approval it went through on.
type agentActivityNote struct{ approvalID string }

type agentActivityNoteKey struct{}

// withAgentActivityNote returns ctx carrying a new note, stored in *note.
func withAgentActivityNote(ctx context.Context, note **agentActivityNote) context.Context {
	*note = &agentActivityNote{}
	return context.WithValue(ctx, agentActivityNoteKey{}, *note)
}

// noteApprovalUsed tells the recorder the request went through on an
// approval.
func noteApprovalUsed(ctx context.Context, approvalID string) {
	if note, ok := ctx.Value(agentActivityNoteKey{}).(*agentActivityNote); ok {
		note.approvalID = approvalID
	}
}

func agentWrite(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// activityWriter captures the response status for the activity row.
type activityWriter struct {
	http.ResponseWriter
	status int
}

func (a *activityWriter) WriteHeader(status int) {
	if a.status == 0 {
		a.status = status
	}
	a.ResponseWriter.WriteHeader(status)
}

func (a *activityWriter) Write(b []byte) (int, error) {
	if a.status == 0 {
		a.status = http.StatusOK
	}
	return a.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the server's writer.
func (a *activityWriter) Unwrap() http.ResponseWriter { return a.ResponseWriter }

// Flush keeps streaming handlers streaming.
func (a *activityWriter) Flush() { _ = http.NewResponseController(a.ResponseWriter).Flush() }

// recordAgentActivity writes the row off the request path: a detached
// context with a short timeout, so a slow database never delays the answer.
func (m *Middleware) recordAgentActivity(r *http.Request, acct state.Account, key state.APIKey, status int, note *agentActivityNote) {
	rec, ok := m.Authn.(AgentActivityRecorder)
	if !ok {
		return
	}
	if status == 0 {
		// Nothing was written through this writer: the rate-limit probe
		// answers through its own (RequireLimited).
		return
	}
	path := r.URL.Path
	if len(path) > maxAgentActivityPath {
		path = path[:maxAgentActivityPath]
	}
	// Postgres refuses invalid UTF-8, which a cut can leave mid-rune.
	// Postgres text also refuses NUL.
	path = strings.ReplaceAll(strings.ToValidUTF8(path, ""), "\x00", "")
	row := state.AgentActivity{
		KeyID: key.ID, AccountID: acct.ID, OrgID: key.OrgID, Method: r.Method, Path: path,
		Status: status, ApprovalID: note.approvalID, CreatedAt: time.Now().UTC(),
		Reason: api.SanitizeAgentReason(r.Header.Get(api.AgentReasonHeader)),
	}
	//nolint:contextcheck // detached-context is load-bearing, as for TouchKeyLastUsed.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := rec.RecordAgentActivity(ctx, row); err != nil && m.Log != nil {
			m.Log.Warn("agent_activity.record_failed", "key_id", row.KeyID, "error", err.Error())
		}
	}()
}
