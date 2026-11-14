package mcphosting

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/toolinvocation"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

// RegisteredInvocationOptions is used by the trusted local CLI worker. It
// accepts no caller-supplied URL and never forwards the native API credential.
type RegisteredInvocationOptions struct {
	Delegated                bool
	Native                   RegisteredInvocationNative
	ManagedCredentialBinding string
	ID                       string
	Arguments                []byte
	TokenEnv                 string
	Token                    string
	SensitiveValues          []string
	VerifyPin                func(context.Context, api.ToolInvocationTarget, string) error
	// HTTP is optional transport injection for portable fixtures. Production
	// leaves it nil and retains the MCP client's no-redirect policy.
	HTTP *http.Client
}

func RunRegisteredInvocation(ctx context.Context, o RegisteredInvocationOptions) (api.ToolInvocationRecord, error) {
	var record api.ToolInvocationRecord
	if o.Native == nil || !toolinvocation.ValidID(o.ID) {
		return record, toolinvocation.ErrInvalid
	}
	account, err := o.Native.Whoami(ctx)
	if err != nil {
		return record, err
	}
	record, err = o.getInvocation(ctx)
	if err != nil {
		return record, err
	}
	if toolinvocation.ValidateRecord(record, account.ID, o.ID) != nil || (record.Delegation != nil) != o.Delegated || o.Delegated && !validDelegatedCredential(o, record) {
		return record, toolinvocation.ErrInvalid
	}
	args, err := toolinvocation.CanonicalArguments(o.Arguments)
	if err != nil || toolpublication.Digest(args) != record.Intent.ArgumentsSHA256 || !validRegisteredCredentialBinding(o, record) {
		return record, toolinvocation.ErrInvalid
	}
	if record.Status != "queued" {
		return record, nil
	}
	if o.Token == o.Native.Token() && o.Token != "" || isNativeInvocationCredential(o.Token) || record.Intent.AccessMode == "open" && o.Token != "" || record.Intent.AccessMode == "external-oauth" && o.Token == "" {
		return finishRegisteredInvocation(ctx, o, record, api.ToolInvocationOutcome{Status: "not_started", Code: "preflight_failed"})
	}
	workCtx, cancel := context.WithDeadline(ctx, record.CreatedAt.Add(time.Duration(record.Intent.TimeoutSeconds)*time.Second))
	defer cancel()
	stop := watchInvocationCancellation(workCtx, o, cancel)
	defer stop()
	client, tool, arguments, err := prepareRegisteredInvocation(workCtx, o, record, args)
	if err != nil {
		return finishRegisteredInvocation(ctx, o, record, api.ToolInvocationOutcome{Status: "not_started", Code: "preflight_failed"})
	}
	var grant api.ToolInvocationReceipt
	if o.Delegated {
		grant, err = o.Native.DispatchDelegatedToolInvocation(workCtx, record.ID)
	} else {
		grant, err = o.Native.DispatchToolInvocation(workCtx, record.ID)
	}
	if err != nil {
		return record, err
	} // A lost dispatch receipt is never retried as execution.
	if toolinvocation.ValidateRecord(grant.Record, account.ID, record.ID) != nil || !sameInvocationBinding(record, grant.Record) {
		return record, toolinvocation.ErrInvalid
	}
	if !grant.DispatchGranted {
		return grant.Record, nil
	}
	if grant.Record.Status != "running" || grant.Record.DispatchedAt == nil {
		return record, toolinvocation.ErrInvalid
	}
	// No Tasks, input responder, transport retry, or automatic resume. This is
	// the only effectful protocol request in this worker.
	x, callErr := client.Call(workCtx, tool, arguments, true)
	redact := append([]string{o.Native.Token(), o.Token}, o.SensitiveValues...)
	out, resultErr := toolinvocation.Result(x.Result, redact...)
	if resultErr != nil || callErr != nil && out.Status != "tool_error" {
		code := "transport_error"
		if errors.Is(workCtx.Err(), context.DeadlineExceeded) {
			code = "timeout"
		}
		if errors.Is(workCtx.Err(), context.Canceled) {
			code = "cancel_requested"
		}
		out = api.ToolInvocationOutcome{Status: "outcome_unknown", Code: code}
	}
	return finishRegisteredInvocation(ctx, o, grant.Record, out)
}

func isNativeInvocationCredential(token string) bool {
	for _, prefix := range []string{"fp_", "gmcp_"} {
		if strings.HasPrefix(token, prefix) {
			return true
		}
	}
	return false
}

func sameInvocationBinding(a, b api.ToolInvocationRecord) bool {
	x, _ := json.Marshal(a.Intent)
	y, _ := json.Marshal(b.Intent)
	u, _ := json.Marshal(a.Target)
	v, _ := json.Marshal(b.Target)
	d, _ := json.Marshal(a.Delegation)
	e, _ := json.Marshal(b.Delegation)
	return bytes.Equal(x, y) && bytes.Equal(u, v) && bytes.Equal(d, e)
}

func (o RegisteredInvocationOptions) getInvocation(ctx context.Context) (api.ToolInvocationRecord, error) {
	if o.Delegated {
		return o.Native.GetDelegatedToolInvocation(ctx, o.ID)
	}
	return o.Native.GetToolInvocation(ctx, o.ID)
}

func finishRegisteredInvocation(ctx context.Context, o RegisteredInvocationOptions, record api.ToolInvocationRecord, out api.ToolInvocationOutcome) (api.ToolInvocationRecord, error) {
	save, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	var result api.ToolInvocationReceipt
	var err error
	if o.Delegated {
		result, err = o.Native.FinishDelegatedToolInvocation(save, record.ID, out)
	} else {
		result, err = o.Native.FinishToolInvocation(save, record.ID, out)
	}
	if err != nil {
		// Cancellation may have won before dispatch, or the deadline may have
		// expired. Read the retained fact; do not turn it into another call.
		current, readErr := o.getInvocation(save)
		if readErr == nil && toolinvocation.ValidateRecord(current, record.AccountID, record.ID) == nil && sameInvocationBinding(record, current) && toolinvocation.Terminal(current.Status) {
			return current, nil
		}
		return record, err
	}
	if toolinvocation.ValidateRecord(result.Record, record.AccountID, record.ID) != nil || !sameInvocationBinding(record, result.Record) {
		return record, toolinvocation.ErrInvalid
	}
	return result.Record, nil
}

func watchInvocationCancellation(ctx context.Context, o RegisteredInvocationOptions, cancel context.CancelFunc) func() {
	pollCtx, stop := context.WithCancel(ctx)
	var done sync.WaitGroup
	done.Go(func() {
		ticker := time.NewTicker(api.ToolInvocationCancelPoll)
		defer ticker.Stop()
		for {
			select {
			case <-pollCtx.Done():
				return
			case <-ticker.C:
				record, err := o.getInvocation(pollCtx)
				if err != nil {
					cancel()
					return
				} // Revocation or lost authority stops the local wait.
				if record.CancellationRequested || toolinvocation.Terminal(record.Status) {
					cancel()
					return
				}
			}
		}
	})
	return func() { stop(); done.Wait() }
}

func prepareRegisteredInvocation(ctx context.Context, o RegisteredInvocationOptions, record api.ToolInvocationRecord, args []byte) (*Client, Tool, map[string]any, error) {
	var tool Tool
	expected, err := registeredInvocationContract(ctx, o, record)
	if err != nil {
		return nil, tool, nil, err
	}
	app, err := o.Native.GetApp(ctx, record.Target.App)
	if err != nil || app.ID != record.Target.AppID || app.Slug != record.Target.App {
		return nil, tool, nil, toolinvocation.ErrInvalid
	}
	dep, err := o.Native.GetDeployment(ctx, record.Target.DeploymentID)
	if err != nil || dep.ID != record.Target.DeploymentID || dep.AppID != app.ID || dep.Status != "live" || dep.SourceSHA256 != record.Target.DeploymentSourceSHA256 || dep.Scope != "" && dep.Scope != api.DefaultEnvScope {
		return nil, tool, nil, toolinvocation.ErrInvalid
	}
	preview, err := o.Native.GetDeploymentURL(ctx, dep.ID)
	if err != nil || !preview.Alive || preview.DeploymentID != dep.ID || preview.AppID != app.ID {
		return nil, tool, nil, toolinvocation.ErrInvalid
	}
	u, err := url.Parse(preview.URL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.Host != preview.Host || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" || !toolpublication.ValidEndpointPath(record.Target.EndpointPath) {
		return nil, tool, nil, toolinvocation.ErrInvalid
	}
	u.Path = record.Target.EndpointPath
	client, err := NewClient(u.String(), o.Token, ProtocolVersion)
	if err != nil {
		return nil, tool, nil, err
	}
	if o.HTTP != nil {
		copy := *o.HTTP
		copy.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
		client.HTTP = &copy
	}
	client.HTTP.Timeout = time.Duration(record.Intent.TimeoutSeconds) * time.Second
	catalog, _, err := client.DiscoverCatalog(ctx)
	if err != nil {
		return nil, tool, nil, err
	}
	contract, err := NewCatalogContract(client.Version, catalog)
	if err != nil {
		return nil, tool, nil, err
	}
	canonical, err := MarshalContract(contract)
	if err != nil || validateInvocationCatalog(o, record, contract, canonical, expected) != nil {
		return nil, tool, nil, toolinvocation.ErrInvalid
	}
	if o.VerifyPin == nil || o.VerifyPin(ctx, record.Target, client.Endpoint) != nil {
		return nil, tool, nil, toolinvocation.ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(args))
	decoder.UseNumber()
	var arguments map[string]any
	if decoder.Decode(&arguments) != nil {
		return nil, tool, nil, toolinvocation.ErrInvalid
	}
	for _, candidate := range catalog.Tools {
		if candidate.Name == record.Intent.Name {
			if validateRegisteredArguments(candidate.InputSchema, arguments) != nil {
				return nil, tool, nil, toolinvocation.ErrInvalid
			}
			return client, candidate, arguments, nil
		}
	}
	return nil, tool, nil, toolinvocation.ErrInvalid
}

func registeredInvocationContract(ctx context.Context, o RegisteredInvocationOptions, r api.ToolInvocationRecord) ([]byte, error) {
	if o.Delegated {
		p, err := o.Native.GetRegistryTool(ctx, r.Intent.Version, r.Intent.Name)
		if err != nil {
			return nil, err
		}
		if p.Version != r.Intent.Version || p.Name != r.Intent.Name || p.AccountID != r.AccountID || p.AppID != r.Target.AppID || p.App != r.Target.App || p.DeploymentID != r.Target.DeploymentID || p.AccessMode != r.Intent.AccessMode ||
			!toolpublication.ValidID(p.SourceSHA256) || p.DeploymentSourceSHA256 != r.Target.DeploymentSourceSHA256 || p.CatalogSHA256 != r.Target.CatalogSHA256 || p.PublishedCatalogContractSHA256 != r.Target.CatalogContractSHA256 || toolpublication.Digest(p.Catalog) != p.CatalogContractSHA256 || len(p.Catalog) > api.ToolPublicationCatalogMaxBytes || len(p.Grants) > api.ToolGrantsPerKey {
			return nil, toolinvocation.ErrInvalid
		}
		for _, g := range p.Grants {
			if g.GrantID == r.Delegation.GrantID && g.CredentialBindingSHA256 == r.Intent.CredentialBindingSHA256 && g.AllowInvocation && time.Now().Before(g.ExpiresAt) {
				return p.Catalog, nil
			}
		}
		return nil, toolinvocation.ErrInvalid
	}
	p, err := o.Native.GetToolPublication(ctx, r.Intent.Version)
	if err != nil {
		return nil, err
	}
	if p.ID != r.Intent.Version || p.AccountID != r.AccountID || p.Status != "completed" || p.Outcome == nil || p.Intent.AccessMode != r.Intent.AccessMode || p.Outcome.DeploymentID != r.Target.DeploymentID ||
		p.DeploymentSourceSHA256 != r.Target.DeploymentSourceSHA256 || p.Outcome.CatalogSHA256 != r.Target.CatalogSHA256 || p.Intent.EndpointPath != r.Target.EndpointPath || toolpublication.Digest(p.Outcome.Catalog) != r.Target.CatalogContractSHA256 {
		return nil, toolinvocation.ErrInvalid
	}
	return p.Outcome.Catalog, nil
}

func validateInvocationCatalog(o RegisteredInvocationOptions, r api.ToolInvocationRecord, c Contract, canonical, expected []byte) error {
	if !o.Delegated {
		if bytes.Equal(canonical, expected) {
			return nil
		}
		return toolinvocation.ErrInvalid
	}
	if toolpublication.Digest(canonical) != r.Target.CatalogContractSHA256 {
		return toolinvocation.ErrInvalid
	}
	for _, tool := range c.Tools {
		if tool.Name == r.Intent.Name {
			projection, err := NewCatalogContract(c.ProtocolVersion, Catalog{Tools: []Tool{tool}})
			if err != nil {
				return err
			}
			raw, err := MarshalContract(projection)
			if err == nil && bytes.Equal(raw, expected) {
				return nil
			}
			return toolinvocation.ErrInvalid
		}
	}
	return toolinvocation.ErrInvalid
}
