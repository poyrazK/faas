package main

// JobTaskAttachHandler owns `GET /v1/jobs/{name}/runs/{id}/tasks/{idx}/attach`
// (ADR-1330): the attach of an interactive image run's task. It resolves the
// task through the job name in the caller's account, then opens the session
// exactly as for an interactive app task (ADR-1037).

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	mwauth "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

type jobTaskAttachTargets interface {
	JobTaskAttachTarget(ctx context.Context, accountID, jobName, runID string, taskIndex int) (state.JobTaskAttachTarget, error)
}

type JobTaskAttachHandler struct {
	Auth    *mwauth.Middleware
	Targets jobTaskAttachTargets
	Nodes   appTaskAttachNodes
	Log     *slog.Logger
	// PingInterval keeps idle sessions alive through proxies; tests shorten it.
	PingInterval time.Duration
}

// ServeHTTP applies POST /v1/image-runs' auth: an attach runs customer code.
func (h *JobTaskAttachHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	inner := mwauth.AccountHandler(h.attach)
	chain := h.Auth.RequireScope(api.ScopesDeployWriteSurface...)(h.Auth.RequireMFA(inner))
	h.Auth.RequireLimited(chain)(w, r)
}

func (h *JobTaskAttachHandler) attach(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !acct.Plan.JobsAllowed() {
		api.WriteProblem(w, api.ErrPlanJobsNotAllowed(acct.Plan))
		return
	}
	token, ok := attachToken(w, r)
	if !ok {
		return
	}
	notFound := api.NewProblem(http.StatusNotFound, api.CodeNotFound,
		"Interactive task not found", "no interactive task with this index exists for the run")
	index, err := strconv.Atoi(r.PathValue("idx"))
	if err != nil || index < 0 {
		api.WriteProblem(w, notFound)
		return
	}
	target, err := h.Targets.JobTaskAttachTarget(r.Context(), acct.ID, r.PathValue("name"), r.PathValue("id"), index)
	if errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, notFound)
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not load the interactive task"))
		return
	}
	switch {
	case target.Terminal():
		api.WriteProblem(w, api.NewProblem(http.StatusGone, api.CodeConflict,
			"Session ended", "the interactive task has already finished"))
		return
	case !target.Ready():
		writeAttachNotReady(w)
		return
	}
	session := &AppTaskAttachHandler{Nodes: h.Nodes, Log: h.Log, PingInterval: h.PingInterval}
	session.open(w, r, target.NodeID, target.InstanceID, token)
}
