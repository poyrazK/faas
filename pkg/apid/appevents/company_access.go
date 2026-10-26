package appevents

import (
	"context"
	"errors"
	"strings"

	"github.com/onebox-faas/faas/pkg/apid/appaccess"
	"github.com/onebox-faas/faas/pkg/state"
)

// eventAccess applies company access to the account event routes
// (ADR-20261025211616). A company app keeps its rows under its creator's
// account, so these routes would otherwise show a company's events to a
// creator who left it. An event is shown when its producer and the recipient
// asked about are visible to the caller, appaccess.VisibleApps's rule;
// recipients in hidden apps are left out. Decisions are cached per request.
type eventAccess struct {
	ctx    context.Context
	store  appaccess.Store
	scopes state.EventReceiptScopeStore
	actor  state.Account
	apps   map[string]bool
	orgs   map[string]bool
}

func (s *Service) eventAccess(ctx context.Context, acct state.Account) (*eventAccess, error) {
	store, ok := s.Host.Store().(appaccess.Store)
	scopes, scoped := s.Host.Store().(state.EventReceiptScopeStore)
	if !ok || !scoped {
		return nil, errors.New("event company access needs the organization access and receipt scope stores")
	}
	return &eventAccess{ctx: ctx, store: store, scopes: scopes, actor: acct, apps: map[string]bool{"": true}, orgs: map[string]bool{}}, nil
}

// checkEventAccess answers ErrNotFound for an event, or one of its recipients,
// the caller may not see.
func (s *Service) checkEventAccess(ctx context.Context, acct state.Account, source, id, subscription string) error {
	access, err := s.eventAccess(ctx, acct)
	if err != nil {
		return err
	}
	_, err = access.event(acct.ID, source, id, subscription)
	return err
}

// app reports whether the caller may see an app's events. An app whose row is
// gone keeps the account's scope, as the account dead-letter view does.
func (a *eventAccess) app(id string) (bool, error) {
	if visible, ok := a.apps[id]; ok {
		return visible, nil
	}
	app, err := a.store.AppByID(a.ctx, id)
	if errors.Is(err, state.ErrNotFound) {
		a.apps[id] = true
		return true, nil
	}
	if err != nil {
		return false, err
	}
	shown, err := appaccess.VisibleAppsContext(a.ctx, a.store, a.actor, []state.App{app})
	if err != nil {
		return false, err
	}
	a.apps[id] = len(shown) == 1
	return a.apps[id], nil
}

// producer reports whether the caller may see an event stamped with producer.
// Personal and unstamped events stay in the account's scope.
func (a *eventAccess) producer(producer string) (bool, error) {
	kind, id, _ := strings.Cut(producer, ":")
	switch kind {
	case "app":
		return a.app(id)
	case "org":
		return a.org(id)
	}
	return true, nil
}

// org is VisibleApps's company rule for an event published with a company key:
// an active member of a company not pending deletion.
func (a *eventAccess) org(id string) (bool, error) {
	if visible, ok := a.orgs[id]; ok {
		return visible, nil
	}
	org, err := a.store.OrgByID(a.ctx, id)
	if errors.Is(err, state.ErrNotFound) {
		a.orgs[id] = false
		return false, nil
	}
	if err != nil {
		return false, err
	}
	visible := org.Personal
	if !visible && !org.DeletedPending && org.Status != state.OrgStatusDeletedPending {
		member, err := a.store.OrgAccessSnapshot(a.ctx, org.ID, a.actor.ID)
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			return false, err
		}
		switch member.Role {
		case state.OrgRoleOwner, state.OrgRoleAdmin, state.OrgRoleDeveloper, state.OrgRoleViewer:
			visible = err == nil
		}
	}
	a.orgs[id] = visible
	return visible, nil
}

// event loads an event's scope and reports whether the caller may see it and,
// when subscription is set, that recipient. A hidden event answers as a
// missing one (ErrNotFound) so the routes do not reveal it exists.
func (a *eventAccess) event(accountID, source, id, subscription string) (state.EventReceiptScope, error) {
	scope, err := a.scopes.EventReceiptScope(a.ctx, accountID, source, id)
	if err != nil {
		return scope, err
	}
	visible, err := a.producer(scope.Producer)
	if err == nil && visible && subscription != "" {
		visible, err = a.app(scope.RecipientApps[subscription])
	}
	if err != nil {
		return scope, err
	}
	if !visible {
		return scope, state.ErrNotFound
	}
	return scope, nil
}

// receipt hides an event the caller may not see and drops recipients in apps
// hidden from the caller. Aggregate counts still cover every recipient.
func (a *eventAccess) receipt(accountID string, receipt *state.EventReceipt) error {
	if _, err := a.event(accountID, receipt.EventSource, receipt.EventID, ""); err != nil {
		return err
	}
	kept := receipt.Recipients[:0]
	for _, entry := range receipt.Recipients {
		visible, err := a.app(entry.AppID)
		if err != nil {
			return err
		}
		if visible {
			kept = append(kept, entry)
		}
	}
	receipt.Recipients = kept
	return nil
}

// backlog drops waiting recipients and consumers in apps hidden from the
// caller. Totals still cover the whole account.
func (a *eventAccess) backlog(result *state.EventBacklog) error {
	recipients := result.Recipients[:0]
	for _, entry := range result.Recipients {
		visible, err := a.app(entry.AppID)
		if err != nil {
			return err
		}
		if visible {
			recipients = append(recipients, entry)
		}
	}
	result.Recipients = recipients
	consumers := result.Consumers[:0]
	for _, consumer := range result.Consumers {
		visible, err := a.app(consumer.AppID)
		if err != nil {
			return err
		}
		if visible {
			consumers = append(consumers, consumer)
		}
	}
	result.Consumers = consumers
	return nil
}

// appSlug is app for a resource that names its app by slug (replay jobs).
func (a *eventAccess) appSlug(slug string) (bool, error) {
	if slug == "" {
		return true, nil
	}
	app, err := a.store.AppBySlug(a.ctx, slug)
	if errors.Is(err, state.ErrNotFound) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return a.app(app.ID)
}
