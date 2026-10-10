package gateway

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/routeprobe"
)

// Route probes (ADR-954) are a separate challenge kind from the hosting smoke.
// A probe token pins one live deployment of one app; unlike the smoke it does
// not bypass customer auth gates, and the request writes no telemetry or usage.

type routeProbeValidator interface {
	ValidateRouteProbe(appID, deploymentID, token string) bool
}

type routeProbeKey struct{}

type routeProbeRequest struct{ deploymentID, token string }

func withRouteProbe(ctx context.Context, deploymentID, token string) context.Context {
	return context.WithValue(ctx, routeProbeKey{}, routeProbeRequest{deploymentID, token})
}

// isRouteProbe reports whether the request is an authenticated route probe.
func isRouteProbe(ctx context.Context) bool {
	probe, ok := ctx.Value(routeProbeKey{}).(routeProbeRequest)
	return ok && probe.deploymentID != ""
}

// authorizedRouteProbe validates and strips the probe headers. Headers that
// fail validation are stripped too, so they never reach the customer app.
// present reports whether any probe header was sent: the handler refuses an
// unauthorized probe instead of serving it as customer traffic, so a probe
// that races its challenge delivery never reaches telemetry or usage.
func (h *Handler) authorizedRouteProbe(r *http.Request, app App) (deploymentID, token string, ok, present bool) {
	deploymentID = strings.TrimSpace(r.Header.Get(routeprobe.DeploymentHeader))
	token = strings.TrimSpace(r.Header.Get(routeprobe.TokenHeader))
	present = r.Header.Get(routeprobe.DeploymentHeader) != "" || r.Header.Get(routeprobe.TokenHeader) != ""
	r.Header.Del(routeprobe.DeploymentHeader)
	r.Header.Del(routeprobe.TokenHeader)
	if h == nil || h.backend == nil || deploymentID == "" || token == "" {
		return "", "", false, present
	}
	validator, valid := h.backend.(routeProbeValidator)
	if !valid || !validator.ValidateRouteProbe(app.ID, deploymentID, token) {
		return "", "", false, present
	}
	return deploymentID, token, true, present
}

// AuthorizeRouteProbe installs a short-lived probe challenge delivered over
// the private notification channel. Probe tokens live in their own map so a
// probe token can never authorize the hosting smoke bypass.
func (b *PGBackend) AuthorizeRouteProbe(appID, deploymentID, token string, expiresAt time.Time) {
	if b == nil || appID == "" || deploymentID == "" || token == "" || !expiresAt.After(time.Now()) {
		return
	}
	b.probeMu.Lock()
	defer b.probeMu.Unlock()
	if b.probeChallenges == nil {
		b.probeChallenges = map[string][]deploymentSmokeChallenge{}
	}
	now := time.Now()
	for key, challenges := range b.probeChallenges {
		live := challenges[:0]
		for _, c := range challenges {
			if c.expiresAt.After(now) {
				live = append(live, c)
			}
		}
		if len(live) == 0 {
			delete(b.probeChallenges, key)
		} else {
			b.probeChallenges[key] = live
		}
	}
	key := smokeChallengeKey(appID, deploymentID)
	b.probeChallenges[key] = append(b.probeChallenges[key], deploymentSmokeChallenge{token: token, expiresAt: expiresAt})
}

// ValidateRouteProbe authenticates a probe token bound to app and deployment.
func (b *PGBackend) ValidateRouteProbe(appID, deploymentID, token string) bool {
	if b == nil || token == "" {
		return false
	}
	b.probeMu.Lock()
	defer b.probeMu.Unlock()
	key := smokeChallengeKey(appID, deploymentID)
	now := time.Now()
	matched := 0
	live := b.probeChallenges[key][:0]
	for _, c := range b.probeChallenges[key] {
		if !c.expiresAt.After(now) {
			continue
		}
		live = append(live, c)
		matched |= subtle.ConstantTimeCompare([]byte(c.token), []byte(token))
	}
	if len(live) == 0 {
		delete(b.probeChallenges, key)
	} else {
		b.probeChallenges[key] = live
	}
	return matched == 1
}
