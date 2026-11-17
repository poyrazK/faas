package mcphosting

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/toolinvocation"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

// RegisteredInvocationNative is the exact read/journal surface needed by the
// worker. The private APID adapter never exports its application credential.
type RegisteredInvocationNative interface {
	Token() string
	Whoami(context.Context) (api.AccountResponse, error)
	GetToolInvocation(context.Context, string) (api.ToolInvocationRecord, error)
	GetDelegatedToolInvocation(context.Context, string) (api.ToolInvocationRecord, error)
	GetToolPublication(context.Context, string) (api.ToolPublicationRecord, error)
	GetRegistryTool(context.Context, string, string) (api.ToolRegistryTool, error)
	GetApp(context.Context, string) (api.AppResponse, error)
	GetDeployment(context.Context, string) (api.DeploymentResponse, error)
	GetDeploymentURL(context.Context, string) (api.DeploymentPreviewURL, error)
	DispatchToolInvocation(context.Context, string) (api.ToolInvocationReceipt, error)
	DispatchDelegatedToolInvocation(context.Context, string) (api.ToolInvocationReceipt, error)
	FinishToolInvocation(context.Context, string, api.ToolInvocationOutcome) (api.ToolInvocationReceipt, error)
	FinishDelegatedToolInvocation(context.Context, string, api.ToolInvocationOutcome) (api.ToolInvocationReceipt, error)
}

func validDelegatedCredential(o RegisteredInvocationOptions, r api.ToolInvocationRecord) bool {
	if o.TokenEnv != "" {
		return false
	}
	if o.ManagedCredentialBinding != "" {
		return r.Intent.AccessMode == "external-oauth" && o.ManagedCredentialBinding == r.Intent.CredentialBindingSHA256 && toolpublication.ValidID(o.ManagedCredentialBinding)
	}
	return r.Intent.AccessMode == "open" && o.Token == ""
}

func validRegisteredCredentialBinding(o RegisteredInvocationOptions, r api.ToolInvocationRecord) bool {
	if o.ManagedCredentialBinding != "" {
		return o.Delegated && validDelegatedCredential(o, r)
	}
	return toolinvocation.ValidTokenEnv(r.Intent.AccessMode, o.TokenEnv) && toolinvocation.CredentialBinding(o.TokenEnv) == r.Intent.CredentialBindingSHA256
}
