package toolregistry

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/mcpcatalog"
	"github.com/onebox-faas/faas/pkg/apid/mcpreadiness"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

type publicationRoleTransport struct {
	executor     *PrivateExecutor
	account      string
	verification api.ToolPublicationVerification
	role         string
	next         http.RoundTripper
}

func (t publicationRoleTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	_, err := t.executor.publicationChange(r.Context(), t.account, t.verification.PublicationID, t.verification.KeyID, "readiness_dispatch", state.ToolPublicationVerificationChange{Role: t.role})
	if err != nil {
		return nil, err
	}
	return t.next.RoundTrip(r)
}

func (e *PrivateExecutor) publicationReadiness(ctx context.Context, account string, p api.ToolPublicationRecord, v api.ToolPublicationVerification, endpoint string) ([]api.ToolPublicationRoleResult, []string, error) {
	if p.Intent.Readiness == nil {
		return nil, nil, nil
	}
	results := []api.ToolPublicationRoleResult{}
	values := []string{}
	for _, role := range p.Intent.Readiness.Roles {
		result, sensitive, err := e.publicationReadinessRole(ctx, account, v, endpoint, role.Name)
		results = append(results, result)
		values = append(values, sensitive...)
		if err != nil {
			return results, values, err
		}
	}
	return results, values, nil
}

func (e *PrivateExecutor) publicationReadinessRole(ctx context.Context, account string, v api.ToolPublicationVerification, endpoint, name string) (api.ToolPublicationRoleResult, []string, error) {
	result := api.ToolPublicationRoleResult{Role: name}
	ctx, cancel := context.WithTimeout(ctx, api.ToolPublicationReadinessRoleTimeout)
	defer cancel()
	subject, role, err := e.publicationRoleSubject(ctx, account, v, name)
	if err != nil {
		return result, nil, err
	}
	client := *e.opts.HTTP
	next := client.Transport
	if next == nil {
		next = http.DefaultTransport
	}
	client.Transport = publicationRoleTransport{executor: e, account: account, verification: v, role: name, next: next}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	prior, err := e.publicationRoleSensitive(ctx, subject, role)
	if err != nil {
		return result, nil, err
	}
	token := ""
	if role.Monitor.ManagedOAuth() {
		ref := role.Monitor.OAuthForScope(api.DefaultEnvScope)
		if ref == nil {
			return result, nil, state.ErrNotFound
		}
		runner := mcpcatalog.ObserverRunner{Store: e.store, Client: &client, Identities: e.opts.Identities}
		credential, err := runner.AcquireReadinessOAuth(ctx, account, v.AppID, *ref, subject.Policy.Endpoint)
		if err != nil {
			return result, nil, err
		}
		defer func() {
			release, stop := context.WithTimeout(context.WithoutCancel(ctx), api.ToolOAuthPersistTimeout)
			defer stop()
			_ = credential.Release(release)
		}()
		token = credential.Token
	} else if role.Monitor != nil && role.Monitor.SecretRef != "" {
		token, err = e.publicationRoleSecret(ctx, subject, role.Monitor.SecretRef)
		if err != nil {
			return result, nil, err
		}
	}
	if token != "" && (!executionToken(token) || !api.ValidMCPProbeToken(token)) {
		return result, nil, state.ErrNotFound
	}
	subject, role, err = e.publicationRoleSubject(ctx, account, v, name)
	if err != nil {
		return result, nil, err
	}
	current, err := e.publicationRoleSensitive(ctx, subject, role)
	if err != nil {
		return result, nil, err
	}
	values := append(append(prior, current...), token)
	evidence := mcpreadiness.ProbeEvidence(ctx, &client, endpoint, token, role)
	status := "failed"
	if evidence.Passed {
		status = "passed"
	}
	evidence.Checks = []api.MCPReadinessCheck{{Name: "publication_readiness", Status: status}}
	saved, err := e.publicationChange(ctx, account, v.PublicationID, v.KeyID, "readiness_record", state.ToolPublicationVerificationChange{Role: name, ReadinessSubject: &subject, ReadinessEvidence: &evidence})
	if err != nil {
		return result, values, err
	}
	if saved.ReadinessEvidence == nil {
		return result, values, state.ErrNotFound
	}
	result.EvidenceID, result.Passed = saved.ReadinessEvidence.ID, saved.ReadinessEvidence.Passed
	if !result.Passed {
		return result, values, errors.New("required readiness role failed")
	}
	return result, values, nil
}

func (e *PrivateExecutor) publicationRoleSubject(ctx context.Context, account string, v api.ToolPublicationVerification, name string) (state.MCPReadinessSubject, api.MCPReadinessRole, error) {
	out, err := e.publicationChange(ctx, account, v.PublicationID, v.KeyID, "readiness_dispatch", state.ToolPublicationVerificationChange{Role: name})
	if err != nil {
		return state.MCPReadinessSubject{}, api.MCPReadinessRole{}, err
	}
	if out.ReadinessSubject == nil {
		return state.MCPReadinessSubject{}, api.MCPReadinessRole{}, state.ErrNotFound
	}
	for _, role := range out.ReadinessSubject.Policy.Roles {
		if role.Name == name {
			return *out.ReadinessSubject, role, nil
		}
	}
	return state.MCPReadinessSubject{}, api.MCPReadinessRole{}, state.ErrNotFound
}

func (e *PrivateExecutor) publicationRoleSecret(ctx context.Context, s state.MCPReadinessSubject, ref string) (string, error) {
	if !strings.HasPrefix(ref, api.SecretRefPrefix) {
		return "", state.ErrInvalidArgument
	}
	key := strings.TrimPrefix(ref, api.SecretRefPrefix)
	secret, err := e.store.GetAppSecretInScope(ctx, s.AccountID, s.AppID, api.DefaultEnvScope, key)
	if err != nil || secret == nil || secret.AccountID != s.AccountID || secret.AppID != s.AppID || secret.Scope != api.DefaultEnvScope || secret.Key != key || secret.ManagedCredentialRef != "" || secret.ManagedPostgresBindingID != "" || secret.ManagedObjectStorageCredentialID != "" {
		return "", state.ErrNotFound
	}
	values, err := secretbox.OpenMulti(e.opts.Identities(ctx), secret.Ciphertext)
	if err != nil || !executionOAuthSecret(values[key]) {
		return "", state.ErrNotFound
	}
	return values[key], nil
}

func (e *PrivateExecutor) publicationRoleSensitive(ctx context.Context, s state.MCPReadinessSubject, role api.MCPReadinessRole) ([]string, error) {
	out := []string{}
	if role.Monitor == nil {
		return out, nil
	}
	if !role.Monitor.ManagedOAuth() {
		if role.Monitor.SecretRef == "" {
			return out, nil
		}
		value, err := e.publicationRoleSecret(ctx, s, role.Monitor.SecretRef)
		return []string{value}, err
	}
	ref := role.Monitor.OAuthForScope(api.DefaultEnvScope)
	store, ok := e.store.(state.MCPCatalogMonitorStore)
	if !ok || ref == nil {
		return nil, state.ErrNotFound
	}
	root, err := store.GetMCPCatalogMonitor(ctx, s.AccountID, s.AppID)
	if err != nil {
		return nil, err
	}
	view, found := api.CatalogMonitorRoleSnapshot(root, ref.Role)
	if !found || view.Config.Revision != ref.Revision || view.Config.Observer == nil || view.Config.Observer.OAuth == nil {
		return nil, state.ErrNotFound
	}
	cfg := view.Config.Observer.OAuth
	for _, source := range []string{cfg.RefreshTokenSecretRef, cfg.ClientSecretRef} {
		if source != "" {
			value, err := e.publicationRoleSecret(ctx, s, source)
			if err != nil {
				return nil, err
			}
			out = append(out, value)
		}
	}
	if view.ObserverStatus != nil && view.ObserverStatus.OAuth != nil && len(view.ObserverStatus.OAuth.Ciphertext) > 0 {
		values, err := secretbox.OpenMulti(e.opts.Identities(ctx), view.ObserverStatus.OAuth.Ciphertext)
		if err != nil {
			return nil, err
		}
		out = append(out, values["ACCESS_TOKEN"], values["REFRESH_TOKEN"])
	}
	return out, nil
}
