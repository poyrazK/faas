package gateway

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// FunctionInvocationStore reads a sent invocation and its send record.
type FunctionInvocationStore interface {
	InvocationByID(context.Context, string) (state.Invocation, error)
	state.InvocationSendReader
}

// functionInvocationMaxWait caps ?wait= so one request never holds a
// connection longer than the SDK's own poll.
const functionInvocationMaxWait = 30 * time.Second

// functionInvocationPoll is how often a waiting request rereads the row.
var functionInvocationPoll = 250 * time.Millisecond

// WithFunctionInvocationAPI serves GET /v1/invocations/{id} on the guest
// platform host api.svc.gregale (ADR-20261112160000 §2): a function reads an
// invocation it sent with send(). The caller is resolved from its address,
// as for every service call, and only the invocation's sender may read it;
// anything else is 404, so ids are no oracle. ?wait=<seconds> (at most 30)
// holds the answer until the invocation is terminal. Every other request
// passes through unchanged.
func WithFunctionInvocationAPI(next http.Handler, resolve ServiceProxyCallerResolver, store FunctionInvocationStore) http.Handler {
	if resolve == nil || store == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := functionInvocationRoute(r)
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		caller, err := resolve(r.Context(), r.RemoteAddr)
		if err != nil || caller == "" {
			api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Caller unknown", "invocations are readable only from the function that sent them"))
			return
		}
		send, err := store.InvocationSend(r.Context(), id)
		if err != nil || send.SenderAppID != caller {
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Invocation not found", "no invocation with this id was sent by this function"))
			return
		}
		wait, problem := functionInvocationWait(r)
		if problem != nil {
			api.WriteProblem(w, problem)
			return
		}
		inv, err := waitForInvocation(r.Context(), store, id, wait)
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("invocation unavailable"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(functionInvocationStatus(inv))
	})
}

func functionInvocationRoute(r *http.Request) (string, bool) {
	if r.Method != http.MethodGet {
		return "", false
	}
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if !strings.EqualFold(strings.TrimSuffix(host, "."), api.ScenarioPlatformAPIHost) {
		return "", false
	}
	id, ok := strings.CutPrefix(r.URL.Path, "/v1/invocations/")
	if !ok || id == "" || strings.Contains(id, "/") {
		return "", false
	}
	return id, true
}

func functionInvocationWait(r *http.Request) (time.Duration, *api.Problem) {
	raw := r.URL.Query().Get("wait")
	if raw == "" {
		return 0, nil
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds < 0 {
		return 0, api.ErrValidation("wait must be a whole number of seconds")
	}
	return min(time.Duration(seconds)*time.Second, functionInvocationMaxWait), nil
}

// waitForInvocation returns the invocation once it is terminal or wait has
// passed, whichever is first.
func waitForInvocation(ctx context.Context, store FunctionInvocationStore, id string, wait time.Duration) (state.Invocation, error) {
	deadline := time.Now().Add(wait)
	for {
		inv, err := store.InvocationByID(ctx, id)
		if err != nil || invocationTerminal(inv.State) || !time.Now().Before(deadline) {
			return inv, err
		}
		select {
		case <-ctx.Done():
			return inv, nil
		case <-time.After(functionInvocationPoll):
		}
	}
}

func invocationTerminal(s state.InvocationState) bool {
	switch s {
	case state.InvocationCompleted, state.InvocationFailed, state.InvocationCancelled, state.InvocationSuperseded, state.InvocationDeadLetter:
		return true
	}
	return false
}

func functionInvocationStatus(inv state.Invocation) api.FunctionInvocationStatus {
	out := api.FunctionInvocationStatus{
		ID: inv.ID, State: string(inv.State), Attempts: inv.Attempts, Done: invocationTerminal(inv.State), CompletedAt: inv.CompletedAt,
	}
	if inv.Outcome != nil {
		out.Outcome = string(*inv.Outcome)
	}
	if inv.State == state.InvocationCompleted {
		out.Result = inv.Result
	} else if out.Done {
		out.Error = inv.LastError
	}
	return out
}
