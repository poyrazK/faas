// Package previewdb gives PR preview environments their own managed
// PostgreSQL databases (ADR-20261102123523). For each production database
// with previews on, it sets up the environment's database, binds the preview
// to it with the production bindings' environment keys and access, and
// removes both with the preview. It lives outside apidserver per ADR-982;
// apidserver wires it into the preview build paths and teardown.
package previewdb

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

// Databases is the managed PostgreSQL service previewdb uses.
type Databases interface {
	List(ctx context.Context, accountID string) ([]managedpostgres.Database, error)
	EnsurePreviewDatabase(ctx context.Context, request managedpostgres.PreviewDatabaseRequest) (managedpostgres.Database, error)
	PreviewDatabases(ctx context.Context, accountID, environmentID string, prNumber int) ([]managedpostgres.Database, error)
	DeletePreviewDatabase(ctx context.Context, accountID, databaseID string) (managedpostgres.Database, error)
	PreviewDatabasePage(ctx context.Context, afterID string, limit int) ([]managedpostgres.Database, error)
}

// Bindings is the managed PostgreSQL binding service previewdb uses.
type Bindings interface {
	List(ctx context.Context, accountID, databaseID string) ([]managedpostgres.Binding, error)
	CreateWithResult(ctx context.Context, request managedpostgres.CreateBindingRequest) (managedpostgres.Binding, bool, error)
	Delete(ctx context.Context, accountID, bindingID string) (managedpostgres.Binding, error)
}

// Manager sets up and releases preview databases.
type Manager struct {
	Databases Databases
	Bindings  Bindings
}

// ErrPending reports a preview database or binding that is still
// provisioning. The reconcilers finish it; a build that needs it waits.
var ErrPending = errors.New("previewdb: a preview database is still provisioning")

// RefusedError is a setup a retry cannot fix until the plan, the setting or
// the account changes. Reason is written to the pull request, which can be
// public, so it names no database and no account state.
type RefusedError struct {
	Reason string
	Err    error
}

func (e *RefusedError) Error() string { return "previewdb: " + e.Reason + ": " + e.Err.Error() }

func (e *RefusedError) Unwrap() error { return e.Err }

// Environment names the PR preview environment preview belongs to: its
// project, or for a legacy preview without one, the preview itself, an
// environment of one. ok is false for anything that is not a PR preview.
func Environment(preview state.App) (id string, prNumber int, ok bool) {
	if preview.PreviewOfSlug == "" || preview.PreviewPrNumber <= 0 {
		return "", 0, false
	}
	if preview.ProjectID != "" {
		return preview.ProjectID, preview.PreviewPrNumber, true
	}
	return preview.ID, preview.PreviewPrNumber, true
}

// Prepare sets up preview's databases from parent's bindings. It returns
// ErrPending while one is provisioning, a *RefusedError when setup is
// refused, and any other error when a retry may succeed.
func (m Manager) Prepare(ctx context.Context, plan api.Plan, parent, preview state.App) error {
	environmentID, prNumber, ok := Environment(preview)
	if !ok || parent.ID == preview.ID || parent.AccountID != preview.AccountID || parent.Slug != preview.PreviewOfSlug {
		return nil
	}
	databases, err := m.Databases.List(ctx, parent.AccountID)
	if err != nil {
		return fmt.Errorf("previewdb: list databases: %w", err)
	}
	pending := false
	for _, source := range databases {
		if !previewSource(source) {
			continue
		}
		wanted, err := m.productionBindings(ctx, parent, source)
		if err != nil {
			return err
		}
		if len(wanted) == 0 {
			continue
		}
		database, err := m.Databases.EnsurePreviewDatabase(ctx, managedpostgres.PreviewDatabaseRequest{
			AccountID:         preview.AccountID,
			Owner:             managedpostgres.PreviewOwner{SourceDatabaseID: source.ID, EnvironmentID: environmentID, PRNumber: prNumber},
			StorageLimitBytes: StorageLimitBytes(plan),
			Limit:             api.ManagedPostgresPreviewDatabasesMax(plan),
		})
		if errors.Is(err, managedpostgres.ErrPreviewsOff) {
			continue // turned off since the list
		}
		if err != nil {
			return refusal(err)
		}
		if database.State == managedpostgres.StateDeleting {
			// A delete is under way; the next attempt sets up a new one.
			pending = true
			continue
		}
		for _, production := range wanted {
			binding, _, err := m.Bindings.CreateWithResult(ctx, managedpostgres.CreateBindingRequest{
				AccountID: preview.AccountID, DatabaseID: database.ID, AppID: preview.ID,
				Scope: api.DefaultEnvScope, EnvironmentKey: production.EnvironmentKey, Access: production.Access, PreviewSetup: true,
			})
			if err != nil && !stillBinding(err, binding, database.ID, preview.ID) {
				return refusal(err)
			}
			if err != nil || binding.State != managedpostgres.BindingStateReady {
				pending = true
			}
		}
	}
	if pending {
		return ErrPending
	}
	return nil
}

// stillBinding reports whether err is the ErrConflict that a binding the
// reconciler holds, or whose next attempt is not due, comes back with: the
// preview's binding is still provisioning.
func stillBinding(err error, binding managedpostgres.Binding, databaseID, appID string) bool {
	return errors.Is(err, managedpostgres.ErrConflict) && binding.DatabaseID == databaseID && binding.AppID == appID
}

// Release removes preview's bindings to its environment's preview databases,
// and purges each one no other preview still binds. A database whose last
// bindings are still being revoked is left for a later teardown or sweep.
func (m Manager) Release(ctx context.Context, preview state.App) error {
	environmentID, prNumber, ok := Environment(preview)
	if !ok {
		return nil
	}
	databases, err := m.Databases.PreviewDatabases(ctx, preview.AccountID, environmentID, prNumber)
	if err != nil {
		return fmt.Errorf("previewdb: list preview databases: %w", err)
	}
	for _, database := range databases {
		if err := m.release(ctx, preview, database); err != nil {
			return err
		}
	}
	return nil
}

// Purge deletes a PR preview environment's database at once, its bindings
// first: a customer's delete of one, which the next preview build sets up
// again, has no undo window.
func (m Manager) Purge(ctx context.Context, accountID, databaseID string) (managedpostgres.Database, error) {
	bindings, err := m.Bindings.List(ctx, accountID, databaseID)
	if err != nil {
		return managedpostgres.Database{}, err
	}
	for _, binding := range bindings {
		if _, err := m.Bindings.Delete(ctx, accountID, binding.ID); err != nil && !errors.Is(err, managedpostgres.ErrNotFound) {
			return managedpostgres.Database{}, err
		}
	}
	return m.Databases.DeletePreviewDatabase(ctx, accountID, databaseID)
}

// Previews lists an account's undeleted PR previews.
type Previews interface {
	ListPreviewsForAccount(ctx context.Context, accountID string) ([]state.App, error)
}

type environment struct {
	id       string
	prNumber int
}

// Sweep purges the databases among page whose PR environment has no
// undeleted preview left: a teardown that stopped midway, or one an older
// apid ran. A database set up at or after setupBefore is left for a later
// sweep, so one the preview listing may not show yet keeps its database. It
// returns how many it purged.
func (m Manager) Sweep(ctx context.Context, previews Previews, page []managedpostgres.Database, setupBefore time.Time) (int, error) {
	open := map[string]map[environment]bool{}
	purged := 0
	var errs error
	for _, database := range page {
		if database.Preview.IsZero() || database.State == managedpostgres.StateDeleting || database.State == managedpostgres.StateDeleted ||
			!database.CreatedAt.Before(setupBefore) {
			continue
		}
		environments, listed := open[database.AccountID]
		if !listed {
			var err error
			if environments, err = openEnvironments(ctx, previews, database.AccountID); err != nil {
				errs = errors.Join(errs, err)
			}
			open[database.AccountID] = environments
		}
		// An account whose previews could not be listed keeps its databases.
		if environments == nil || environments[environment{database.Preview.EnvironmentID, database.Preview.PRNumber}] {
			continue
		}
		_, err := m.Purge(ctx, database.AccountID, database.ID)
		switch {
		case err == nil:
			purged++
		case errors.Is(err, managedpostgres.ErrNotFound), errors.Is(err, managedpostgres.ErrConflict):
			// Already gone, or its bindings are still being revoked.
		default:
			errs = errors.Join(errs, fmt.Errorf("previewdb: purge preview database %s: %w", database.ID, err))
		}
	}
	return purged, errs
}

// SweepAll pages through every preview database and sweeps each page.
func (m Manager) SweepAll(ctx context.Context, previews Previews, setupBefore time.Time) (int, error) {
	purged := 0
	var errs error
	for after := ""; ; {
		if err := ctx.Err(); err != nil {
			return purged, errors.Join(errs, err)
		}
		page, err := m.Databases.PreviewDatabasePage(ctx, after, sweepPageSize)
		if err != nil {
			return purged, errors.Join(errs, fmt.Errorf("previewdb: list preview databases: %w", err))
		}
		n, err := m.Sweep(ctx, previews, page, setupBefore)
		purged, errs = purged+n, errors.Join(errs, err)
		if len(page) < sweepPageSize {
			return purged, errs
		}
		after = page[len(page)-1].ID
	}
}

const (
	// SweepInterval is how often RunSweep looks for preview databases
	// whose PR environment closed. The preview's own teardown purges
	// them; the sweep only finishes teardowns that stopped midway.
	SweepInterval = 10 * time.Minute
	// sweepStartupDelay lets apid's startup settle before the first sweep.
	sweepStartupDelay = 2 * time.Minute
	// sweepSetupGrace leaves a database set up this recently alone.
	sweepSetupGrace = 10 * time.Minute
	sweepPageSize   = 100
)

// RunSweep sweeps every SweepInterval until ctx ends. Each apid replica may
// run it: a purge is idempotent.
func RunSweep(ctx context.Context, m Manager, previews Previews, log *slog.Logger) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	timer := time.NewTimer(sweepStartupDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		purged, err := m.SweepAll(ctx, previews, time.Now().Add(-sweepSetupGrace))
		switch {
		case err != nil && ctx.Err() == nil:
			log.Warn("preview database sweep failed", "purged", purged, "err", err)
		case purged > 0:
			log.Info("preview database sweep purged the databases of closed PR environments", "purged", purged)
		}
		timer.Reset(SweepInterval)
	}
}

func openEnvironments(ctx context.Context, previews Previews, accountID string) (map[environment]bool, error) {
	apps, err := previews.ListPreviewsForAccount(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("previewdb: list previews of account %s: %w", accountID, err)
	}
	environments := map[environment]bool{}
	for _, app := range apps {
		if id, prNumber, ok := Environment(app); ok {
			environments[environment{id, prNumber}] = true
		}
	}
	return environments, nil
}

// PurgeCopies purges the preview databases copied from source, before source
// is deleted: on Neon a copy is a branch of its source's project and goes
// with it. Empty preview databases do not depend on their source and stay
// with their PR.
func (m Manager) PurgeCopies(ctx context.Context, accountID, sourceID string) error {
	databases, err := m.Databases.List(ctx, accountID)
	if err != nil {
		return fmt.Errorf("previewdb: list databases: %w", err)
	}
	for _, database := range databases {
		if database.Preview.SourceDatabaseID != sourceID || database.RestoreSourceDatabaseID != sourceID || database.State == managedpostgres.StateDeleted {
			continue
		}
		if _, err := m.Purge(ctx, accountID, database.ID); err != nil && !errors.Is(err, managedpostgres.ErrNotFound) {
			return fmt.Errorf("previewdb: purge preview copy: %w", err)
		}
	}
	return nil
}

func (m Manager) release(ctx context.Context, preview state.App, database managedpostgres.Database) error {
	bindings, err := m.Bindings.List(ctx, preview.AccountID, database.ID)
	if err != nil && !errors.Is(err, managedpostgres.ErrNotFound) {
		return fmt.Errorf("previewdb: list bindings: %w", err)
	}
	others := 0
	for _, binding := range bindings {
		if binding.AppID != preview.ID {
			others++
			continue
		}
		if _, err := m.Bindings.Delete(ctx, preview.AccountID, binding.ID); err != nil && !errors.Is(err, managedpostgres.ErrNotFound) {
			return fmt.Errorf("previewdb: delete binding: %w", err)
		}
	}
	if others > 0 || database.State == managedpostgres.StateDeleted {
		return nil
	}
	_, err = m.Databases.DeletePreviewDatabase(ctx, preview.AccountID, database.ID)
	if err == nil || errors.Is(err, managedpostgres.ErrNotFound) || errors.Is(err, managedpostgres.ErrConflict) {
		return nil
	}
	return fmt.Errorf("previewdb: delete preview database: %w", err)
}

// productionBindings returns the bindings a preview of parent copies from
// source: one per environment key across the scopes production serves, the
// default scope's first.
func (m Manager) productionBindings(ctx context.Context, parent state.App, source managedpostgres.Database) ([]managedpostgres.Binding, error) {
	bindings, err := m.Bindings.List(ctx, parent.AccountID, source.ID)
	if err != nil {
		return nil, fmt.Errorf("previewdb: list production bindings: %w", err)
	}
	var wanted []managedpostgres.Binding
	seen := map[string]bool{}
	for _, scope := range []string{api.DefaultEnvScope, "production"} {
		for _, binding := range bindings {
			if binding.AppID != parent.ID || binding.Scope != scope || seen[binding.EnvironmentKey] ||
				binding.State == managedpostgres.BindingStateDeleting || binding.State == managedpostgres.BindingStateDeleted {
				continue
			}
			seen[binding.EnvironmentKey] = true
			wanted = append(wanted, binding)
		}
	}
	return wanted, nil
}

// StorageLimitBytes is a preview database's storage limit on plan:
// api.ManagedPostgresPreviewStorageLimitBytes or the plan's pool, whichever
// is smaller.
func StorageLimitBytes(plan api.Plan) int64 {
	limits, ok := api.ManagedPostgresLimitsFor(plan)
	if !ok {
		return 0
	}
	return min(api.ManagedPostgresPreviewStorageLimitBytes, limits.StorageLimitBytes)
}

func previewSource(source managedpostgres.Database) bool {
	return (source.Previews == managedpostgres.PreviewsEmpty || source.Previews == managedpostgres.PreviewsCopy) &&
		source.Preview.IsZero() && source.State != managedpostgres.StateDeleting && source.State != managedpostgres.StateDeleted
}

// refusal sorts a setup error: a provider or rollout outage is retried, and
// anything else waits for the plan, the setting or the account to change.
func refusal(err error) error {
	switch {
	case errors.Is(err, managedpostgres.ErrUnavailable):
		return fmt.Errorf("previewdb: set up preview database: %w", err)
	case errors.Is(err, managedpostgres.ErrPreviewDatabaseLimit):
		return &RefusedError{Reason: "the plan's PR preview databases are all in use; close a pull request or tear a preview down", Err: err}
	case errors.Is(err, managedpostgres.ErrQuotaExceeded):
		return &RefusedError{Reason: "the plan does not include a PR preview database", Err: err}
	case errors.Is(err, managedpostgres.ErrPreviewCopyUnsupported):
		return &RefusedError{Reason: "its production database cannot be copied: it has no restore window", Err: err}
	case errors.Is(err, managedpostgres.ErrConflict):
		return &RefusedError{Reason: "its PR preview database conflicts with an existing database or binding", Err: err}
	default:
		return &RefusedError{Reason: "its PR preview database could not be set up", Err: err}
	}
}
