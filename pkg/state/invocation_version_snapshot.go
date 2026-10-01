// adr: 375
package state

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// InvocationVersionReader is valid only inside its snapshot callback. Revision
// resolution returns identity only; guest artifacts and customer secrets are not
// part of version selection.
type InvocationVersionReader interface {
	AppByID(context.Context, string) (App, error)
	ResolveProjectRelease(context.Context, string, string, string) (string, string, error)
	ResolveRevisionPin(context.Context, string, string, string) (Deployment, error)
}

type InvocationVersionSnapshotStore interface {
	WithInvocationVersionSnapshot(context.Context, func(InvocationVersionReader) error) error
}

func (s *PgStore) WithInvocationVersionSnapshot(ctx context.Context, read func(InvocationVersionReader) error) error {
	if read == nil {
		return ErrInvalidArgument
	}
	return s.WithServicePolicySnapshot(ctx, func(reader ServicePolicyReader) error {
		return read(invocationVersionPolicyReader{servicePolicyReader: reader.(servicePolicyReader)})
	})
}

type invocationVersionPolicyReader struct{ servicePolicyReader }

func (s invocationVersionPolicyReader) AppByID(ctx context.Context, id string) (App, error) {
	row, err := sqlc.New().ReadInvocationVersionApp(ctx, s.tx, uuidToPgtype(id))
	if err != nil {
		return App{}, mapErr(err)
	}
	app := App{ID: pgUUIDString(row.ID), AccountID: pgUUIDString(row.AccountID),
		ProjectID: pgUUIDString(row.ProjectID), PreviewOfSlug: row.PreviewOfSlug.String, Status: AppStatus(row.Status)}
	if row.DeletedAt.Valid {
		at := row.DeletedAt.Time
		app.DeletedAt = &at
	}
	return app, nil
}

func (s invocationVersionPolicyReader) ResolveProjectRelease(ctx context.Context, app, scope, requested string) (string, string, error) {
	requested, err := canonicalInvocationPin(requested)
	if err != nil {
		return "", "", err
	}
	return s.ResolvePublicProjectRelease(ctx, app, scope, requested)
}

func (s invocationVersionPolicyReader) ResolveRevisionPin(ctx context.Context, app, scope, deployment string) (Deployment, error) {
	deployment, err := canonicalInvocationPin(deployment)
	if err != nil {
		return Deployment{}, err
	}
	allowed, err := s.PublicRevisionAllowed(ctx, app, scope, deployment)
	if err != nil {
		return Deployment{}, err
	}
	if !allowed {
		return Deployment{}, ErrNotFound
	}
	return Deployment{ID: deployment}, nil
}

func (m *MemStore) WithInvocationVersionSnapshot(ctx context.Context, read func(InvocationVersionReader) error) error {
	if read == nil {
		return ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := read(memInvocationVersionReader{store: m}); err != nil {
		return err
	}
	return ctx.Err()
}

type memInvocationVersionReader struct{ store *MemStore }

func (s memInvocationVersionReader) AppByID(_ context.Context, id string) (App, error) {
	app, err := s.store.appByIDLocked(id)
	if err != nil {
		return App{}, err
	}
	return App{ID: app.ID, AccountID: app.AccountID, ProjectID: app.ProjectID,
		PreviewOfSlug: app.PreviewOfSlug, Status: app.Status, DeletedAt: cloneTimePtr(app.DeletedAt)}, nil
}

func (s memInvocationVersionReader) ResolveProjectRelease(_ context.Context, app, scope, requested string) (string, string, error) {
	requested, err := canonicalInvocationPin(requested)
	if err != nil {
		return "", "", err
	}
	return s.store.resolveProjectReleaseLocked(app, scope, requested)
}

func (s memInvocationVersionReader) ResolveRevisionPin(_ context.Context, app, scope, deployment string) (Deployment, error) {
	deployment, err := canonicalInvocationPin(deployment)
	if err != nil {
		return Deployment{}, err
	}
	if _, ok := s.store.deployments[deployment]; !ok {
		// Historical memory deployment IDs omit dashes; the returned identity
		// must match the stored row and scheduler target, not the header spelling.
		deployment = strings.ReplaceAll(deployment, "-", "")
	}
	dep, err := s.store.resolveRevisionPinLocked(app, scope, deployment)
	return Deployment{ID: dep.ID}, err
}

func canonicalInvocationPin(pin string) (string, error) {
	if pin == "" {
		return "", nil
	}
	id, err := uuid.Parse(pin)
	if err != nil {
		return "", ErrInvalidArgument
	}
	return id.String(), nil
}
