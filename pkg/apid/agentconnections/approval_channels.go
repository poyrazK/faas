package agentconnections

import (
	"context"
	"fmt"
	"net/http"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

// GetApprovalChannels is GET /v1/agent-approvals/channels: the account's
// notification channels that get its agents' approval requests
// (ADR-20261107140000 §3).
func GetApprovalChannels(d Deps) httpkit.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		names, err := boundChannelNames(r.Context(), d.Store, acct.ID, "")
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not read the approval channels"))
			return
		}
		httpkit.WriteJSON(w, http.StatusOK, api.AgentApprovalChannelsResponse{Channels: names})
	}
}

// PutApprovalChannels is PUT /v1/agent-approvals/channels: it replaces those
// channels. An empty list routes requests to email only.
func PutApprovalChannels(d Deps) httpkit.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		var req api.AgentApprovalChannelsRequest
		if err := httpkit.DecodeJSON(r, &req); err != nil || req.Channels == nil {
			api.WriteProblem(w, api.ErrValidation("channels is required: the names of your notification channels, or an empty list"))
			return
		}
		if prob := setApprovalChannels(r.Context(), d.Store, acct.ID, "", req.Channels); prob != nil {
			api.WriteProblem(w, prob)
			return
		}
		if d.Audit != nil {
			d.Audit(r.Context(), "agent.approval.channels_changed", acct.ID, map[string]any{"channels": req.Channels})
		}
		names, err := boundChannelNames(r.Context(), d.Store, acct.ID, "")
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("the channels are saved, but could not be read back; retry"))
			return
		}
		httpkit.WriteJSON(w, http.StatusOK, api.AgentApprovalChannelsResponse{Channels: names})
	}
}

// setApprovalChannels binds the account's channels with these names to one
// scope, replacing what was bound.
func setApprovalChannels(ctx context.Context, store state.Store, accountID, orgID string, names []string) *api.Problem {
	bindings, ok := store.(state.AgentApprovalChannelStore)
	channels, okChannels := store.(state.NotificationChannelStore)
	if !ok || !okChannels {
		return api.ErrCapacity("approval channels unavailable")
	}
	if len(names) > api.MaxAgentApprovalChannels {
		limit, observed := int64(api.MaxAgentApprovalChannels), int64(len(names))
		return api.ErrValidation(fmt.Sprintf("at most %d approval channels", api.MaxAgentApprovalChannels)).WithLimit(limit, observed)
	}
	own, err := channels.ListNotificationChannels(ctx, accountID)
	if err != nil {
		return api.ErrCapacity("could not read your notification channels")
	}
	ids := make([]string, 0, len(names))
	for _, name := range names {
		i := slices.IndexFunc(own, func(ch state.NotificationChannel) bool { return ch.Name == name })
		if i < 0 {
			return api.ErrValidation(fmt.Sprintf("no notification channel named %q: create it with `gregale alert channel create`", name))
		}
		if !slices.Contains(ids, own[i].ID) {
			ids = append(ids, own[i].ID)
		}
	}
	if err := bindings.SetAgentApprovalChannels(ctx, accountID, orgID, ids); err != nil {
		return api.ErrCapacity("could not save the approval channels")
	}
	return nil
}

func boundChannelNames(ctx context.Context, store state.Store, accountID, orgID string) ([]string, error) {
	names := []string{}
	bindings, ok := store.(state.AgentApprovalChannelStore)
	if !ok {
		return names, nil
	}
	chans, err := bindings.AgentApprovalChannels(ctx, accountID, orgID)
	if err != nil {
		return nil, err
	}
	for _, ch := range chans {
		names = append(names, ch.Name)
	}
	return names, nil
}
