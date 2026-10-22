// adr: 20261022183526
package gateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// A contained worker reads its consumer group as an idle one: captured
// publishes never reach retained history, so nothing is ever claimable inside
// the run, and production messages are never claimed. Calls are answered,
// not captured; they are polls, not intents.

// realtimeGroupAddress names a consumer group: by app, endpoint, channel and
// name on the app-key routes, or by ID on the worker-credential routes.
type realtimeGroupAddress struct {
	slug, endpoint, channel, name string
	groupID                       string
}

func (a realtimeGroupAddress) worker() bool { return a.groupID != "" }

// realtimeGroupRoute serves .../groups/{group}[/action] for an app-key call or
// /v1/realtime/workers/groups/{group_id}[/action] for a worker credential.
func realtimeGroupRoute(method string, address realtimeGroupAddress, action []string) (rehearsalPlatformRoute, bool) {
	if method == http.MethodGet {
		if len(action) != 0 {
			return nil, false
		}
		return address.serve(serveRealtimeGroupStatus), true
	}
	switch strings.Join(action, "/") {
	case "claims":
		return address.serve(serveRealtimeGroupClaim), true
	case "claims/batch":
		return address.serve(serveRealtimeGroupBatchClaim), true
	case "claims/settle":
		return address.serve(serveRealtimeGroupSettle), true
	case "claims/settle/batch":
		return address.serve(serveRealtimeGroupBatchSettle), true
	case "heartbeats":
		return address.serve(serveRealtimeGroupHeartbeat), true
	}
	return nil, false
}

// realtimeGroupStatusRoute matches GET
// /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}.
func realtimeGroupStatusRoute(path string) (rehearsalPlatformRoute, bool) {
	rest, found := strings.CutPrefix(path, "/v1/apps/")
	if !found {
		return nil, false
	}
	parts := strings.Split(rest, "/")
	if len(parts) != 8 || parts[0] == "" || parts[1] != "realtime" || parts[2] != "endpoints" || parts[3] == "" || parts[4] != "channels" || parts[5] == "" ||
		parts[6] != "groups" || parts[7] == "" {
		return nil, false
	}
	return realtimeGroupRoute(http.MethodGet, realtimeGroupAddress{slug: parts[0], endpoint: parts[3], channel: parts[5], name: parts[7]}, nil)
}

// realtimeWorkerGroupRoute matches /v1/realtime/workers/groups/{group_id}.
func realtimeWorkerGroupRoute(method, path string) (rehearsalPlatformRoute, bool) {
	rest, found := strings.CutPrefix(path, "/v1/realtime/workers/groups/")
	if !found {
		return nil, false
	}
	parts := strings.Split(rest, "/")
	if parts[0] == "" {
		return nil, false
	}
	return realtimeGroupRoute(method, realtimeGroupAddress{groupID: parts[0]}, parts[1:])
}

type realtimeGroupHandler func(http.ResponseWriter, *http.Request, rehearsalPlatformCall, realtimeGroupAddress, state.ScenarioRealtimeGroup)

// serve resolves the group before the body, as apid loads it first.
func (a realtimeGroupAddress) serve(next realtimeGroupHandler) rehearsalPlatformRoute {
	return func(w http.ResponseWriter, r *http.Request, call rehearsalPlatformCall) {
		var group state.ScenarioRealtimeGroup
		var err error
		if a.worker() {
			group, err = call.store.ScenarioRealtimeWorkerGroup(r.Context(), call.policy.AccountID, a.groupID)
		} else {
			if problem := realtimeChannelProblem(a.channel); problem != nil {
				api.WriteProblem(w, problem)
				return
			}
			group, err = call.store.ScenarioRealtimeConsumerGroup(r.Context(), call.policy.AccountID, call.policy.RunID, a.slug, a.endpoint, a.channel, a.name)
		}
		switch {
		case errors.Is(err, state.ErrNotFound) && a.worker():
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "group not found"))
			return
		case errors.Is(err, state.ErrNotFound):
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "realtime consumer group not found"))
			return
		case err != nil:
			api.WriteProblem(w, api.ErrCapacity("consumer group unavailable"))
			return
		}
		if r.Method != http.MethodGet && !group.EndpointEnabled {
			api.WriteProblem(w, api.ErrRealtimeInvalid("endpoint must be enabled"))
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		next(w, r, call, a, group)
	}
}

// writeRehearsalIdle answers a call the run serves without capturing it.
func writeRehearsalIdle(w http.ResponseWriter, status int, body any) {
	w.Header().Set("X-Gregale-Rehearsal", "idle")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func realtimeGroupInvalid() *api.Problem {
	return api.ErrRealtimeInvalid("invalid group, worker, sequence, claim token, idempotency key or retry policy")
}

func realtimeGroupBoundToTrigger() *api.Problem {
	return api.NewProblem(http.StatusConflict, "realtime_group_bound_to_trigger", "Consumer group is bound to a trigger",
		"a realtime trigger consumes this group; delete the trigger to claim or settle its messages directly").WithDocs("https://gregale.dev/docs/realtime#function-triggers")
}

func realtimeGroupLeaseLost() *api.Problem {
	return api.NewProblem(http.StatusConflict, "realtime_group_lease_lost", "Claim lease lost", "the claim expired, was already settled, or has been reassigned; this token can no longer complete it")
}

func decodeRealtimeGroupBody(w http.ResponseWriter, r *http.Request, call rehearsalPlatformCall, limit int64, dst any) bool {
	body, problem := readRehearsalPlatformBody(w, r, limit, call.now)
	return problem == nil && decodeRehearsalStrictJSON(body, dst) == nil
}

// realtimeGroupWorker is the worker a claim names. A worker credential fixes
// its own identity, so an omitted name stands for it.
func realtimeGroupWorker(address realtimeGroupAddress, worker string) string {
	if worker == "" && address.worker() {
		return "credential"
	}
	return worker
}

// waitRealtimeGroupClaim holds a claim for its wait_seconds, as an idle group
// does, or until the caller leaves.
func waitRealtimeGroupClaim(r *http.Request, seconds int) {
	if seconds <= 0 {
		return
	}
	timer := time.NewTimer(time.Duration(seconds) * time.Second)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-r.Context().Done():
	}
}

func serveRealtimeGroupStatus(w http.ResponseWriter, _ *http.Request, _ rehearsalPlatformCall, _ realtimeGroupAddress, group state.ScenarioRealtimeGroup) {
	writeRehearsalIdle(w, http.StatusOK, state.RealtimeConsumerGroup{GroupID: group.GroupID, Name: group.Name, Channel: group.Channel,
		LeaseSeconds: group.LeaseSeconds, MaxAttempts: group.MaxAttempts, CreatedAt: group.CreatedAt, OrderingMetadataKey: group.OrderingMetadataKey,
		DeadLetterPolicy: group.DeadLetterPolicy, DeadLetterRetentionSeconds: group.DeadLetterRetentionSeconds, DeadlineMetadataKey: group.DeadlineMetaKey,
		Filter: group.Filter, TriggerID: group.TriggerID, AssignedSequence: "0", LatestSequence: "0", OldestSequence: "0", Unassigned: "0", Completed: "0",
		DuplicateMessages: "0", FilteredMessages: "0", ExpiredDeliveries: "0", ExpiredDeadLetters: "0", LastFencingToken: "0"})
}

func serveRealtimeGroupClaim(w http.ResponseWriter, r *http.Request, call rehearsalPlatformCall, address realtimeGroupAddress, group state.ScenarioRealtimeGroup) {
	var req struct {
		Worker      string `json:"worker,omitempty"`
		WaitSeconds int    `json:"wait_seconds,omitempty"`
	}
	if !decodeRealtimeGroupBody(w, r, call, api.RealtimeConsumerGroupBodyMaxBytes, &req) {
		api.WriteProblem(w, api.ErrRealtimeInvalid("invalid worker identity"))
		return
	}
	if !state.ValidRealtimeConsumerGroupClaim(realtimeGroupWorker(address, req.Worker), 1, req.WaitSeconds) {
		api.WriteProblem(w, realtimeGroupInvalid())
		return
	}
	if group.TriggerID != "" {
		api.WriteProblem(w, realtimeGroupBoundToTrigger())
		return
	}
	waitRealtimeGroupClaim(r, req.WaitSeconds)
	w.Header().Set("X-Gregale-Rehearsal", "idle")
	w.WriteHeader(http.StatusNoContent)
}

func serveRealtimeGroupBatchClaim(w http.ResponseWriter, r *http.Request, call rehearsalPlatformCall, address realtimeGroupAddress, group state.ScenarioRealtimeGroup) {
	var req state.RealtimeConsumerGroupBatchRequest
	if !decodeRealtimeGroupBody(w, r, call, api.RealtimeConsumerGroupBodyMaxBytes, &req) {
		api.WriteProblem(w, api.ErrRealtimeInvalid("invalid batch claim request"))
		return
	}
	if !state.ValidRealtimeConsumerGroupClaim(realtimeGroupWorker(address, req.Worker), req.Limit, req.WaitSeconds) {
		api.WriteProblem(w, realtimeGroupInvalid())
		return
	}
	if group.TriggerID != "" {
		api.WriteProblem(w, realtimeGroupBoundToTrigger())
		return
	}
	waitRealtimeGroupClaim(r, req.WaitSeconds)
	writeRehearsalIdle(w, http.StatusOK, state.RealtimeConsumerGroupBatch{Claims: []state.RealtimeConsumerGroupClaim{}})
}

// A settlement can only name a claim the run never received, so it is the
// lease-lost answer for any valid token.
func serveRealtimeGroupSettle(w http.ResponseWriter, r *http.Request, call rehearsalPlatformCall, _ realtimeGroupAddress, group state.ScenarioRealtimeGroup) {
	var req state.RealtimeConsumerGroupSettlement
	if !decodeRealtimeGroupBody(w, r, call, api.RealtimeConsumerGroupBodyMaxBytes, &req) || !state.ValidRealtimeConsumerGroupSettlement(req) {
		api.WriteProblem(w, realtimeGroupInvalid())
		return
	}
	if group.TriggerID != "" {
		api.WriteProblem(w, realtimeGroupBoundToTrigger())
		return
	}
	api.WriteProblem(w, realtimeGroupLeaseLost())
}

func serveRealtimeGroupBatchSettle(w http.ResponseWriter, r *http.Request, call rehearsalPlatformCall, _ realtimeGroupAddress, group state.ScenarioRealtimeGroup) {
	var req state.RealtimeConsumerGroupBatchSettlementRequest
	if !decodeRealtimeGroupBody(w, r, call, api.RealtimeConsumerGroupSettlementBodyMaxBytes, &req) {
		api.WriteProblem(w, api.ErrRealtimeInvalid("invalid batch settlement"))
		return
	}
	if !state.ValidRealtimeConsumerGroupBatchSettlement(req) {
		api.WriteProblem(w, realtimeGroupInvalid())
		return
	}
	if group.TriggerID != "" {
		api.WriteProblem(w, realtimeGroupBoundToTrigger())
		return
	}
	result := state.RealtimeConsumerGroupBatchSettlementResult{Results: make([]state.RealtimeConsumerGroupBatchSettlementItem, 0, len(req.Settlements))}
	for _, item := range req.Settlements {
		result.Results = append(result.Results, state.RealtimeConsumerGroupBatchSettlementItem{Sequence: item.Sequence, Action: item.Action,
			RealtimeConsumerGroupSettlementResult: state.RealtimeConsumerGroupSettlementResult{Status: "lease_lost"}})
	}
	writeRehearsalIdle(w, http.StatusOK, result)
}

// A heartbeat is acknowledged as live: the worker is up, it just has nothing
// to do. Its ID is stable per group and worker, as production keeps one row.
func serveRealtimeGroupHeartbeat(w http.ResponseWriter, r *http.Request, call rehearsalPlatformCall, address realtimeGroupAddress, group state.ScenarioRealtimeGroup) {
	var req state.RealtimeWorkerHeartbeatRequest
	if !decodeRealtimeGroupBody(w, r, call, api.RealtimeConsumerGroupBodyMaxBytes, &req) {
		api.WriteProblem(w, api.ErrRealtimeInvalid("invalid worker heartbeat"))
		return
	}
	req.Worker = realtimeGroupWorker(address, req.Worker)
	if !state.ValidRealtimeWorkerHeartbeat(req) {
		api.WriteProblem(w, realtimeGroupInvalid())
		return
	}
	writeRehearsalIdle(w, http.StatusOK, state.RealtimeWorkerHeartbeat{ID: uuid.NewSHA1(uuid.NameSpaceOID, []byte(group.GroupID+"\x00"+req.Worker)).String(),
		Worker: req.Worker, Ready: *req.Ready, StatusCode: req.StatusCode, LastSeenAt: call.now, Live: true})
}
