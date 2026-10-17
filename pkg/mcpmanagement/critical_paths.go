package mcpmanagement

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
)

type criticalPathsArgs struct {
	App   string `json:"app" jsonschema:"Existing app slug with app-wide diagnostic consent"`
	Since string `json:"since,omitempty" jsonschema:"Positive duration or whole days; at most 64 bytes. Native default 24h and plan retention limits apply."`
	Limit int    `json:"limit,omitempty" jsonschema:"Maximum projected path patterns from 1 to 10; default 5. Limits output in native order, not native sampling or query size."`
}

func (m *management) getCriticalPaths(ctx context.Context, args criticalPathsArgs) (any, error) {
	selection := performanceArgs{App: args.App, Since: args.Since}
	if err := validatePerformanceArgs(selection); err != nil {
		return nil, err
	}
	if args.Limit == 0 {
		args.Limit = api.MCPCriticalPathsDefault
	}
	if args.Limit < 1 || args.Limit > api.MCPCriticalPathsMax {
		return nil, invalid("invalid_argument", "limit must be between 1 and 10")
	}
	app, err := m.requestApp(ctx, args.App)
	if err != nil {
		return nil, err
	}
	native, err := m.client.GetAppDebugCriticalPaths(ctx, args.App, args.Since)
	if err != nil {
		return nil, err
	}
	window, err := validatePerformanceWindow(selection, app.ID, native.AppID, native.Since, native.WindowStart, native.WindowEnd, &native.RetentionClamped)
	if err != nil {
		return nil, err
	}
	view, err := m.projectCriticalPaths(window, native, args.Limit)
	if err != nil {
		return nil, err
	}
	return m.performanceResult(selection, app.ID, view), nil
}
