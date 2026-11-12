package middleware

import (
	"bufio"
	"context"
	"net"
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
type agentActivityNote struct {
	// id is the activity row's id, which the undo record names.
	id         string
	approvalID string
	// binding is taken before the handler reads the body on routes that
	// charge the agent budget (ADR-20261111114059).
	binding *state.AgentApprovalBinding
}

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
	// onHijack records the row when a handler takes the connection over: a
	// WebSocket handler returns only when the connection ends.
	onHijack func()
	// keepBody keeps the first maxUndoResponseBody bytes of the answer for
	// the undo capture (ADR-20261112080601).
	keepBody bool
	body     []byte
}

// maxUndoResponseBody bounds the answer kept for an undo capture: it needs
// ids and revisions, which come first and small.
const maxUndoResponseBody = 64 << 10

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
	if a.keepBody && len(a.body) < maxUndoResponseBody {
		a.body = append(a.body, b[:min(len(b), maxUndoResponseBody-len(a.body))]...)
	}
	return a.ResponseWriter.Write(b)
}

// AgentUndoPending is an undo capture begun before an agent's write
// (ADR-20261112080601).
type AgentUndoPending interface {
	// Finish records how to reverse the write, once it answered status with
	// body; activityID names the activity row.
	Finish(ctx context.Context, status int, body []byte, activityID string) error
}

// finishAgentUndo records the undo of a write that answered. It reads the
// resource as the write left it, so it runs before the next request of the
// agent can change it; a failure only loses the undo, never the answer.
func (m *Middleware) finishAgentUndo(r *http.Request, undo AgentUndoPending, aw *activityWriter, activityID string) {
	if undo == nil || aw.status == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 3*time.Second)
	defer cancel()
	if err := undo.Finish(ctx, aw.status, aw.body, activityID); err != nil && m.Log != nil {
		m.Log.Warn("agent_undo.record_failed", "path", r.URL.Path, "error", err.Error())
	}
}

// Unwrap lets http.ResponseController reach the server's writer.
func (a *activityWriter) Unwrap() http.ResponseWriter { return a.ResponseWriter }

// Flush keeps streaming handlers streaming.
func (a *activityWriter) Flush() { _ = http.NewResponseController(a.ResponseWriter).Flush() }

// Hijack hands the connection to a WebSocket handler; the request is
// recorded as switching protocols at that moment (ADR-20261111150000).
func (a *activityWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := http.NewResponseController(a.ResponseWriter).Hijack()
	if err == nil && a.status == 0 {
		a.status = http.StatusSwitchingProtocols
		if a.onHijack != nil {
			a.onHijack()
		}
	}
	return conn, rw, err
}

// recordAgentActivity writes the row off the request path: a detached
// context with a short timeout, so a slow database never delays the answer.
func (m *Middleware) recordAgentActivity(r *http.Request, acct state.Account, key state.APIKey, status int, note *agentActivityNote) {
	rec, ok := m.Authn.(AgentActivityRecorder)
	if !ok {
		// A gateway's store does not record activity; apid does it for it.
		if rec, ok = m.ActivityRecorder, m.ActivityRecorder != nil; !ok {
			return
		}
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
		ID: note.id, KeyID: key.ID, AccountID: acct.ID, OrgID: key.OrgID, Method: r.Method, Path: path,
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
