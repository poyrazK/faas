package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

var ErrServiceBindingPlanConflict = errors.New("service binding settings changed; create a fresh plan")

// ServiceBindingFingerprint binds the reviewed settings to this app identity
// and source ownership. Unrelated app settings do not invalidate a plan.
func ServiceBindingFingerprint(app App) string {
	m := app.Manifest
	bindings := append([]api.AppServiceBinding{}, m.ServiceBindings...)
	slices.SortFunc(bindings, func(a, b api.AppServiceBinding) int {
		return strings.Compare(a.Service+"\x00"+a.Binding, b.Service+"\x00"+b.Binding)
	})
	callers := m.AllowedServiceCallers
	if callers != nil {
		copyOfCallers := append([]string{}, (*callers)...)
		slices.Sort(copyOfCallers)
		callers = &copyOfCallers
	}
	scopes := m.AllowedServiceCallScopes
	if scopes != nil {
		// A non-nil empty grant map denies all, even when its underlying
		// map is nil. Keep it distinct from an unrestricted nil pointer.
		copyOfScopes := make(api.ServiceCallerScopes, len(*scopes))
		for caller, grant := range *scopes {
			copyOfScopes[caller] = grant
		}
		scopes = &copyOfScopes
	}
	reliability := m.ServiceReliability
	if len(reliability) == 0 {
		reliability = nil
	}
	body, _ := json.Marshal(struct {
		Version                              int
		Account, App, Slug, Project, Preview string
		Bindings                             []api.AppServiceBinding
		Policy                               api.ServiceBindingPolicy
		Transport                            api.ServiceBindingTransport
		Callers                              *[]string
		Scopes                               *api.ServiceCallerScopes
		Reliability                          map[string]api.ServiceReliabilityPolicy
	}{1, app.AccountID, app.ID, app.Slug, app.ProjectID, app.PreviewOfSlug, bindings, m.ServiceBindingPolicy.Effective(), m.ServiceBindingTransport.Effective(), callers, scopes, reliability})
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}

// Called while the app row/memory mutex is locked, before any update or outbox
// insert. Merge only the guarded settings, preserving unrelated concurrent edits.
func prepareServiceBindingPlanUpdate(before App, p UpdateAppParams) (UpdateAppParams, error) {
	if p.ExpectedServiceBindingFingerprint == "" {
		return p, nil
	}
	if ServiceBindingFingerprint(before) != p.ExpectedServiceBindingFingerprint {
		return p, fmt.Errorf("%w: %w", ErrConflict, ErrServiceBindingPlanConflict)
	}
	if p.Manifest == nil {
		return p, ErrInvalidArgument
	}
	m := before.Manifest
	desired := p.Manifest
	m.ServiceBindings = desired.ServiceBindings
	m.ServiceBindingPolicy = desired.ServiceBindingPolicy
	m.ServiceBindingTransport = desired.ServiceBindingTransport
	m.AllowedServiceCallers = desired.AllowedServiceCallers
	m.AllowedServiceCallScopes = desired.AllowedServiceCallScopes
	m.ServiceReliability = desired.ServiceReliability
	m.Env = api.ServiceBindingEnvForTransport(before.Manifest.Env, m.ServiceBindings, m.ServiceBindingTransport)
	p.Manifest = &m
	return p, nil
}
