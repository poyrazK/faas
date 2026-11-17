package sandboxes

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/agentpresets"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
)

// TaskStore admits sandbox tasks; *agentstore.Store implements it.
type TaskStore interface {
	CreateSandboxTask(ctx context.Context, a agentstore.SandboxAdmission, call agentstore.SandboxTaskCall) (agentstore.Sandbox, bool, error)
}

// browseTaskSlack is the part of a browse task's lifetime spent booting the
// session and starting the browser, beyond the task's own timeout.
const browseTaskSlack = 3 * time.Minute

// AdmitBrowse admits a gregale_browse task (ADR-20261116182615 §3–§4): a
// browser session on web egress that fetches url, linked to call. It is
// admitted like POST /v1/sandboxes with browser: true and network web, so
// the same switches, plan limits and concurrency apply; it exposes no port
// and lives only as long as the task can. A call already linked returns
// its session with existing set.
func (h *Handler) AdmitBrowse(ctx context.Context, acct state.Account, url string, maxChars int,
	call agentstore.SandboxTaskCall) (sb agentstore.Sandbox, existing bool, prob *api.Problem) {
	store, ok := h.Store.(TaskStore)
	if !ok || h.Store == nil {
		return sb, false, api.ErrCapacity("sandbox tasks are unavailable on this control-plane host")
	}
	switch {
	case !api.ValidSandboxBrowseURL(url):
		return sb, false, api.ErrValidation("url must be an http or https URL of a public host on port 80 or 443, without credentials, at most 2048 bytes")
	case maxChars == 0:
		maxChars = api.SandboxBrowseDefaultChars
	case maxChars < 1 || maxChars > api.SandboxBrowseMaxChars:
		return sb, false, api.ErrValidation(fmt.Sprintf("max_chars must be 1-%d; got %d", api.SandboxBrowseMaxChars, maxChars))
	}
	timeout := api.SandboxTaskTimeoutSec[acct.Plan.PlanIndex()]
	if timeout == 0 {
		return sb, false, api.ErrAgentLimit(acct.Plan, "SandboxTaskTimeoutSec", 0, 1)
	}
	lifetime := timeout + int(browseTaskSlack/time.Second)
	lifetime = min(lifetime, api.SandboxMaxLifetimeSec[acct.Plan.PlanIndex()])
	a, prob := h.admission(acct, createBody{CreateSandboxRequest: api.CreateSandboxRequest{Preset: agentpresets.Names()[0],
		Browser: true, Network: &api.SandboxNetwork{Mode: api.SandboxNetworkWeb}, LifetimeS: lifetime}})
	if prob != nil {
		return sb, false, prob
	}
	// A task is read by its result, not over a preview.
	a.Ports = nil
	a.Task = &agentstore.SandboxTask{Kind: agentstore.SandboxTaskBrowse, URL: url, MaxChars: maxChars}
	sb, existing, err := store.CreateSandboxTask(ctx, a, call)
	if err != nil {
		return sb, false, h.problem(acct, "sandbox.task.create", err)
	}
	if !existing {
		h.audit(ctx, "sandbox.created", sb)
	}
	return sb, existing, nil
}

// TaskResultStore reads task results; *agentstore.Store implements it.
type TaskResultStore interface {
	SandboxTaskResult(ctx context.Context, accountID, id string) (agentstore.SandboxTaskResult, error)
}

// Task handles GET /v1/sandboxes/{id}/task: a task session's result, in
// full where gregale_browse's tool result had to shorten it.
func (h *Handler) Task(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !h.configured(w) {
		return
	}
	store, ok := h.Store.(TaskResultStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("sandbox tasks are unavailable on this control-plane host"))
		return
	}
	id := r.PathValue("id")
	res, err := store.SandboxTaskResult(r.Context(), acct.ID, id)
	if err != nil {
		api.WriteProblem(w, h.problem(acct, "sandbox.task.get", err))
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, TaskResultView(id, res))
}

// TaskResultView is a task's result as the API shows it: the command's JSON
// output when it exited 0 with JSON, its output as text otherwise, and how
// the session ended.
func TaskResultView(id string, res agentstore.SandboxTaskResult) api.SandboxTaskResult {
	out := api.SandboxTaskResult{SandboxID: id, Kind: res.Kind, URL: res.URL, State: res.State, ExitCode: res.ExitCode}
	switch {
	case res.ExitCode != nil && *res.ExitCode == 0 && json.Valid(res.Output):
		out.Result = json.RawMessage(res.Output)
	case len(res.Output) > 0:
		out.Output = strings.ToValidUTF8(string(res.Output), "�")
	}
	if res.OutcomeCode != "" {
		out.Outcome = &api.SandboxOutcome{Code: res.OutcomeCode, Message: res.OutcomeMessage}
	}
	return out
}

// BrowserStore admits gregale_browser sessions and records their actions;
// *agentstore.Store implements it.
type BrowserStore interface {
	CreateBrowserSession(ctx context.Context, a agentstore.SandboxAdmission, args agentstore.SandboxActionArgs,
		call agentstore.SandboxTaskCall) (agentstore.Sandbox, bool, error)
	BrowserSessionThread(ctx context.Context, accountID, id string) (agentstore.SandboxThread, error)
	CreateSandboxAction(ctx context.Context, accountID, sessionID, kind string, args agentstore.SandboxActionArgs,
		call agentstore.SandboxTaskCall) (agentstore.SandboxAction, bool, error)
	CloseSandbox(ctx context.Context, accountID, id string) (agentstore.Sandbox, error)
	SandboxActionResult(ctx context.Context, accountID, id string, seq int) (agentstore.SandboxActionResult, error)
}

// AdmitBrowserSession admits a gregale_browser session that opens on url
// (ADR-20261117065818 §3): a browser session on web egress, admitted like
// POST /v1/sandboxes with browser: true and network web, so the same
// switches, plan limits and concurrency apply. It closes after the plan's
// SandboxBrowserIdleCloseSec without an action and lives at most
// SandboxBrowserLifetimeSec. A call already linked returns its session
// with existing set.
func (h *Handler) AdmitBrowserSession(ctx context.Context, acct state.Account, url string, maxChars int,
	call agentstore.SandboxTaskCall) (sb agentstore.Sandbox, existing bool, prob *api.Problem) {
	store, ok := h.Store.(BrowserStore)
	if !ok || h.Store == nil {
		return sb, false, api.ErrCapacity("browser sessions are unavailable on this control-plane host")
	}
	if !api.ValidSandboxBrowseURL(url) {
		return sb, false, api.ErrValidation("url must be an http or https URL of a public host on port 80 or 443, without credentials, at most 2048 bytes")
	}
	idx := acct.Plan.PlanIndex()
	idle, lifetime := api.SandboxBrowserIdleCloseSec[idx], min(api.SandboxBrowserLifetimeSec[idx], api.SandboxMaxLifetimeSec[idx])
	if idle == 0 || lifetime == 0 {
		return sb, false, api.ErrAgentLimit(acct.Plan, "SandboxBrowserLifetimeSec", 0, 1)
	}
	a, prob := h.admission(acct, createBody{CreateSandboxRequest: api.CreateSandboxRequest{Preset: agentpresets.Names()[0],
		Browser: true, Network: &api.SandboxNetwork{Mode: api.SandboxNetworkWeb}, IdleTimeoutS: idle, LifetimeS: lifetime}})
	if prob != nil {
		return sb, false, prob
	}
	// The session is driven by its actions, not over a preview.
	a.Ports = nil
	sb, existing, err := store.CreateBrowserSession(ctx, a, agentstore.SandboxActionArgs{URL: url, MaxChars: maxChars}, call)
	if err != nil {
		return sb, false, h.problem(acct, "sandbox.browser.create", err)
	}
	if !existing {
		h.audit(ctx, "sandbox.created", sb)
	}
	return sb, existing, nil
}

// ActionResult handles GET /v1/sandboxes/{id}/actions/{seq}: one
// gregale_browser action, in full where its tool result had to shorten it.
func (h *Handler) ActionResult(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !h.configured(w) {
		return
	}
	store, ok := h.Store.(BrowserStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("browser sessions are unavailable on this control-plane host"))
		return
	}
	seq, err := strconv.Atoi(r.PathValue("seq"))
	if err != nil || seq < 1 {
		api.WriteProblem(w, api.ErrValidation("seq must be an action's sequence number, from 1"))
		return
	}
	id := r.PathValue("id")
	res, err := store.SandboxActionResult(r.Context(), acct.ID, id, seq)
	if err != nil {
		api.WriteProblem(w, h.problem(acct, "sandbox.action.get", err))
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, ActionResultView(id, seq, res))
}

// ActionResultView is an action as the API shows it: the snapshot as result
// when the command exited 0 with JSON, its output as text otherwise.
func ActionResultView(id string, seq int, res agentstore.SandboxActionResult) api.SandboxActionResult {
	out := api.SandboxActionResult{SandboxID: id, Seq: seq, Kind: res.Kind, URL: res.Args.URL, Ref: res.Args.Ref, Key: res.Args.Key,
		Direction: res.Args.Direction, State: res.State, ExitCode: res.ExitCode}
	switch {
	case res.ExitCode != nil && *res.ExitCode == 0 && json.Valid(res.Output):
		out.Result = json.RawMessage(res.Output)
	case len(res.Output) > 0:
		out.Output = strings.ToValidUTF8(string(res.Output), "�")
	}
	return out
}

// ActionScreenshot handles GET /v1/sandboxes/{id}/actions/{seq}/screenshot:
// the JPEG an action kept (ADR-20261117065818 §5), until its retention ends.
func (h *Handler) ActionScreenshot(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !h.configured(w) {
		return
	}
	store, ok := h.Store.(BrowserStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("browser sessions are unavailable on this control-plane host"))
		return
	}
	seq, err := strconv.Atoi(r.PathValue("seq"))
	if err != nil || seq < 1 {
		api.WriteProblem(w, api.ErrValidation("seq must be an action's sequence number, from 1"))
		return
	}
	res, err := store.SandboxActionResult(r.Context(), acct.ID, r.PathValue("id"), seq)
	if err != nil {
		api.WriteProblem(w, h.problem(acct, "sandbox.action.screenshot", err))
		return
	}
	shot := res.Screenshot
	if shot.Key == "" || shot.Bytes <= 0 || shot.Bytes > int64(api.SandboxScreenshotMaxBytes) {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Screenshot not found",
			"the action kept no screenshot, or it is past its retention"))
		return
	}
	if h.Files == nil {
		api.WriteProblem(w, api.ErrCapacity("screenshots are unavailable on this control-plane host"))
		return
	}
	body, err := h.Files.Get(r.Context(), shot.Key)
	if err != nil {
		if h.Log != nil {
			h.Log.Warn("sandboxes: read screenshot", "sandbox_id", r.PathValue("id"), "err", err)
		}
		api.WriteProblem(w, api.ErrCapacity("the screenshot could not be read"))
		return
	}
	defer func() { _ = body.Close() }()
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Content-Length", strconv.FormatInt(shot.Bytes, 10))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	_, _ = io.Copy(w, io.LimitReader(body, shot.Bytes))
}
