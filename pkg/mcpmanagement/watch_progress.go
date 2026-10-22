package mcpmanagement

import (
	"context"
	"encoding/json"
	"math"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const maxWatchProgressUpdates = 46

type watchProgressKey struct{}

// Only fixed identifiers, verdict flags and bounded counts reach progress.
// This deliberately excludes raw lifecycle diagnostics, resource identities,
// timestamps and gateway names, including from the change fingerprint.
type watchProgressState struct {
	Build           string `json:"build"`
	Accepted        bool   `json:"accepted"`
	Live            bool   `json:"live"`
	Healthy         bool   `json:"healthy"`
	Completed       bool   `json:"completed"`
	Failed          bool   `json:"failed"`
	CapacityKnown   bool   `json:"capacity_known"`
	Required        int    `json:"required"`
	Ready           int    `json:"ready"`
	Starting        int    `json:"starting"`
	Unready         int    `json:"unready"`
	HandoffAction   string `json:"handoff_action"`
	Bindings        string `json:"bindings"`
	Handoff         string `json:"handoff"`
	MissingGateways int    `json:"missing_gateways"`
	WorkerRunning   int64  `json:"worker_running"`
	WorkerStarting  int64  `json:"worker_starting"`
	WorkerDraining  int64  `json:"worker_draining"`
	WorkerOther     int64  `json:"worker_other"`
	WorkerRetired   int64  `json:"worker_retired"`
	WorkerConverged bool   `json:"worker_converged"`
}

type watchProgress struct {
	management *management
	args       any
	notify     func(context.Context, *mcp.ProgressNotificationParams) error
	token      any
	last       watchProgressState
	sent       int
}

func (m *management) withWatchProgress(ctx context.Context, req *mcp.CallToolRequest, args any) (context.Context, error) {
	token := req.Params.GetProgressToken()
	if token == nil {
		return ctx, nil
	}
	valid := false
	switch t := token.(type) {
	case string:
		valid = len(t) > 0 && len(t) <= 128 && utf8.ValidString(t)
	case float64:
		valid = !math.IsNaN(t) && !math.IsInf(t, 0)
	}
	if !valid {
		return ctx, invalid("invalid_argument", "progressToken must be a finite number or a nonempty string of at most 128 bytes")
	}
	p := &watchProgress{management: m, args: args, token: token, notify: req.Session.NotifyProgress}
	return context.WithValue(ctx, watchProgressKey{}, p), nil
}

func reportWatchProgress(ctx context.Context, out watchRolloutResult) error {
	p, ok := ctx.Value(watchProgressKey{}).(*watchProgress)
	if !ok {
		return nil
	}
	state := rolloutProgressState(out)
	if p.sent >= maxWatchProgressUpdates || p.sent > 0 && state == p.last {
		return nil
	}
	// No asynchronous sender or detached context survives cancellation. Reserve
	// a small send budget so a slow client cannot extend a bounded watch.
	if err := p.management.authorizeArgs(ctx, p.args); err != nil {
		return err
	}
	sendCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	if err := sendCtx.Err(); err != nil {
		return err
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if err := p.notify(sendCtx, &mcp.ProgressNotificationParams{ProgressToken: p.token, Progress: float64(p.sent + 1), Message: string(data)}); err != nil {
		return err
	}
	p.sent++
	p.last = state
	return nil
}

func rolloutProgressState(out watchRolloutResult) watchProgressState {
	state := watchProgressState{Build: "none", Accepted: out.Accepted, Live: out.Live, Healthy: out.Healthy, Completed: out.Completed, Failed: out.Failed, Handoff: "none"}
	if out.Build != nil {
		state.Build = "unknown"
		switch out.Build.Status {
		case "queued", "running", "succeeded", "failed", "cancelled":
			state.Build = out.Build.Status
		}
	}
	c := out.Health.Capacity
	state.CapacityKnown = c.Known
	if c.Known {
		state.Required = boundedProgressCount(c.Required)
		state.Ready = boundedProgressCount(c.Ready)
		state.Starting = boundedProgressCount(c.Starting)
		state.Unready = boundedProgressCount(c.Unready)
	}
	if h := out.Deployment.ServiceRolloutHandoff; h != nil {
		state.Handoff = "unknown"
		switch h.Phase {
		case "pending", "routing", "draining", "complete":
			state.Handoff = h.Phase
		}
		state.HandoffAction = "unknown"
		switch h.Action {
		case "promote", "abort":
			state.HandoffAction = h.Action
		}
		if h.BindingsCheck != nil {
			state.Bindings = "unknown"
			switch h.BindingsCheck.Status {
			case "pending", "passed", "blocked", "unknown":
				state.Bindings = h.BindingsCheck.Status
			}
		}
		state.MissingGateways = boundedProgressCount(len(h.MissingGateways))
	}
	if w := out.Worker; w != nil {
		state.WorkerRunning = min(max(w.Running, 0), 1000000)
		state.WorkerStarting = min(max(w.Starting, 0), 1000000)
		state.WorkerDraining = min(max(w.Draining, 0), 1000000)
		state.WorkerOther = min(max(w.OtherResidents, 0), 1000000)
		state.WorkerRetired = min(max(w.RetiredResidents, 0), 1000000)
		state.WorkerConverged = w.GenerationConverged && w.SelectedDeploymentID == out.Deployment.ID
	}
	return state
}
func boundedProgressCount(n int) int { return min(max(n, 0), 1000000) }
