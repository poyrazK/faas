package apidserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/routesplit"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

// Route split bindings (ADR-20261121170000 amendment 4): the route's app gets
// its own Postgres login on each database the app is bound to and its own
// key on each bucket, never a copy of the app's credentials, so rotating or
// deleting one app's binding never breaks the other.

// Bindings lists app's managed Postgres bindings and the bucket keys it
// holds.
func (h routeSplitHost) Bindings(ctx context.Context, app state.App) ([]routesplit.Binding, *api.Problem) {
	var out []routesplit.Binding
	if h.s.managedPostgresBindings != nil {
		rows, err := h.s.managedPostgresBindings.ListForApp(ctx, app.AccountID, app.ID, "")
		if err != nil && !errors.Is(err, managedpostgres.ErrUnsupported) {
			return nil, api.ErrCapacity("could not read the app's Postgres bindings")
		}
		for _, row := range rows {
			if row.State == string(managedpostgres.BindingStateDeleting) || row.State == string(managedpostgres.BindingStateDeleted) {
				continue
			}
			out = append(out, routesplit.Binding{
				Kind: routesplit.BindingPostgres, ID: row.BindingID, Scope: row.Scope,
				Label: row.EnvironmentKey + " (database " + row.DatabaseName + ")",
				Ready: row.State == string(managedpostgres.BindingStateReady) && !row.RotationPending,
			})
		}
	}
	held, err := h.s.object.HeldComputeBindings(ctx, app)
	if err != nil {
		return nil, api.ErrCapacity("could not read the app's bucket bindings")
	}
	for _, binding := range held {
		c := binding.Credential
		pending := false
		if store, ok := h.s.store.(state.ObjectS3CredentialBindingStore); ok {
			wake, err := store.PendingObjectS3CredentialRotation(ctx, app.AccountID, c.BucketID, c.ID)
			if err != nil {
				return nil, api.ErrCapacity("could not read the app's bucket bindings")
			}
			pending = wake != ""
		}
		out = append(out, routesplit.Binding{
			Kind: routesplit.BindingBucket, ID: c.ID, Scope: c.ManagedScope,
			Label: c.ManagedPrefix + "_* (bucket binding " + c.Label + ")", Ready: !pending,
		})
	}
	return out, nil
}

// Bind gives target its own binding like b.
func (h routeSplitHost) Bind(ctx context.Context, acct state.Account, source, target state.App, b routesplit.Binding) *api.Problem {
	switch b.Kind {
	case routesplit.BindingPostgres:
		return h.bindPostgres(ctx, acct, source, target, b)
	case routesplit.BindingBucket:
		held, err := h.s.object.HeldComputeBindings(ctx, source)
		if err != nil {
			return api.ErrCapacity("could not read the app's bucket bindings")
		}
		for _, binding := range held {
			if binding.Credential.ID == b.ID {
				_, prob := h.s.object.BorrowComputeBinding(ctx, acct, target, binding)
				return prob
			}
		}
		return api.NewProblem(http.StatusConflict, api.CodeRouteSplitChanged, "Binding changed", "the bucket binding "+b.Label+" was removed while the split ran; apply it again")
	}
	return api.ErrValidation("unknown binding kind " + b.Kind)
}

func (h routeSplitHost) bindPostgres(ctx context.Context, acct state.Account, source, target state.App, b routesplit.Binding) *api.Problem {
	if h.s.managedPostgresBindings == nil {
		return api.ErrCapacity("managed PostgreSQL is unavailable")
	}
	like, err := h.s.managedPostgresBindings.Get(ctx, source.AccountID, b.ID)
	if err != nil {
		return postgresBindingProblem(err)
	}
	created, err := h.s.managedPostgresBindings.Create(ctx, managedpostgres.CreateBindingRequest{
		AccountID: target.AccountID, DatabaseAccountID: like.DatabaseAccountID, DatabaseID: like.DatabaseID,
		AppID: target.ID, Scope: like.Scope, EnvironmentKey: like.EnvironmentKey, Access: like.Access,
	})
	if err != nil {
		return postgresBindingProblem(err)
	}
	h.s.audit.Emit(ctx, "managed_postgres.binding.created", &acct.ID, map[string]any{
		"binding_id": created.ID, "database_id": created.DatabaseID, "app_id": created.AppID,
		"scope": created.Scope, "access": created.Access, "route_split_of": source.ID,
	})
	if created.State != managedpostgres.BindingStateReady {
		// The route must boot with its DATABASE_URL; rollback deletes it.
		return api.NewProblem(http.StatusServiceUnavailable, api.CodeCapacity, "Binding not ready",
			"the database did not issue the route's app a login in time")
	}
	return nil
}

// ReleaseBindings deletes target's Postgres bindings and revokes the bucket
// keys it borrowed.
func (h routeSplitHost) ReleaseBindings(ctx context.Context, acct state.Account, target state.App) *api.Problem {
	if h.s.managedPostgresBindings != nil {
		rows, err := h.s.managedPostgresBindings.ListForApp(ctx, target.AccountID, target.ID, "")
		if err != nil && !errors.Is(err, managedpostgres.ErrUnsupported) {
			return api.ErrCapacity("could not read the route's app's Postgres bindings")
		}
		var failed []string
		for _, row := range rows {
			if row.State == string(managedpostgres.BindingStateDeleting) || row.State == string(managedpostgres.BindingStateDeleted) {
				continue
			}
			deleted, err := h.s.managedPostgresBindings.Delete(ctx, target.AccountID, row.BindingID)
			// A delete the provider has not finished stays "deleting" and
			// the reconciler retries it; apid deletes such an app.
			if err != nil && !errors.Is(err, managedpostgres.ErrNotFound) && !h.deleting(ctx, target, row.BindingID) {
				failed = append(failed, row.EnvironmentKey)
				continue
			}
			if err == nil {
				h.s.audit.Emit(ctx, "managed_postgres.binding.deleted", &acct.ID, map[string]any{
					"binding_id": deleted.ID, "database_id": deleted.DatabaseID, "app_id": deleted.AppID,
				})
			}
		}
		if len(failed) != 0 {
			return api.ErrCapacity("could not delete the route's app's Postgres bindings " + strings.Join(failed, ", "))
		}
	}
	if err := h.s.object.RevokeBorrowedComputeBindings(ctx, acct, target); err != nil {
		h.s.log.Warn("route split: could not revoke borrowed bucket keys", "app", target.ID, "err", err)
		return api.ErrCapacity("could not revoke the route's app's bucket keys")
	}
	return nil
}

func (h routeSplitHost) deleting(ctx context.Context, app state.App, bindingID string) bool {
	binding, err := h.s.managedPostgresBindings.Get(ctx, app.AccountID, bindingID)
	return err == nil && (binding.State == managedpostgres.BindingStateDeleting || binding.State == managedpostgres.BindingStateDeleted)
}

// postgresBindingProblem is the problem the binding API answers for err.
func postgresBindingProblem(err error) *api.Problem {
	rec := httptest.NewRecorder()
	managedPostgresProblem(rec, err)
	return recordedProblem(rec)
}

// appBorrowedBindingsProblem refuses to delete an app that holds keys on
// another app's bucket: only a route split makes one, and its undo removes
// the route's app with its keys and its route rule together. Deleting the
// app alone would leave the route pointing at a deleted app.
func (s *server) appBorrowedBindingsProblem(ctx context.Context, app state.App) *api.Problem {
	borrowed, err := s.object.BorrowedComputeBindings(ctx, app)
	if err != nil {
		return api.ErrCapacity("list-borrowed-bucket-bindings")
	}
	if len(borrowed) == 0 {
		return nil
	}
	prefixes := make([]string, 0, len(borrowed))
	for _, binding := range borrowed {
		prefixes = append(prefixes, binding.Credential.ManagedPrefix+"_*")
	}
	return api.NewProblem(http.StatusConflict, "app_has_borrowed_bucket_bindings", "Conflict",
		fmt.Sprintf("This app serves a route split off another app and holds its own keys on that app's buckets (%s).", strings.Join(prefixes, ", "))).
		WithHint("Undo the split instead: gregale app scale <app> --split-status, then gregale app scale <app> --undo-split ID.")
}
