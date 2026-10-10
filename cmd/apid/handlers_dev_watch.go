package main

import (
	"context"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// validateDevWatch checks the shape of a watch-mode request (ADR-970) and
// returns it normalized; nil means watch mode is off.
func validateDevWatch(watch *api.DevWatch) (*api.DevWatch, *api.Problem) {
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

// devWatchUnsupported refuses watch mode for an environment it cannot serve:
// a function, too little RAM, or an app reachable without authentication.
// Development servers expose source maps, error overlays and reload
// endpoints, so they only run behind the developer app's access check.
func devWatchUnsupported(app state.App, watch *api.DevWatch) *api.Problem {
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
		return api.NewProblem(http.StatusUnprocessableEntity, api.CodeDevWatchUnsupported,
			"Watch mode needs an authenticated environment",
			"a development server exposes source maps and error overlays; require authentication on this developer environment, then retry")
	}
	return nil
}

// saveDevWatch records the setting for the next developer build. A store
// without the capability can only accept watch mode off.
func (s *server) saveDevWatch(ctx context.Context, appID string, watch *api.DevWatch) error {
	store, ok := s.store.(state.DevWatchStore)
	if !ok {
		if watch == nil {
			return nil
		}
		return errDevWatchUnavailable
	}
	command := ""
	if watch != nil {
		command = watch.Command
	}
	return store.SetDevWatchCommand(ctx, appID, command)
}

// loadDevWatch reports the stored setting, nil when off or unavailable.
func (s *server) loadDevWatch(ctx context.Context, appID string) *api.DevWatch {
	store, ok := s.store.(state.DevWatchStore)
	if !ok {
		return nil
	}
	command, err := store.DevWatchCommand(ctx, appID)
	if err != nil || command == "" {
		return nil
	}
	return &api.DevWatch{Command: command}
}

type devWatchError string

func (e devWatchError) Error() string { return string(e) }

const errDevWatchUnavailable = devWatchError("developer watch settings are not available on this control plane")
