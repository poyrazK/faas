// adr: 20261022155812
package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// pullLeaseBodyMax bounds a lease or settle body; 100 receipts fit easily.
const pullLeaseBodyMax = 64 << 10

// servePullLease answers a candidate pull consumer's lease calls with only its
// qualification attempt's reviewed smoke message. The caller is the observed
// node address; the app and queue in the path only select the lease.
func (p *environmentQualificationServiceProxy) servePullLease(w http.ResponseWriter, r *http.Request, nodeID, hostIP string) {
	store, ok := p.config.Store.(state.EnvironmentQualificationPullSmokeStore)
	app, queue, verb, valid := parseQualificationPullLeasePath(r.URL.Path)
	if !ok || !valid {
		serviceProxyProblem(w, http.StatusForbidden, "private queue lease request is unsupported")
		return
	}
	if r.Method != http.MethodPost {
		serviceProxyProblem(w, http.StatusMethodNotAllowed, "private queue leases accept POST only")
		return
	}
	caller := state.EnvironmentQualificationPullCaller{NodeID: nodeID, HostIP: hostIP, App: app, Queue: queue}
	body := http.MaxBytesReader(w, r.Body, pullLeaseBodyMax)
	if verb == "" {
		var req api.QueueLeaseRequest
		if err := decodePullLeaseBody(body, &req); err != nil || req.WaitSeconds < 0 || req.WaitSeconds > api.QueueLeaseMaxWaitSeconds {
			serviceProxyProblem(w, http.StatusBadRequest, "invalid private queue lease request")
			return
		}
		p.leasePullSmoke(w, r.Context(), store, caller, req)
		return
	}
	var req api.QueueLeaseReceiptsRequest
	if err := decodePullLeaseBody(body, &req); err != nil {
		serviceProxyProblem(w, http.StatusBadRequest, "invalid private queue settle request")
		return
	}
	out, err := store.SettleEnvironmentQualificationPullSmoke(r.Context(), caller, verb, req.Receipts, pullLeaseVisibility(req.VisibilityTimeoutSeconds))
	if err != nil {
		writePullLeaseError(w, err)
		return
	}
	resp := api.QueueLeaseReceiptsResponse{Succeeded: out.Succeeded, Failed: make([]api.QueueLeaseReceiptFailure, 0, len(out.Failed))}
	if resp.Succeeded == nil {
		resp.Succeeded = []string{}
	}
	for _, failure := range out.Failed {
		resp.Failed = append(resp.Failed, api.QueueLeaseReceiptFailure{Receipt: failure.Receipt, Reason: failure.Reason})
	}
	writePullLeaseJSON(w, resp)
}

// leasePullSmoke long-polls like the production lease API: an armed message
// is returned as soon as the store hands it out, otherwise an empty list once
// the requested wait has passed.
func (p *environmentQualificationServiceProxy) leasePullSmoke(w http.ResponseWriter, ctx context.Context, store state.EnvironmentQualificationPullSmokeStore,
	caller state.EnvironmentQualificationPullCaller, req api.QueueLeaseRequest) {
	until := time.Now().Add(time.Duration(req.WaitSeconds) * time.Second)
	visibility := pullLeaseVisibility(req.VisibilityTimeoutSeconds)
	for {
		leases, err := store.LeaseEnvironmentQualificationPullSmoke(ctx, caller, visibility)
		if err != nil {
			writePullLeaseError(w, err)
			return
		}
		if len(leases) > 0 || !time.Now().Before(until) {
			resp := api.QueueLeaseResponse{Messages: make([]api.QueueLeaseMessage, 0, len(leases))}
			for _, lease := range leases {
				resp.Messages = append(resp.Messages, api.QueueLeaseMessage{ID: lease.MessageID, Receipt: lease.Receipt, Payload: lease.Payload,
					Attempt: lease.Attempt, EnqueuedAt: lease.ArmedAt, LeaseExpiresAt: lease.LeaseExpiresAt})
			}
			writePullLeaseJSON(w, resp)
			return
		}
		timer := time.NewTimer(api.EnvironmentGitOpsQualificationPullSmokePoll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return // the consumer went away; an armed message stays unleased
		case <-timer.C:
		}
	}
}

func pullLeaseVisibility(seconds int) time.Duration {
	if seconds == 0 {
		seconds = api.QueueLeaseDefaultVisibilitySeconds
	}
	return time.Duration(seconds) * time.Second
}

func decodePullLeaseBody(body io.Reader, out any) error {
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func writePullLeaseJSON(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(body)
}

func writePullLeaseError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, state.ErrInvalidArgument):
		serviceProxyProblem(w, http.StatusBadRequest, "invalid private queue lease request")
	case errors.Is(err, state.ErrNotFound):
		serviceProxyProblem(w, http.StatusNotFound, "queue has no reviewed pull binding on this candidate")
	case errors.Is(err, state.ErrConflict):
		serviceProxyProblem(w, http.StatusForbidden, "private queue lease authority is unavailable")
	default:
		serviceProxyProblem(w, http.StatusServiceUnavailable, "private queue lease is unavailable")
	}
}

// parseQualificationPullLeasePath accepts exactly the production lease shape
// under the qualification prefix:
// {prefix}/v1/apps/{slug}/queues/{queue}/leases[:ack|:nack|:extend].
func parseQualificationPullLeasePath(path string) (string, string, string, bool) {
	rest, ok := strings.CutPrefix(path, api.EnvironmentQualificationQueuePrefix+"/v1/apps/")
	if !ok || strings.Contains(rest, "%") {
		return "", "", "", false
	}
	parts := strings.Split(rest, "/")
	if len(parts) != 4 || parts[1] != "queues" || !api.ValidAppSlug(parts[0]) || !api.ValidQueueBindingName(parts[2]) {
		return "", "", "", false
	}
	action, verb, settle := strings.Cut(parts[3], ":")
	if action != "leases" {
		return "", "", "", false
	}
	if settle && verb != state.PullSmokeAck && verb != state.PullSmokeNack && verb != state.PullSmokeExtend {
		return "", "", "", false
	}
	return parts[0], parts[2], verb, true
}
