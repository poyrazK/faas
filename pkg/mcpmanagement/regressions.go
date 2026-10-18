package mcpmanagement

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type regressionsArgs struct {
	App   string `json:"app" jsonschema:"Existing app slug with app-wide diagnostic consent"`
	Since string `json:"since,omitempty" jsonschema:"Positive detection lookback duration or whole days; at most 64 bytes. Native default 1h and plan retention cap apply; not a request sampling window."`
	Limit int    `json:"limit,omitempty" jsonschema:"Maximum projected observations from 1 to 50; default 20. Output bound only, not a native query limit or pagination."`
}

func regressionSelection(args regressionsArgs) (int, error) {
	if err := validatePerformanceArgs(performanceArgs{App: args.App, Since: args.Since}); err != nil {
		return 0, err
	}
	limit := args.Limit
	if limit == 0 {
		limit = api.MCPRegressionsDefault
	}
	if limit < 1 || limit > api.MCPRegressionsMax {
		return 0, invalid("invalid_argument", "limit must be between 1 and 50")
	}
	return limit, nil
}

func (m *management) listRegressions(ctx context.Context, args regressionsArgs) (any, error) {
	limit, err := regressionSelection(args)
	if err != nil {
		return nil, err
	}
	app, err := m.requestApp(ctx, args.App)
	if err != nil {
		return nil, err
	}
	native, err := m.client.ListAppDebugRegressions(ctx, args.App, args.Since)
	if err != nil {
		return nil, err
	}
	clamped, err := validateRegressionLookback(args.Since, native.Since)
	if err != nil {
		return nil, err
	}
	scopes := map[string]string{}
	// Only the projected native prefix is released or dereferenced. Native
	// visibility and ordering remain authoritative, including triage state.
	for _, row := range native.Regressions[:min(len(native.Regressions), limit)] {
		id, parseErr := uuid.Parse(row.DeploymentID)
		if parseErr != nil || id == uuid.Nil || id.String() != row.DeploymentID {
			return nil, invalid("invalid_response", "native regression has an invalid deployment identity")
		}
		if err := m.pinRequestDeployment(ctx, row.DeploymentID, app.ID, scopes); err != nil {
			return nil, err
		}
	}
	view, err := m.projectRegressions(args.App, app.ID, native, scopes, limit, clamped)
	if err != nil {
		return nil, err
	}
	return &guardedRead{value: view, check: func(ctx context.Context) error { return m.recheckRequestPage(ctx, args.App, app.ID, scopes) }}, nil
}

func validateRegressionLookback(requestedRaw, effectiveRaw string) (bool, error) {
	requested := time.Hour
	if requestedRaw != "" {
		var err error
		requested, err = requestSince(requestedRaw)
		if err != nil {
			return false, err
		}
	}
	effective, err := requestSince(effectiveRaw)
	if err != nil || effective > requested {
		return false, invalid("invalid_response", "native regression detection lookback is invalid or wider than requested")
	}
	return effective < requested, nil
}
