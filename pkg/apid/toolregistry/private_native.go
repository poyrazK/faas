package toolregistry

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// This adapter exposes only the bound invocation's native facts to the shared
// worker. It uses current store authority, not an operator API credential.
type privateNative struct {
	executor *PrivateExecutor
	request  *http.Request
	account  state.Account
	keyID    string
	record   api.ToolInvocationRecord
}

func (n *privateNative) Token() string {
	return strings.TrimPrefix(n.request.Header.Get("Authorization"), "Bearer ")
}
func (n *privateNative) Whoami(context.Context) (api.AccountResponse, error) {
	return api.AccountResponse{ID: n.account.ID}, nil
}
func (n *privateNative) current(ctx context.Context, id string) (api.ToolInvocationRecord, error) {
	if id != n.record.ID {
		return api.ToolInvocationRecord{}, state.ErrNotFound
	}
	account, err := n.executor.store.AccountByID(ctx, n.account.ID)
	if err != nil || !account.Active() {
		return api.ToolInvocationRecord{}, state.ErrNotFound
	}
	req := n.request.WithContext(ctx)
	key, err := grantKey(req, n.account, n.executor.store, n.keyID)
	if err != nil {
		return api.ToolInvocationRecord{}, err
	}
	return delegatedInvocationRecord(req, n.account, n.executor.store, n.executor.store.(state.ToolInvocationStore), key, id)
}
func (n *privateNative) GetDelegatedToolInvocation(ctx context.Context, id string) (api.ToolInvocationRecord, error) {
	return n.current(ctx, id)
}
func (n *privateNative) GetToolInvocation(context.Context, string) (api.ToolInvocationRecord, error) {
	return api.ToolInvocationRecord{}, state.ErrNotFound
}
func (n *privateNative) GetToolPublication(context.Context, string) (api.ToolPublicationRecord, error) {
	return api.ToolPublicationRecord{}, state.ErrNotFound
}
func (n *privateNative) GetRegistryTool(ctx context.Context, version, name string) (api.ToolRegistryTool, error) {
	record, err := n.current(ctx, n.record.ID)
	if err != nil || record.Intent.Version != version || record.Intent.Name != name {
		return api.ToolRegistryTool{}, state.ErrNotFound
	}
	req := n.request.WithContext(ctx)
	key, err := grantKey(req, n.account, n.executor.store, n.keyID)
	if err != nil {
		return api.ToolRegistryTool{}, err
	}
	g, p, err := invocationGrant(req, n.account, n.executor.store, key, record.Delegation.GrantID, record.Intent)
	if err != nil {
		return api.ToolRegistryTool{}, err
	}
	app, err := publicationApp(req, n.account, n.executor.store, p)
	if err != nil {
		return api.ToolRegistryTool{}, err
	}
	out, err := projectTool(p, app, name)
	out.Grants = []api.ToolGrantAccess{grantAccess(g)}
	return out, err
}
func (n *privateNative) GetApp(ctx context.Context, slug string) (api.AppResponse, error) {
	if _, err := n.current(ctx, n.record.ID); err != nil {
		return api.AppResponse{}, err
	}
	app, err := n.executor.store.AppByID(ctx, n.record.Target.AppID)
	if err != nil || app.AccountID != n.account.ID || app.Slug != slug || slug != n.record.Target.App {
		return api.AppResponse{}, state.ErrNotFound
	}
	return api.AppResponse{ID: app.ID, Slug: app.Slug}, nil
}
func (n *privateNative) GetDeployment(ctx context.Context, id string) (api.DeploymentResponse, error) {
	if _, err := n.current(ctx, n.record.ID); err != nil {
		return api.DeploymentResponse{}, err
	}
	dep, err := n.executor.store.DeploymentByID(ctx, id)
	if err != nil || id != n.record.Target.DeploymentID || dep.AppID != n.record.Target.AppID {
		return api.DeploymentResponse{}, state.ErrNotFound
	}
	return api.DeploymentResponse{ID: dep.ID, AppID: dep.AppID, Status: string(dep.Status), Scope: dep.Scope, SourceSHA256: dep.SourceSHA256}, nil
}
func (n *privateNative) GetDeploymentURL(ctx context.Context, id string) (api.DeploymentPreviewURL, error) {
	if _, err := n.GetDeployment(ctx, id); err != nil {
		return api.DeploymentPreviewURL{}, err
	}
	dep, err := n.executor.store.DeploymentByID(ctx, id)
	if err != nil {
		return api.DeploymentPreviewURL{}, err
	}
	app, err := n.executor.store.AppByID(ctx, dep.AppID)
	if err != nil {
		return api.DeploymentPreviewURL{}, err
	}
	endpoint, err := n.executor.opts.Endpoint(ctx, app, dep)
	if err != nil {
		return api.DeploymentPreviewURL{}, err
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return api.DeploymentPreviewURL{}, err
	}
	return api.DeploymentPreviewURL{AppID: app.ID, DeploymentID: dep.ID, Host: parsed.Host, URL: endpoint, Alive: true}, nil
}
func (n *privateNative) change(ctx context.Context, id, action string, out *api.ToolInvocationOutcome) (api.ToolInvocationReceipt, error) {
	record, err := n.current(ctx, id)
	if err != nil {
		return api.ToolInvocationReceipt{}, err
	}
	if action == "dispatch" {
		if err := availableInvocationTarget(n.request.WithContext(ctx), n.account, n.executor.store, record.Target); err != nil {
			return api.ToolInvocationReceipt{}, err
		}
	}
	return n.executor.store.(state.DelegatedToolInvocationStore).ChangeDelegatedToolInvocation(ctx, n.account.ID, id, *record.Delegation, action, out)
}
func (n *privateNative) DispatchDelegatedToolInvocation(ctx context.Context, id string) (api.ToolInvocationReceipt, error) {
	return n.change(ctx, id, "dispatch", nil)
}
func (n *privateNative) FinishDelegatedToolInvocation(ctx context.Context, id string, out api.ToolInvocationOutcome) (api.ToolInvocationReceipt, error) {
	return n.change(ctx, id, "finish", &out)
}
func (n *privateNative) DispatchToolInvocation(context.Context, string) (api.ToolInvocationReceipt, error) {
	return api.ToolInvocationReceipt{}, state.ErrNotFound
}
func (n *privateNative) FinishToolInvocation(context.Context, string, api.ToolInvocationOutcome) (api.ToolInvocationReceipt, error) {
	return api.ToolInvocationReceipt{}, state.ErrNotFound
}
