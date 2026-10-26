package devloop

import (
	"context"
	"fmt"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ValidateGoCommand checks a developer environment's Go command
// (ADR-20261026120412): a `cmd/<name>` directory name, for an app.
func ValidateGoCommand(req api.UpsertDevSessionRequest) *api.Problem {
	switch {
	case req.GoCommand == "":
		return nil
	case req.Type == string(state.AppTypeFunction):
		return api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid go_command",
			"go_command selects a command of a Go app; functions have a handler instead")
	case !api.ValidGoCommand(req.GoCommand):
		return api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid go_command",
			fmt.Sprintf("go_command %q must name a cmd/ directory: letters, digits, dots, hyphens and underscores", req.GoCommand))
	}
	return nil
}

// SaveGoCommand records the Go command the environment's next build
// compiles, as a project service's build_go_command does; empty builds what
// the source builds by default. The returned app carries the stored value.
func SaveGoCommand(ctx context.Context, store state.Store, app state.App, command string) (state.App, error) {
	if app.Manifest.BuildGoCommand == command {
		return app, nil
	}
	manifest := app.Manifest
	manifest.BuildGoCommand = command
	updated, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &manifest})
	if err != nil {
		return app, fmt.Errorf("store developer go command: %w", err)
	}
	return updated, nil
}
