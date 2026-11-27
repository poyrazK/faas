package devloop

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ValidateWatch checks the shape of a watch-mode request (ADR-1170) and
// returns it normalized; nil means watch mode is off.
func ValidateWatch(watch *api.DevWatch) (*api.DevWatch, *api.Problem) {
	if watch == nil {
		return nil, nil
	}
	command := strings.TrimSpace(watch.Command)
	if command == "" || len(command) > api.DevWatchCommandMaxBytes || strings.ContainsAny(command, "\n\r\x00") {
		return nil, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid watch command",
			"watch.command must be one line of at most 512 bytes, for example \"npm run dev\"")
	}
	return &api.DevWatch{Command: command}, nil
}

// ReviveRAMMB is the RAM a developer environment comes back with when a
// session revives its deleted app: at least what a new watch environment
// gets (api.DevWatchRAMMB), so an environment made before that default does
// not come back at 512 MB and swap through every first compile. A larger
// size someone chose is kept, and without watch mode the size is unchanged.
func ReviveRAMMB(current int, limits api.Limits, watch *api.DevWatch) int {
	if watch == nil {
		return current
	}
	return max(current, api.DevWatchRAMMB(limits))
}

// WatchUnsupported refuses watch mode for an environment it cannot serve: a
// function, too little RAM, or an app reachable without authentication.
// Development servers expose source maps, error overlays and reload
// endpoints, so they only run behind the developer app's access check.
func WatchUnsupported(app state.App, plan api.Plan, watch *api.DevWatch) *api.Problem {
	if watch == nil {
		return nil
	}
	if app.Type == state.AppTypeFunction {
		return api.NewProblem(http.StatusUnprocessableEntity, api.CodeDevWatchUnsupported,
			"Watch mode is for apps", "functions run through the platform runner and have no development server")
	}
	if app.RAMMB < api.DevWatchMinRAMMB {
		return api.ErrDevWatchRAM(app.RAMMB)
	}
	if !app.RequireAuthn && (app.PublicAuthMode == "" || app.PublicAuthMode == api.AppPublicAuthModeOpen) {
		detail := "a development server exposes source maps and error overlays, so it only runs behind authentication; require authentication on this developer environment, then retry"
		if !plan.RequireAuthnAllowed() {
			detail = "a development server exposes source maps and error overlays, so it only runs behind authentication, which your plan does not include; a production-shaped developer build (gregale dev --watch=false) works on every plan"
		}
		return api.NewProblem(http.StatusUnprocessableEntity, api.CodeDevWatchUnsupported,
			"Watch mode needs an authenticated environment", detail)
	}
	return nil
}

// ErrWatchUnavailable is returned when watch mode is requested from a store
// without developer watch settings.
var ErrWatchUnavailable = errors.New("developer watch settings are not available on this control plane")

// SaveWatch records the setting for the next developer build, with host,
// the developer app's public hostname, for the development server to accept
// (api.DevWatchHostEnv). A store without the capability can only accept
// watch mode off.
func SaveWatch(ctx context.Context, store state.Store, appID string, watch *api.DevWatch, host string) error {
	watchStore, ok := store.(state.DevWatchStore)
	if !ok {
		if watch == nil {
			return nil
		}
		return ErrWatchUnavailable
	}
	setting := state.DevWatchSetting{}
	if watch != nil {
		setting = state.DevWatchSetting{Command: watch.Command, Host: WatchHost(host)}
	}
	return watchStore.SetDevWatch(ctx, appID, setting)
}

// WatchHost normalizes a public hostname for dev_watch_settings.host; one
// it cannot store is dropped, which only costs the development server's
// host allowance, never the session.
func WatchHost(host string) string {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if !api.ValidDevWatchHost(host) {
		return ""
	}
	return host
}

// LoadWatch reports the stored setting; nil when off or unavailable.
func LoadWatch(ctx context.Context, store state.Store, appID string) *api.DevWatch {
	watchStore, ok := store.(state.DevWatchStore)
	if !ok {
		return nil
	}
	setting, err := watchStore.DevWatch(ctx, appID)
	if err != nil || setting.Command == "" {
		return nil
	}
	return &api.DevWatch{Command: setting.Command}
}
