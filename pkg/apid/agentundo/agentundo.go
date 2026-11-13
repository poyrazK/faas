// Package agentundo reverses what an agent key changed (ADR-20261112080601).
//
// For each write an agent makes, apid's agent middleware asks Begin for a
// capture before the handler runs and finishes it after the handler
// answered. The capture reads the resource through apid's own handlers (a
// Dispatcher) and stores an undo record: the request that reverses the write
// (the inverse) and a check that tells, later, whether someone changed the
// resource again since. Plan and Apply turn a key's records into an undo a
// person reviews and applies; the inverse requests run through the same
// handlers, as that person.
package agentundo

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// Dispatcher runs one of apid's handlers for method and target (a path with
// an optional query) as acct, with base's context (its principal), and
// returns the status and body. A route it does not serve answers 404.
type Dispatcher func(ctx context.Context, base *http.Request, acct state.Account, method, target string, body []byte) (int, []byte)

// Engine captures, plans and applies undos.
type Engine struct {
	Store    state.AgentUndoStore
	Dispatch Dispatcher
	// Lookup resolves what a capture cannot read through Dispatch; nil
	// leaves those writes not undoable.
	Lookup Lookup
	Now    func() time.Time
}

// Lookup answers identities the routes undo reads through do not show.
type Lookup interface {
	// DeploymentApp is the slug of the app deployment belongs to, as acct
	// sees it.
	DeploymentApp(ctx context.Context, acct state.Account, deployment string) (string, error)
	// EnvironmentID is the id of the named environment of the app's
	// project, as acct sees it.
	EnvironmentID(ctx context.Context, acct state.Account, appSlug, environment string) (string, error)
}

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now().UTC()
	}
	return time.Now().UTC()
}

// Inverse is the request that reverses an agent's write. A Body field
// "expected_revision" is filled at apply time from the Check.
type Inverse struct {
	Method string          `json:"method"`
	Path   string          `json:"path"`
	Body   json.RawMessage `json:"body,omitempty"`
	// NewRequestID asks apply to set the body's request_id to a fresh UUID.
	NewRequestID bool `json:"new_request_id,omitempty"`
	// GoneIsDone treats a 404 answer as nothing left to undo.
	GoneIsDone bool `json:"gone_is_done,omitempty"`
	// Deploy, when set, undoes a deploy or rollback by the app's state at
	// apply time instead of sending Method and Path as they are.
	Deploy *DeployUndo `json:"deploy,omitempty"`
	// Traffic, when set, puts back an earlier traffic split instead of
	// sending Method and Path.
	Traffic *TrafficUndo `json:"traffic,omitempty"`
}

// Check kinds.
const (
	// CheckFields reads Path and compares Fields (inside Object, when set)
	// with the values the agent left.
	CheckFields = "fields"
	// CheckConfig compares the variables of the scope's newest config
	// revision with those of AfterRevision.
	CheckConfig = "config"
)

// Check tells whether the resource is still as the agent left it.
type Check struct {
	Kind   string                     `json:"kind"`
	Path   string                     `json:"path"`
	Object string                     `json:"object,omitempty"`
	Fields map[string]json.RawMessage `json:"fields,omitempty"`
	// RevisionField names the field of Path's answer whose value apply
	// sends as the inverse body's expected_revision.
	RevisionField string `json:"revision_field,omitempty"`
	AfterRevision int64  `json:"after_revision,omitempty"`
	// App and Split are what a traffic check reads: the app's path and the
	// live split the agent left.
	App   string  `json:"app,omitempty"`
	Split []Split `json:"split,omitempty"`
}

// resource is what two records of one resource share: undoing them must go
// newest first, and a failure leaves the older ones alone.
func (c *Check) resource() string {
	if c == nil {
		return ""
	}
	return c.Path
}

// Reasons a change cannot be undone.
const (
	reasonUnknownRoute = "Gregale cannot undo this kind of change yet"
	reasonCapture      = "Gregale could not read what this change replaced"
	reasonSecret       = "Gregale keeps no copy of a replaced secret, so a secret change cannot be undone; set the secret again"
	reasonNoRevision   = "there is no earlier configuration revision to return to"
	reasonNothing      = "the request changed nothing"
)

func is2xx(status int) bool { return status >= 200 && status < 300 }

// problemOf reads the code and detail of an RFC 7807 answer.
func problemOf(status int, body []byte) (code, detail string) {
	var p struct {
		Code   string `json:"code"`
		Title  string `json:"title"`
		Detail string `json:"detail"`
	}
	if json.Unmarshal(body, &p) == nil && (p.Code != "" || p.Detail != "") {
		if p.Detail == "" {
			p.Detail = p.Title
		}
		return p.Code, p.Detail
	}
	return "undo_failed", http.StatusText(status)
}
