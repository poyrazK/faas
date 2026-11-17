package sandboxes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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
