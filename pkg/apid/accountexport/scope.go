// Package accountexport keeps identity exports within personal resource ownership.
package accountexport

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type Store interface {
	AppByID(context.Context, string) (state.App, error)
	ProjectByID(context.Context, string) (state.Project, error)
	ProjectBySlug(context.Context, string, string) (state.Project, error)
	OrgByID(context.Context, string) (state.Org, error)
}

type key struct{}
type Scope struct {
	store     Store
	accountID string
	apps      map[string]bool
	orgs      map[string]state.Org
}

func WithScope(ctx context.Context, store Store, accountID string) (context.Context, *Scope) {
	scope := &Scope{store: store, accountID: accountID, apps: make(map[string]bool), orgs: make(map[string]state.Org)}
	return context.WithValue(ctx, key{}, scope), scope
}

func For(ctx context.Context, store Store, accountID string) *Scope {
	if scope, ok := ctx.Value(key{}).(*Scope); ok && scope.accountID == accountID {
		return scope
	}
	_, scope := WithScope(ctx, store, accountID)
	return scope
}

func (s *Scope) AppRow(ctx context.Context, app state.App) (bool, error) {
	allowed := app.AccountID == s.accountID
	if allowed && app.OrgID != "" {
		org, ok := s.orgs[app.OrgID]
		if !ok {
			var err error
			org, err = s.store.OrgByID(ctx, app.OrgID)
			if err != nil {
				return false, err
			}
			s.orgs[org.ID] = org
		}
		allowed = org.Personal && org.PersonalOwnerAccountID != nil && *org.PersonalOwnerAccountID == s.accountID
	}
	s.apps[app.ID] = allowed
	return allowed, nil
}

func (s *Scope) App(ctx context.Context, id string) (bool, error) {
	if allowed, ok := s.apps[id]; ok {
		return allowed, nil
	}
	app, err := s.store.AppByID(ctx, id)
	if errors.Is(err, state.ErrNotFound) {
		s.apps[id] = false
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return s.AppRow(ctx, app)
}

func (s *Scope) Audit(ctx context.Context, data json.RawMessage) (bool, error) {
	if len(data) == 0 {
		return true, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return true, nil //nolint:nilerr // Historical non-object payloads have no structured resource reference.
	}
	var source string
	if json.Unmarshal(fields["source"], &source) == nil && source == api.ObjectEventSource {
		nested := fields["data"]
		var event api.ObjectStorageEvent
		if json.Unmarshal(nested, &event) != nil || event.BucketID == "" {
			return false, nil //nolint:nilerr // Exclude malformed storage envelopes without failing the entire export.
		}
		if allowed, err := s.Audit(ctx, nested); err != nil || !allowed {
			return false, err
		}
	}
	if value, ok := fields["org_id"]; ok {
		var id string
		if err := json.Unmarshal(value, &id); err != nil {
			return false, nil //nolint:nilerr // Exclude malformed resource references without failing the entire export.
		}
		if id != "" {
			personal, err := s.personalOrganization(ctx, id)
			if err != nil || !personal {
				return false, err
			}
		}
	}
	for _, name := range []string{"app_id", "app"} {
		if value, ok := fields[name]; ok {
			var id string
			if err := json.Unmarshal(value, &id); err != nil {
				return false, nil //nolint:nilerr // Exclude malformed resource references without failing the entire export.
			}
			if id != "" {
				allowed, err := s.App(ctx, id)
				if err != nil || !allowed {
					return false, err
				}
			}
		}
	}
	for _, name := range []string{"project_id", "project_slug"} {
		value, present := fields[name]
		if !present {
			continue
		}
		var reference string
		if err := json.Unmarshal(value, &reference); err != nil {
			return false, nil //nolint:nilerr // Exclude malformed resource references.
		}
		if reference == "" {
			continue
		}
		allowed, err := s.project(ctx, name, reference)
		if err != nil || !allowed {
			return false, err
		}
	}
	for _, name := range []string{"database_id", "source_database_id", "binding_id"} {
		value, present := fields[name]
		if !present {
			continue
		}
		var id string
		if err := json.Unmarshal(value, &id); err != nil {
			return false, nil //nolint:nilerr // Exclude malformed resource references from personal exports.
		}
		if id == "" {
			continue
		}
		reader, ok := s.store.(interface {
			PersonalManagedPostgresReference(context.Context, string, string, string) (bool, error)
		})
		if !ok {
			return false, nil
		}
		allowed, err := reader.PersonalManagedPostgresReference(ctx, s.accountID, name, id)
		if err != nil || !allowed {
			return false, err
		}
	}
	for _, name := range []string{"bucket_id", "source_bucket_id", "credential_id"} {
		value, present := fields[name]
		if !present {
			continue
		}
		var id string
		if err := json.Unmarshal(value, &id); err != nil {
			return false, nil //nolint:nilerr // Malformed historical storage references cannot be exported.
		}
		if id == "" {
			continue
		}
		reader, ok := s.store.(interface {
			PersonalObjectStorageReference(context.Context, string, string, string) (bool, error)
		})
		if !ok {
			return false, nil
		}
		allowed, err := reader.PersonalObjectStorageReference(ctx, s.accountID, name, id)
		if err != nil || !allowed {
			return false, err
		}
	}
	for _, name := range []string{"queue_binding_id", "invocation_id"} {
		value, present := fields[name]
		if !present {
			continue
		}
		var id string
		if json.Unmarshal(value, &id) != nil {
			return false, nil //nolint:nilerr // Malformed historical queue references are excluded from personal exports.
		}
		if id == "" {
			continue
		}
		reader, ok := s.store.(interface {
			PersonalQueueReference(context.Context, string, string, string) (bool, error)
		})
		if !ok {
			return false, nil
		}
		allowed, err := reader.PersonalQueueReference(ctx, s.accountID, name, id)
		if err != nil || !allowed {
			return false, err
		}
	}
	return true, nil
}

func (s *Scope) personalOrganization(ctx context.Context, id string) (bool, error) {
	org, cached := s.orgs[id]
	if !cached {
		var err error
		org, err = s.store.OrgByID(ctx, id)
		switch {
		case errors.Is(err, state.ErrNotFound):
			return false, nil
		case err != nil:
			return false, err
		}
		s.orgs[id] = org
	}
	return org.Personal && org.PersonalOwnerAccountID != nil && *org.PersonalOwnerAccountID == s.accountID, nil
}

func (s *Scope) project(ctx context.Context, kind, reference string) (bool, error) {
	var project state.Project
	var err error
	if kind == "project_id" {
		project, err = s.store.ProjectByID(ctx, reference)
	} else {
		project, err = s.store.ProjectBySlug(ctx, s.accountID, reference)
	}
	if errors.Is(err, state.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if project.AccountID != s.accountID {
		return false, nil
	}
	if project.OrgID != "" {
		return s.personalOrganization(ctx, project.OrgID)
	}
	return true, nil
}
