package mcpmanagement

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

type residencyArgs struct {
	App   string `json:"app" jsonschema:"Existing app slug with app-wide diagnostic consent"`
	Since string `json:"since,omitempty" jsonschema:"Positive lookback duration or whole days; native default 24h and plan retention cap apply"`
	Limit int    `json:"limit,omitempty" jsonschema:"Maximum retained observations from 1 to 20; default 10"`
}

func registerResidencyDiagnostics(s *mcp.Server, m *management) {
	register(s, m, "gregale_get_running_causes", "Inspect retained scheduler reasons an app stayed resident, floor/idle configuration and bounded history. Remote access requires app-wide diagnostic consent. The native current field is a retained observation while resident, not a live blocker measurement; freshness is unknown. Request matches are approximate aggregates. Excludes connection endpoints and instance identities. No savings estimates or configuration changes.", m.getRunningCauses)
	s.AddPrompt(&mcp.Prompt{Name: "gregale_residency_diagnosis", Description: "Read-only diagnosis of why an app stayed resident, with observation age and missing-evidence limits.", Arguments: []*mcp.PromptArgument{{Name: "app", Required: true, Description: "Existing app with diagnostic consent"}, {Name: "since", Description: "Positive lookback duration/day alias; native default 24h and retention cap apply"}}}, m.getResidencyPrompt)
}

func (m *management) getRunningCauses(ctx context.Context, args residencyArgs) (any, error) {
	selection := performanceArgs{App: args.App, Since: args.Since}
	if err := validatePerformanceArgs(selection); err != nil {
		return nil, err
	}
	if args.Limit == 0 {
		args.Limit = api.MCPResidencyHistoryDefault
	}
	if args.Limit < 1 || args.Limit > api.MCPResidencyHistoryMax {
		return nil, invalid("invalid_argument", "limit must be between 1 and 20")
	}
	app, err := m.requestApp(ctx, args.App)
	if err != nil {
		return nil, err
	}
	native, err := m.client.GetAppDebugRunningWithLimit(ctx, args.App, args.Since, args.Limit)
	if err != nil {
		return nil, err
	}
	window, err := validatePerformanceWindow(selection, app.ID, native.AppID, native.Since, native.WindowStart, native.WindowEnd, &native.RetentionClamped)
	if err != nil {
		return nil, err
	}
	view, scopes, err := m.projectResidency(ctx, window, native, args.Limit)
	if err != nil {
		return nil, err
	}
	return &guardedRead{value: view, check: func(ctx context.Context) error {
		return m.recheckRequestPage(ctx, args.App, app.ID, scopes)
	}}, nil
}
