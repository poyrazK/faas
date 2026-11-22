package sandboxes

// adr: 20261122132055 §3 — forks: N new sessions start from a running or
// paused session's memory and volume as they are now. apid queues them;
// agentd takes the source's fork capture (or uses its pause capture) and
// starts each fork from it once the shared store holds it.

import (
	"fmt"
	"maps"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/apid/resourcelabels"
	"github.com/onebox-faas/faas/pkg/labels"
	"github.com/onebox-faas/faas/pkg/state"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
)

// Fork handles POST /v1/sandboxes/{id}/fork. It answers 202 with the
// queued forks, each with its session token.
func (h *Handler) Fork(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !h.configured(w) {
		return
	}
	if !h.Persistent {
		api.WriteProblem(w, api.ErrSandboxUnsupported("fork", "forks are not available on this platform yet"))
		return
	}
	var body api.ForkSandboxRequest
	if !httpkit.DecodeJSONLimit(w, r, &body, requestMaxBytes) {
		return
	}
	src, err := h.Store.Sandbox(r.Context(), acct.ID, r.PathValue("id"))
	if err != nil {
		api.WriteProblem(w, h.problem(acct, "sandbox.fork", err))
		return
	}
	admission, prob := h.forkAdmission(acct, src, body)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	forks, err := h.Store.ForkSandbox(r.Context(), admission)
	if err != nil {
		api.WriteProblem(w, h.problem(acct, "sandbox.fork", err))
		return
	}
	out := api.ForkSandboxResponse{Forks: make([]api.Sandbox, 0, len(forks))}
	for _, f := range forks {
		h.audit(r.Context(), "sandbox.forked", f)
		out.Forks = append(out.Forks, h.view(acct, f, true))
	}
	httpkit.WriteJSON(w, http.StatusAccepted, out)
}

// forkAdmission checks a fork request against the plan: the count, the
// forks' kind and, for ephemeral forks, the source's shape.
func (h *Handler) forkAdmission(acct state.Account, src agentstore.Sandbox, body api.ForkSandboxRequest) (agentstore.SandboxForkAdmission, *api.Problem) {
	idx := acct.Plan.PlanIndex()
	a := agentstore.SandboxForkAdmission{AccountID: acct.ID, SourceID: src.ID, Count: body.Count, Kind: body.Kind,
		Labels: body.Labels, QueuedMax: api.AgentRunQueuedPerAccount[idx], PersistentMax: api.SandboxPersistentMax[idx]}
	if a.Count == 0 {
		a.Count = 1
	}
	if a.Kind == "" {
		// Forks are ephemeral unless asked (ADR-20261122132055 §3).
		a.Kind = api.SandboxKindEphemeral
	}
	if a.Labels == nil {
		a.Labels = maps.Clone(src.Labels)
	}
	max := api.SandboxForkMax[idx]
	switch {
	case a.Count < 0:
		return a, api.ErrValidation("count must be at least 1")
	case a.Count > max:
		return a, api.ErrAgentLimit(acct.Plan, "SandboxForkMax", max, a.Count)
	case a.Kind != api.SandboxKindEphemeral && a.Kind != api.SandboxKindPersistent:
		return a, api.ErrValidation("kind must be ephemeral or persistent")
	case a.Kind == api.SandboxKindPersistent && a.PersistentMax == 0:
		return a, api.ErrAgentLimit(acct.Plan, "SandboxPersistentMax", 0, a.Count)
	}
	if prob := resourcelabels.Problem(labels.Change{Set: a.Labels}.Validate()); prob != nil {
		return a, prob
	}
	lifetime := api.SandboxMaxLifetimeSec[idx]
	if a.Kind == api.SandboxKindPersistent {
		lifetime = api.SandboxPersistentRunMaxSec[idx]
	} else if prob := ephemeralForkShape(acct.Plan, src); prob != nil {
		return a, prob
	}
	a.ExpiresAt = h.now().UTC().Add(time.Duration(lifetime) * time.Second)
	return a, nil
}

// ephemeralForkShape requires the source's machine to fit an ephemeral
// session's: a fork restores its source's memory and vCPUs as they are.
func ephemeralForkShape(plan api.Plan, src agentstore.Sandbox) *api.Problem {
	idx := plan.PlanIndex()
	switch {
	case src.RAMMB > api.AgentRunRAMMBMax[idx]:
		return api.ErrAgentLimit(plan, "AgentRunRAMMBMax", api.AgentRunRAMMBMax[idx], src.RAMMB)
	case src.VCPU > api.AgentRunVCPUMax[idx]:
		return api.ErrAgentLimit(plan, "AgentRunVCPUMax", api.AgentRunVCPUMax[idx], src.VCPU)
	case src.VolumeGB > api.SandboxVolumeMaxGB[idx]:
		return api.ErrValidation(fmt.Sprintf("the session's %d GB volume is above the plan's SandboxVolumeMaxGB", src.VolumeGB))
	}
	return nil
}
