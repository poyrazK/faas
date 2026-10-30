package agents

import (
	"github.com/onebox-faas/faas/pkg/api"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
)

func agentJSON(a agentstore.Agent, latest *agentstore.Version) api.Agent {
	out := api.Agent{ID: a.ID, Name: a.Name, Description: a.Description, LatestVersion: a.LatestVersion,
		CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt}
	if latest != nil {
		v := versionJSON(*latest)
		out.Version = &v
	}
	return out
}

func versionJSON(v agentstore.Version) api.AgentVersion {
	h := v.Harness
	return api.AgentVersion{
		Version: v.Version,
		AgentVersionSpec: api.AgentVersionSpec{
			Harness:        api.AgentHarness{Preset: h.Preset, Image: h.Image, Run: h.Run, Models: h.Models},
			PromptTemplate: v.PromptTemplate, RAMMB: v.RAMMB, TimeoutSec: v.TimeoutSec,
			Integrations: v.Integrations, AIBudgetMillicents: v.AIBudgetMillicents, Deliveries: deliveriesJSON(v.Deliveries),
		},
		CreatedAt: v.CreatedAt,
	}
}

func deliveriesJSON(ds []agentstore.Delivery) []api.AgentDelivery {
	var out []api.AgentDelivery
	for _, d := range ds {
		out = append(out, api.AgentDelivery{Type: d.Type, Field: d.Field, Bucket: d.Bucket, Prefix: d.Prefix})
	}
	return out
}

func outputsJSON(o agentstore.Outputs) api.AgentRunOutputs {
	out := api.AgentRunOutputs{Result: o.Result, Files: []api.AgentOutputFile{}, LogTail: o.LogTail, LogTruncated: o.LogTruncated,
		Deliveries: o.Deliveries}
	for _, f := range o.Files {
		out.Files = append(out.Files, api.AgentOutputFile{Name: f.Name, Size: f.Size, SHA256: f.SHA256})
	}
	return out
}

func runJSON(r agentstore.Run) api.AgentRun {
	out := api.AgentRun{
		ID: r.ID, Agent: r.AgentName, AgentID: r.AgentID, AgentVersion: r.AgentVersion, Tenant: r.TenantID,
		Trigger: r.Trigger, DedupeKey: r.DedupeKey, State: r.State, RAMMB: r.RAMMB, TimeoutSec: r.TimeoutSec,
		Attempt: r.Attempt, CancelRequestedAt: r.CancelRequestedAt, QueuedAt: r.QueuedAt,
		StartedAt: r.StartedAt, RunningAt: r.RunningAt, FinishedAt: r.FinishedAt,
	}
	if r.OutcomeCode != "" {
		out.Outcome = &api.AgentRunOutcome{Code: r.OutcomeCode, Message: r.OutcomeMessage}
	}
	if c := r.Checkout; c != nil {
		out.Checkout = &api.AgentCheckout{Repository: c.Repository, Commit: c.Commit, InstallationID: c.InstallationID}
	}
	if g := r.GitHub; g != nil {
		out.GitHub = &api.AgentGitHubTarget{Repository: g.Repository, Number: g.Number, InstallationID: g.InstallationID}
	}
	return out
}
