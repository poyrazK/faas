// Package stageworkflows holds apid's rules for a stage's stored workflows
// (ADR-20261104120000 amendment 2026-11-22). The app's stored workflow
// routes take ?environment=<slug>; omitted or production is production. A
// workflow is reached only through its own environment. A stage's workflow
// can be listed, read, saved, published, enabled, disabled, deleted and its
// revisions read; a stage runs manual and schedule workflows on its own
// deployment. The routes that test, verify or watch a workflow refuse it.
package stageworkflows

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Request is one stored workflow route's view of the environments involved.
type Request struct {
	// Requested is the environment the caller names ("" is production).
	Requested string
	// Held is the environment holding the named workflow; Found is false
	// when no environment does (or the route names no workflow).
	Held  string
	Found bool
	// StageReady marks the routes a stage's workflow supports.
	StageReady bool
	// Write marks a route that changes the workflow.
	Write bool
	// Open is the runtime key stage_stored_workflows_enabled.
	Open bool
}

// Check refuses a route that reaches a workflow through another environment
// than its own, a route a stage's workflow does not support yet, and a stage
// write while operators keep stage_stored_workflows_enabled off: the key
// waits until every apid and schedd runs the release whose readers keep a
// stage's workflows out of production and run them on the stage. A manual
// run start in a stage is such a write. Production's routes are unchanged.
func Check(req Request) *api.Problem {
	if req.Found && req.Held != req.Requested {
		if req.Write {
			return Problem(state.ErrStoredWorkflowNameInOtherEnvironment)
		}
		return api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such stored workflow")
	}
	if req.Requested == "" {
		return nil
	}
	if !req.StageReady {
		return api.NewProblem(http.StatusNotImplemented, api.CodeNotImplemented, "Not available in a stage",
			"a stage's stored workflow cannot be tested, verified, watched or restored yet; use production")
	}
	if req.Write && !req.Open {
		return api.NewProblem(http.StatusNotImplemented, api.CodeNotImplemented, "Stage stored workflows unavailable",
			"stored workflows in a stage are not available yet; create the workflow in production")
	}
	return nil
}

// Problem maps the store's stage refusals, or returns nil.
func Problem(err error) *api.Problem {
	switch {
	case errors.Is(err, state.ErrStoredWorkflowNameInOtherEnvironment):
		return api.NewProblem(http.StatusConflict, api.CodeConflict, "Name in use",
			"another environment of this app has a stored workflow with this name; until a later release, names are unique per app")
	case errors.Is(err, state.ErrStoredWorkflowStageDisabled):
		return api.NewProblem(http.StatusConflict, api.CodeConflict, "Stage workflow cannot be enabled",
			"a stage runs only manual and schedule workflows; this one stays disabled")
	}
	return nil
}
