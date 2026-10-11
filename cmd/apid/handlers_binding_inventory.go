package main

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

const bindingInventoryReadTimeout = 5 * time.Second

type bindingInventorySection struct {
	items             []api.AppBindingInventoryItem
	issues            []api.BindingInventoryIssue
	revisions         map[string]string
	refreshWakeIDs    map[string]string
	adoptionSelectors map[string]state.BindingAdoptionSelector
	privateBindingIDs map[string]string
}

func bindingInventoryUnavailable(kind, code, severity, message string) bindingInventorySection {
	return bindingInventorySection{issues: []api.BindingInventoryIssue{{Type: kind, Code: code, Severity: severity, Message: message}}}
}

func (s *server) getAppBindingInventory(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	scope := r.URL.Query().Get("scope")
	if scope != "" {
		if problem := api.ValidateScope(scope); problem != nil {
			api.WriteProblem(w, problem)
			return
		}
	}
	if id := r.URL.Query().Get("deployment_id"); id != "" {
		deployment, problem := s.selectBindingVerificationDeployment(r.Context(), app, id)
		if problem != nil {
			api.WriteProblem(w, problem)
			return
		}
		// Canonicalize the selector once; all later reads use this same ID.
		query := r.URL.Query()
		query.Set("deployment_id", deployment.ID)
		r.URL.RawQuery = query.Encode()
	}
	inventory := s.collectBindingInventory(r.Context(), r, acct, app, scope)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, inventory)
}

// Each domain retains its own store and authorization boundary. The bounded
// parallel reads prevent one unavailable domain from delaying healthy sections.
func (s *server) collectBindingInventory(parent context.Context, r *http.Request, acct state.Account, app state.App, scope string) api.AppBindingInventory {
	inventory := api.AppBindingInventory{
		App: app.Slug, Scope: scope, Complete: true, GeneratedAt: time.Now().UTC(),
		RequestedDeploymentID: r.URL.Query().Get("deployment_id"),
		Bindings:              serviceBindingInventory(app),
	}
	ctx, cancel := context.WithTimeout(parent, bindingInventoryReadTimeout)
	revisions, err := s.serviceBindingRevisions(ctx, app)
	cancel()
	if err != nil {
		revisions = map[string]string{}
		issue := api.BindingInventoryIssue{Type: api.BindingTypeService, Code: "query_failed", Severity: "error", Message: "Service dependency metadata could not be read."}
		inventory.Issues = append(inventory.Issues, issue)
		inventory.Warnings = append(inventory.Warnings, issue.Message)
	}
	refreshWakeIDs := make(map[string]string)
	adoptionSelectors := make(map[string]state.BindingAdoptionSelector)
	privateBindingIDs := make(map[string]string)
	readers := []struct {
		kind   string
		scopes []string
		read   func(context.Context) bindingInventorySection
	}{
		{api.BindingTypePostgres, api.ScopesManagedPostgresReadSurface, func(ctx context.Context) bindingInventorySection {
			return s.postgresBindingInventory(ctx, acct.ID, app.ID, scope)
		}},
		{api.BindingTypeObjectStorage, api.ScopesStorageManageSurface, func(ctx context.Context) bindingInventorySection {
			return s.objectStorageBindingInventory(ctx, acct.ID, app.ID, scope)
		}},
		{api.BindingTypeQueue, api.ScopesReadSurface, func(ctx context.Context) bindingInventorySection {
			return s.queueBindingInventory(ctx, acct.ID, app.ID, inventory.GeneratedAt)
		}},
		{api.BindingTypeOutbound, api.ScopesReadSurface, func(ctx context.Context) bindingInventorySection {
			return s.outboundBindingInventory(ctx, acct.ID, app.ID)
		}},
	}
	sections := make([]bindingInventorySection, len(readers))
	var wg sync.WaitGroup
	for index, reader := range readers {
		workerRead, _ := r.Context().Value(bindingReleaseWorkerReadsKey{}).(bool)
		if !workerRead && !middleware.HasScope(r, reader.scopes...) {
			sections[index] = bindingInventoryUnavailable(reader.kind, "forbidden", "error", fmt.Sprintf("%s binding metadata requires one of: %s.", reader.kind, strings.Join(reader.scopes, ", ")))
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(parent, bindingInventoryReadTimeout)
			defer cancel()
			sections[index] = reader.read(ctx)
		}()
	}
	wg.Wait()
	for _, section := range sections {
		for key, selector := range section.adoptionSelectors {
			adoptionSelectors[key] = selector
		}
		for key, wakeID := range section.refreshWakeIDs {
			refreshWakeIDs[key] = wakeID
		}
		for key, revision := range section.revisions {
			revisions[key] = revision
		}
		for key, bindingID := range section.privateBindingIDs {
			privateBindingIDs[key] = bindingID
		}
		inventory.Bindings = append(inventory.Bindings, section.items...)
		inventory.Issues = append(inventory.Issues, section.issues...)
		for _, issue := range section.issues {
			inventory.Warnings = append(inventory.Warnings, issue.Message)
		}
	}
	s.applyBindingRuntimeInventory(parent, r, acct, app, &inventory, refreshWakeIDs)
	s.applyBindingVerification(parent, r, acct, app, &inventory, revisions, privateBindingIDs)
	s.applyBindingApplicationAdoption(parent, acct, app, &inventory, adoptionSelectors)
	inventory.Complete = len(inventory.Issues) == 0
	sort.Slice(inventory.Bindings, func(i, j int) bool {
		a, b := inventory.Bindings[i], inventory.Bindings[j]
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		if a.Binding != b.Binding {
			return a.Binding < b.Binding
		}
		return a.Scope < b.Scope
	})
	return inventory
}

func inventoryItem(kind, name, binding, scope, access, config string) api.AppBindingInventoryItem {
	return api.AppBindingInventoryItem{
		Type: kind, Name: name, Binding: binding, Scope: scope, Access: access, State: config,
		RuntimeStatus: "unknown", VerificationStatus: "unknown",
	}
}

func serviceBindingInventory(app state.App) []api.AppBindingInventoryItem {
	items := make([]api.AppBindingInventoryItem, 0, len(app.Manifest.ServiceBindings))
	config := "declared"
	if app.Manifest.ServiceBindingPolicy.Effective() == api.ServiceBindingPolicyDeclared {
		config = "enforced"
	}
	for _, binding := range app.Manifest.ServiceBindings {
		item := inventoryItem(api.BindingTypeService, binding.Service, binding.Binding, "app", "invoke", config)
		item.HTTPURL = fmt.Sprintf("http://%s.svc.gregale:%d", binding.Service, api.ServiceBindingPort)
		item.HTTPSEnv = api.ServiceBindingHTTPSEnvKey(binding.Service)
		item.HTTPSURL = fmt.Sprintf("https://%s.internal", binding.Service)
		item.Transport = string(app.Manifest.ServiceBindingTransport.Effective())
		items = append(items, item)
	}
	return items
}
