package mcpmanagement

import (
	"context"
	"reflect"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/toolinvocation"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

func (m *management) invocationAccess() error {
	if !m.remote() {
		return m.toolRegistryAccess()
	}
	if !m.opts.AllowToolInvocation {
		return invalid("invocation_disabled", "this connection has not approved hosted tool invocation")
	}
	return m.toolDiscoveryAccess()
}

func (m *management) invocationWritesAllowed() bool {
	if m.remote() {
		return m.opts.AllowToolInvocation
	}
	return m.opts.AllowWrites
}

func (m *management) invokeDelegatedTool(ctx context.Context, a invokeToolArgs) (any, error) {
	if err := m.invocationAccess(); err != nil {
		return nil, err
	}
	if !a.AllowToolExecution || (a.AccessMode == "open" && m.opts.LocalCLI == nil) || (a.AccessMode != "open" && a.AccessMode != "external-oauth") || a.TokenEnv != "" || !toolinvocation.ValidID(a.GrantID) || !toolinvocation.ValidID(a.RequestID) {
		return nil, invalid("invalid_argument", "hosted invocation requires an exact grant, explicit execution consent and supported access without a token reference")
	}
	if a.AccessMode == "external-oauth" && (!m.opts.AllowPrivateToolInvocation || !toolpublication.ValidID(a.CredentialBinding)) || a.AccessMode == "open" && a.CredentialBinding != "" {
		return nil, invalid("private_invocation_disabled", "private calls require independent consent and an exact owner credential binding")
	}
	if a.TimeoutSeconds == 0 {
		a.TimeoutSeconds = api.ToolInvocationDefaultTimeout
	}
	args, err := toolinvocation.CanonicalArguments([]byte(a.ArgumentsJSON))
	if err != nil {
		return nil, invalid("invalid_argument", "provide a bounded JSON argument object")
	}
	in := api.ToolInvocationIntent{Version: a.Version, Name: a.Name, ArgumentsSHA256: toolpublication.Digest(args), AccessMode: a.AccessMode, CredentialBindingSHA256: a.CredentialBinding, TimeoutSeconds: a.TimeoutSeconds, AllowToolExecution: true}
	if toolinvocation.ValidateIntent(in) != nil {
		return nil, invalid("invalid_argument", "provide an exact tool version and bounded timeout")
	}
	account, err := m.client.Whoami(ctx)
	if err != nil {
		return nil, err
	}
	if old, readErr := m.client.GetDelegatedToolInvocation(ctx, a.RequestID); readErr == nil {
		if toolinvocation.ValidateRecord(old, account.ID, a.RequestID) != nil || old.Delegation == nil || old.Delegation.GrantID != a.GrantID || !reflect.DeepEqual(old.Intent, in) {
			return nil, invalid("invocation_changed", "request identity already names another invocation")
		}
		if !m.registryAppAllowed(old.Target.App, old.Target.AppID) {
			return nil, invalid("app_access_denied", "invocation is outside this connection's consent")
		}
		return invocationView(old, 0)
	} else if !notFound(readErr) {
		return nil, readErr
	}
	p, err := m.client.GetRegistryTool(ctx, a.Version, a.Name)
	if err != nil {
		return nil, err
	}
	if err = validateScopedRegistryTool(p, account.ID, describeToolArgs{Version: a.Version, Name: a.Name}); err != nil {
		return nil, err
	}
	if !m.registryAppAllowed(p.App, p.AppID) {
		return nil, invalid("app_access_denied", "tool is outside this connection's app consent")
	}
	allowed := false
	for _, g := range p.Grants {
		allowed = allowed || g.GrantID == a.GrantID && g.AllowInvocation && g.CredentialBindingSHA256 == a.CredentialBinding && time.Now().Before(g.ExpiresAt)
	}
	if !allowed || p.AccessMode != a.AccessMode {
		return nil, invalid("invocation_access_denied", "this exact active grant does not authorize the requested tool invocation")
	}
	if a.AccessMode == "external-oauth" {
		begin, err := m.client.ExecutePrivateToolInvocation(ctx, a.RequestID, api.PrivateToolInvocationRequest{GrantID: a.GrantID, Intent: in, ArgumentsJSON: string(args)})
		if err != nil {
			return nil, err
		}
		if err = validateDelegatedBegin(begin, account.ID, a.RequestID, a.GrantID, in, p); err != nil {
			return nil, err
		}
		return invocationView(begin.Record, 0)
	}
	workCtx, release, err := m.delegatedInvocationLease(ctx, a.TimeoutSeconds)
	if err != nil {
		return nil, err
	}
	begin, err := m.client.BeginDelegatedToolInvocation(ctx, a.RequestID, api.DelegatedToolInvocationIntent{GrantID: a.GrantID, Intent: in})
	if err == nil {
		err = validateDelegatedBegin(begin, account.ID, a.RequestID, a.GrantID, in, p)
	}
	if err != nil || !begin.Created || begin.Record.Status != "queued" {
		release()
		if err != nil {
			return nil, err
		}
		return invocationView(begin.Record, 0)
	}
	go func() {
		defer release()
		_, _ = m.opts.LocalCLI(workCtx, []string{"mcp", "invocation-run", "--delegated", "--id", a.RequestID}, args)
	}()
	return invocationView(begin.Record, 0)
}

func (m *management) delegatedInvocationLease(ctx context.Context, timeout int) (context.Context, func(), error) {
	select {
	case toolInvocationSlots <- struct{}{}:
	default:
		return nil, nil, invalid("invocation_capacity", "hosted workers are busy; retain this request identity")
	}
	work, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Duration(timeout)*time.Second+api.ToolInvocationReportGrace)
	work, finish, err := m.lifecycle.enter(work, false)
	if err != nil {
		cancel()
		<-toolInvocationSlots
		return nil, nil, err
	}
	work, finishGrant, err := m.grantLifecycle.enter(work, false)
	if err != nil {
		finish()
		cancel()
		<-toolInvocationSlots
		return nil, nil, err
	}
	return work, func() { finishGrant(); finish(); cancel(); <-toolInvocationSlots }, nil
}

func validateDelegatedBegin(begin api.ToolInvocationReceipt, account, id, grant string, in api.ToolInvocationIntent, p api.ToolRegistryTool) error {
	if toolinvocation.ValidateRecord(begin.Record, account, id) != nil || !reflect.DeepEqual(begin.Record.Intent, in) || begin.Record.Delegation == nil || begin.Record.Delegation.GrantID != grant || begin.Record.Target.AppID != p.AppID || begin.Record.Target.App != p.App || begin.Record.Target.DeploymentID != p.DeploymentID || begin.Record.Target.CatalogContractSHA256 != p.PublishedCatalogContractSHA256 || begin.Record.Target.CatalogSHA256 != p.CatalogSHA256 || begin.Record.Target.DeploymentSourceSHA256 != p.DeploymentSourceSHA256 {
		return invalid("invalid_response", "native invocation differs from the exact reviewed tool and grant")
	}
	return nil
}
