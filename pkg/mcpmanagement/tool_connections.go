package mcpmanagement

import (
	"context"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/toolgrant"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

type toolConnectionArgs struct {
	GrantID string `json:"grant_id" jsonschema:"Exact owner-issued private invocation grant UUID"`
}

func (m *management) getToolConnection(ctx context.Context, a toolConnectionArgs) (any, error) {
	if err := m.invocationAccess(); err != nil {
		return api.ToolOAuthConnectionStatus{}, err
	}
	if !m.remote() || !m.opts.AllowPrivateToolInvocation || !toolgrant.ValidID(a.GrantID) {
		return api.ToolOAuthConnectionStatus{}, invalid("private_invocation_disabled", "an exact private invocation grant and independent consent are required")
	}
	out, err := m.client.GetDelegatedToolConnection(ctx, a.GrantID)
	if err != nil {
		return out, err
	}
	if out.GrantID != a.GrantID || !api.ValidAppSlug(out.App) || !toolgrant.ValidResourceID(out.AppID) || !toolpublication.ValidID(out.Version) || !toolgrant.ValidName(out.Name) || !slices.Contains([]string{"ready", "refresh_required", "refreshing", "reconnect_required"}, out.State) || out.RecoveryAction != "" && out.RecoveryAction != "reconnect" && out.RecoveryAction != "review_connection" {
		return api.ToolOAuthConnectionStatus{}, invalid("invalid_response", "unbound connection status")
	}
	if !m.registryAppAllowed(out.App, out.AppID) {
		return api.ToolOAuthConnectionStatus{}, invalid("app_access_denied", "connection is outside this connection's app consent")
	}
	return &guardedRead{value: out, check: func(ctx context.Context) error {
		current, err := m.client.GetDelegatedToolConnection(ctx, a.GrantID)
		if err != nil {
			return err
		}
		if current.GrantID != out.GrantID || current.App != out.App || current.AppID != out.AppID || current.Version != out.Version || current.Name != out.Name || !m.registryAppAllowed(current.App, current.AppID) {
			return invalid("app_access_denied", "connection authorization changed while reading status")
		}
		return nil
	}}, nil
}
