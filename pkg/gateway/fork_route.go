package gateway

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// forkTargetResolver resolves a production fork's instance for a request
// that carries the fork id and access token (ADR-732). found is false for an
// unknown fork, a wrong token, or a fork that is not running; the three are
// indistinguishable to the caller so the route is not an oracle.
type forkTargetResolver interface {
	ResolveForkTarget(ctx context.Context, appID, forkID, token string) (target Target, found bool, err error)
}

// CodeForkNotFound is returned for every fork request the gateway cannot
// route: unknown fork, wrong token, or a fork that is not running.
const CodeForkNotFound = "fork_not_found"

func forkRequested(r *http.Request) bool {
	return r.Header.Get(api.ForkHeader) != "" || r.Header.Get(api.ForkTokenHeader) != ""
}

// serveFork routes one request to a production fork. It never wakes,
// queues, caches, retries or applies edge rules: a fork is a fixed copy
// that either runs or does not. The fork headers are removed before the
// request reaches the guest.
func (h *Handler) serveFork(w http.ResponseWriter, r *http.Request, app App) {
	forkID := strings.TrimSpace(r.Header.Get(api.ForkHeader))
	token := strings.TrimSpace(r.Header.Get(api.ForkTokenHeader))
	r.Header.Del(api.ForkHeader)
	r.Header.Del(api.ForkTokenHeader)

	notFound := api.NewProblem(http.StatusNotFound, CodeForkNotFound, "No such fork",
		"no running fork matches this id and token").WithDocs("https://gregale.dev/docs/forks#access")
	resolver, ok := h.backend.(forkTargetResolver)
	if !ok || forkID == "" || token == "" {
		api.WriteProblem(w, notFound)
		return
	}
	target, found, err := resolver.ResolveForkTarget(r.Context(), app.ID, forkID, token)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, api.CodeCapacity,
			"Fork lookup unavailable", "the gateway could not look up the fork"))
		return
	}
	if !found {
		api.WriteProblem(w, notFound)
		return
	}

	identity := target.PlatformIdentity(app.AccountID, requestIDFrom(r))
	if identity.AppID == "" {
		identity.AppID = app.ID
	}
	identity.ApplyGuestHeaders(r.Header)
	r = r.WithContext(wire.WithPlatformIdentity(r.Context(), identity))
	stampTrustedClientIP(r)
	r.Header.Set("x-faas-protocol", decideProtocol(app))
	w.Header().Set("X-Gregale-Fork-Served", "1")
	if h.proxyByNode != nil {
		h.proxyByNode(target).ServeHTTP(w, r)
		return
	}
	h.proxyFor(target.NodeID, app.Plan.MaxResponseBodyBytes()).ServeHTTP(w, r)
}

// ForkTargetFromState decides whether a fork request may be routed and to
// which instance (ADR-732). It requires the fork to be running and
// unexpired, the token to hash to the stored value (constant time), and the
// recorded instance to be a running fork of the same app on a known node.
func ForkTargetFromState(fork state.AppFork, instance state.Instance, port int, token string, now time.Time) (Target, bool) {
	if fork.Status != state.AppForkRunning || !fork.ExpiresAt.After(now) || fork.InstanceID == nil ||
		len(fork.AccessTokenHash) != sha256.Size || token == "" {
		return Target{}, false
	}
	if subtle.ConstantTimeCompare(api.AppForkAccessTokenHash(token), fork.AccessTokenHash) != 1 {
		return Target{}, false
	}
	if instance.ID != *fork.InstanceID || instance.AppID != fork.AppID || !state.IsFork(instance.Mode) ||
		instance.State != string(state.StateRunning) || instance.NodeID == "" {
		return Target{}, false
	}
	return Target{
		AppID: fork.AppID, InstanceID: instance.ID, NodeID: instance.NodeID,
		WakeID: instance.WakeID, DeploymentID: instance.DeploymentID, Port: port,
	}, true
}
