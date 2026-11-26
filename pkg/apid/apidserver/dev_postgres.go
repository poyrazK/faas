package apidserver

import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/postgresaccess"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

const devPostgresEnvironmentKey = "DATABASE_URL"

// devPostgresMigrationKey carries a migration credential for the developer
// database. Only release tasks receive migration credentials, so a dev app's
// release command can create its schema while the serving app and a
// --postgres-seed keep DATABASE_URL, whose read_write login cannot.
const devPostgresMigrationKey = "MIGRATION_DATABASE_URL"

// devPostgresBindings are the bindings a developer database gets, the
// DATABASE_URL one first: the response and the ownership rule name it.
var devPostgresBindings = []struct {
	key    string
	access managedpostgres.CredentialAccess
}{
	{devPostgresEnvironmentKey, managedpostgres.CredentialReadWrite},
	{devPostgresMigrationKey, managedpostgres.CredentialMigration},
}

const devPostgresStorageLimitBytes int64 = 1 << 30

// ensureDevPostgres creates or reuses the database whose name matches the
// stable developer app slug, then binds it to DATABASE_URL and
// MIGRATION_DATABASE_URL (devPostgresBindings). The name is an
// internal idempotency key; callers only receive safe resource metadata.
func (s *server) ensureDevPostgres(ctx context.Context, acct state.Account, app state.App, request *api.DevPostgresRequest) (*api.DevPostgresResponse, error) {
	if request == nil {
		return nil, nil
	}
	if s.managedPostgres == nil || s.managedPostgresBindings == nil {
		return nil, managedpostgres.ErrUnavailable
	}
	if managedPostgresNotInPlan(acct.Plan) {
		return nil, managedpostgres.ErrNotInPlan
	}
	limits, _ := api.ManagedPostgresLimitsFor(acct.Plan)
	if !limits.DevelopmentAllowed {
		return nil, managedpostgres.ErrQuotaExceeded
	}
	if err := s.devPostgresDatabaseURLFree(ctx, acct, app); err != nil {
		return nil, err
	}
	ctx, err := postgresaccess.PersonalContext(ctx, s.store, acct.ID)
	if err != nil {
		return nil, err
	}
	databases, err := s.managedPostgres.List(ctx, acct.ID)
	if err != nil {
		return nil, err
	}
	wasExisting := false
	var existing *managedpostgres.Database
	for _, database := range databases {
		if database.Name == app.Slug {
			wasExisting = true
			copy := database
			existing = &copy
			break
		}
	}
	region := request.Region
	if region == "" {
		if existing != nil {
			region = existing.Spec.Region
		} else {
			region = s.managedPostgres.DefaultRegion()
		}
	}
	spec := managedpostgres.Spec{
		Region:               region,
		PostgresMajor:        api.ManagedPostgresDefaultMajor,
		Class:                managedpostgres.ClassDevelopment,
		Availability:         managedpostgres.AvailabilitySingleZone,
		ScaleToZero:          true,
		StorageLimitBytes:    min(devPostgresStorageLimitBytes, limits.StorageLimitBytes),
		RestoreWindowSeconds: 0,
	}
	if existing != nil {
		if existing.Spec.Class != managedpostgres.ClassDevelopment || !existing.Spec.ScaleToZero || existing.Spec.Availability != managedpostgres.AvailabilitySingleZone {
			return nil, managedpostgres.ErrConflict
		}
		if request.Region == "" {
			spec = existing.Spec
		}
		// A database made when dev databases were PostgreSQL 16 keeps its
		// major; only new ones take the current default.
		spec.PostgresMajor = existing.Spec.PostgresMajor
	}
	if spec.StorageLimitBytes > limits.StorageLimitBytes {
		return nil, managedpostgres.ErrQuotaExceeded
	}
	database, err := s.managedPostgres.Create(ctx, managedpostgres.CreateRequest{
		AccountID: acct.ID,
		Name:      app.Slug,
		Spec:      spec,
	})
	if err != nil {
		return nil, err
	}

	var created []string
	var primary managedpostgres.Binding
	states := make([]managedpostgres.BindingState, 0, len(devPostgresBindings))
	for i, want := range devPostgresBindings {
		binding, bindingCreated, err := s.managedPostgresBindings.CreateWithResult(ctx, managedpostgres.CreateBindingRequest{
			AccountID:      acct.ID,
			DatabaseID:     database.ID,
			AppID:          app.ID,
			Scope:          api.DefaultEnvScope,
			EnvironmentKey: want.key,
			Access:         want.access,
		})
		if bindingCreated && binding.ID != "" {
			created = append(created, binding.ID)
		}
		if err != nil {
			// Sandbox setup retries under a durable lease and reuses partial
			// resources. Its janitor owns cleanup after the sandbox closes.
			if !state.IsAPISandbox(app) {
				s.compensateDevPostgres(ctx, acct.ID, database.ID, created, !wasExisting)
			}
			return nil, err
		}
		if i == 0 {
			primary = binding
		}
		states = append(states, binding.State)
	}
	return &api.DevPostgresResponse{
		DatabaseID:     database.ID,
		Name:           database.Name,
		State:          string(database.State),
		BindingID:      primary.ID,
		BindingState:   devPostgresBindingsState(states),
		EnvironmentKey: primary.EnvironmentKey,
		// The CLI counts this key as provided, so a release command that reads
		// it passes the developer configuration check.
		MigrationEnvironmentKey: devPostgresMigrationKey,
	}, nil
}

// compensateDevPostgres undoes what a failed ensureDevPostgres created: the
// bindings it made and, when it made the database too, the database.
func (s *server) compensateDevPostgres(ctx context.Context, accountID, databaseID string, bindingIDs []string, deleteDatabase bool) {
	ctx = context.WithoutCancel(ctx)
	for _, id := range bindingIDs {
		if _, err := s.managedPostgresBindings.Delete(ctx, accountID, id); err != nil && s.log != nil {
			s.log.Warn("dev postgres binding compensation failed", "binding_id", id, "err", err)
		}
	}
	if !deleteDatabase {
		return
	}
	if _, err := s.managedPostgres.Delete(ctx, accountID, databaseID); err != nil && s.log != nil {
		s.log.Warn("dev postgres database compensation failed", "database_id", databaseID, "err", err)
	}
}

// devPostgresSecretConflict reports a customer secret in the developer app's
// default scope under a key a developer database binding needs.
type devPostgresSecretConflict struct{ key string }

func (e devPostgresSecretConflict) Error() string {
	return "developer app already holds a " + e.key + " secret"
}

// devPostgresDatabaseURLFree refuses --postgres while the developer app holds
// a DATABASE_URL or MIGRATION_DATABASE_URL secret that no managed binding
// owns. A binding's credential write only replaces a row the same binding
// owns, so it would fail and retry hourly while the app kept the old value,
// and nothing would tell the developer. A binding's own row (a renewal) is
// fine.
func (s *server) devPostgresDatabaseURLFree(ctx context.Context, acct state.Account, app state.App) error {
	for _, want := range devPostgresBindings {
		secret, err := s.store.GetAppSecret(ctx, acct.ID, app.ID, want.key)
		if errors.Is(err, state.ErrNotFound) {
			continue
		}
		if err != nil {
			return fmt.Errorf("read developer %s secret: %w", want.key, err)
		}
		if secret.ManagedPostgresBindingID != "" || secret.ManagedObjectStorageCredentialID != "" {
			continue
		}
		return devPostgresSecretConflict{key: want.key}
	}
	return nil
}

// describeDevPostgres returns the safe state of the automatically named
// database whose DATABASE_URL binding belongs to this developer app, without
// creating or changing anything. A same-named database without that binding
// is not reported, matching cleanupDevPostgres's ownership rule.
func (s *server) describeDevPostgres(ctx context.Context, app state.App) (*api.DevPostgresResponse, error) {
	if s.managedPostgres == nil || s.managedPostgresBindings == nil {
		return nil, nil
	}
	databases, err := s.managedPostgres.List(ctx, app.AccountID)
	if err != nil {
		return nil, err
	}
	for _, database := range databases {
		if database.Name != app.Slug {
			continue
		}
		bindings, listErr := s.managedPostgresBindings.List(ctx, app.AccountID, database.ID)
		if listErr != nil {
			return nil, listErr
		}
		var out *api.DevPostgresResponse
		var states []managedpostgres.BindingState
		for _, binding := range bindings {
			if binding.AppID != app.ID || binding.Scope != api.DefaultEnvScope || !isDevPostgresBindingKey(binding.EnvironmentKey) {
				continue
			}
			states = append(states, binding.State)
			if binding.EnvironmentKey != devPostgresEnvironmentKey {
				continue
			}
			out = &api.DevPostgresResponse{
				DatabaseID:     database.ID,
				Name:           database.Name,
				State:          string(database.State),
				BindingID:      binding.ID,
				EnvironmentKey: binding.EnvironmentKey,
			}
		}
		if out != nil {
			out.BindingState = devPostgresBindingsState(states)
		}
		return out, nil
	}
	return nil, nil
}

// cleanupDevPostgres removes only the automatically named database when its
// DATABASE_URL binding belongs to this developer app. A database with the
// same name but no matching binding is never touched, which keeps explicit
// customer-managed attachments safe.
func (s *server) cleanupDevPostgres(ctx context.Context, app state.App) error {
	if app.PreviewOfSlug == "" || app.PreviewPrNumber != 0 || s.managedPostgres == nil || s.managedPostgresBindings == nil {
		return nil
	}
	databases, err := s.managedPostgres.List(ctx, app.AccountID)
	if err != nil {
		return err
	}
	for _, database := range databases {
		if database.Name != app.Slug {
			continue
		}
		bindings, listErr := s.managedPostgresBindings.List(ctx, app.AccountID, database.ID)
		if listErr != nil {
			return listErr
		}
		owned := false
		for _, binding := range bindings {
			if binding.AppID == app.ID && binding.Scope == api.DefaultEnvScope && binding.EnvironmentKey == devPostgresEnvironmentKey {
				owned = true
			}
		}
		if !owned {
			return nil
		}
		// The database's own bindings (devPostgresBindings) go with it; any
		// other binding makes the delete below refuse.
		for _, binding := range bindings {
			if binding.AppID != app.ID || binding.Scope != api.DefaultEnvScope || !isDevPostgresBindingKey(binding.EnvironmentKey) {
				continue
			}
			if _, deleteErr := s.managedPostgresBindings.Delete(ctx, app.AccountID, binding.ID); deleteErr != nil && !errors.Is(deleteErr, managedpostgres.ErrNotFound) {
				return fmt.Errorf("delete dev postgres binding: %w", deleteErr)
			}
		}
		remaining, listErr := s.managedPostgresBindings.List(ctx, app.AccountID, database.ID)
		if listErr != nil {
			return listErr
		}
		if len(remaining) != 0 {
			return managedpostgres.ErrConflict
		}
		if _, err := s.managedPostgres.Delete(ctx, app.AccountID, database.ID); err != nil && !errors.Is(err, managedpostgres.ErrNotFound) {
			return fmt.Errorf("delete dev postgres database: %w", err)
		}
		return nil
	}
	return nil
}

// devPostgresBindingsState is the developer database's binding state as one
// value: ready only when every one of its bindings is, because the first
// release command needs MIGRATION_DATABASE_URL and the first boot needs
// DATABASE_URL (a binding writes its secret before it turns ready). A failed
// binding wins, then any other non-ready state.
func devPostgresBindingsState(states []managedpostgres.BindingState) string {
	if len(states) == 0 {
		return string(managedpostgres.BindingStateProvisioning)
	}
	out := managedpostgres.BindingStateReady
	for _, state := range states {
		switch {
		case state == managedpostgres.BindingStateFailed:
			return string(state)
		case state != managedpostgres.BindingStateReady && out == managedpostgres.BindingStateReady:
			out = state
		}
	}
	return string(out)
}

// isDevPostgresBindingKey reports whether key is one of the developer
// database's own binding keys.
func isDevPostgresBindingKey(key string) bool {
	for _, want := range devPostgresBindings {
		if want.key == key {
			return true
		}
	}
	return false
}
