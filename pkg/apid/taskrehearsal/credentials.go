package taskrehearsal

import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	pgruntime "github.com/onebox-faas/faas/pkg/rehearsalpg/runtime"
	"github.com/onebox-faas/faas/pkg/state"
)

// BindRuntime delivers the restricted Task logins after the contained migration.
// A failed or interrupted grant ends the preparation. It cannot be retried.
func (p *Prepared) BindRuntime(ctx context.Context) error {
	if !p.binding.CompareAndSwap(false, true) {
		return state.ErrConflict
	}
	bind, stop := context.WithDeadline(ctx, p.expiresAt)
	defer stop()
	if err := p.bindRuntime(bind, nil, nil); err != nil {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer cancel()
		return errors.Join(err, p.cleanup(cleanup))
	}
	return nil
}

func (p *Prepared) bindRuntime(ctx context.Context, env *runnerEnvironment, observer *pgruntime.TaskObservation) error {
	for _, role := range workloads {
		if role.name == Migration {
			continue
		}
		app, err := p.provisioner.Store.AppByID(ctx, p.AppID(role.name))
		if err != nil {
			return fmt.Errorf("load rehearsal %s workload: %w", role.name, err)
		}
		if !p.owns(app, role.name) || app.Status == state.AppDeleted {
			return state.ErrConflict
		}
		member, err := p.provisioner.Store.ScenarioTestMemberByApp(ctx, app.ID)
		if err != nil {
			return fmt.Errorf("load rehearsal %s membership: %w", role.name, err)
		}
		if member.AccountID != p.account.ID || member.RunID != p.scenarioID() ||
			member.Workload != role.name || member.NetworkMode != state.ScenarioNetworkContained ||
			member.WebhookMode != state.ScenarioWebhookCaptureMode {
			return state.ErrConflict
		}
		connection, err := p.provisioner.Postgres.BindParticipant(ctx, p.account.ID, member.RunID, app.ID, role.name+".svc.gregale")
		if err != nil {
			return fmt.Errorf("bind rehearsal %s credential: %w", role.name, err)
		}
		if observer != nil && role.name == Observer {
			*observer, err = p.provisioner.Postgres.TaskObservations(ctx, p.account.ID, member.RunID, app.ID, connection)
			if err != nil {
				return fmt.Errorf("open contained Task observer: %w", err)
			}
		}
		if err := p.persistConnection(ctx, app, role.name, connection, env); err != nil {
			return fmt.Errorf("persist rehearsal %s credential", role.name)
		}
	}
	return ctx.Err()
}

// Cleanup disposes of resources using their durable account/run identity.
// The caller must own the run or be recovering it after the executor exits.
func (p *Provisioner) Cleanup(ctx context.Context, accountID, runID string) error {
	target, err := p.target(state.Account{ID: accountID}, runID)
	if err != nil {
		return err
	}
	return target.cleanup(ctx)
}

func (p *Prepared) cleanup(ctx context.Context) error {
	var errs []error
	postgres := p.provisioner.Postgres
	fixture, err := postgres.Catalog.GetScenarioPostgres(ctx, p.account.ID, p.scenarioID())
	switch {
	case err == nil:
		if fixture.AppID != p.AppID(Migration) || fixture.AccountID != p.account.ID || fixture.RunID != p.scenarioID() {
			return state.ErrConflict
		}
		if err := postgres.Delete(ctx, p.account.ID, p.scenarioID()); err != nil {
			errs = append(errs, fmt.Errorf("drop rehearsal database: %w", err))
		}
	case !errors.Is(err, state.ErrNotFound):
		errs = append(errs, fmt.Errorf("load rehearsal database for cleanup: %w", err))
	}
	for _, role := range workloads {
		if err := p.deleteApp(ctx, role.name); err != nil {
			errs = append(errs, fmt.Errorf("clean rehearsal %s workload: %w", role.name, err))
		}
	}
	return errors.Join(errs...)
}

func (p *Prepared) deleteApp(ctx context.Context, name string) error {
	store := p.provisioner.Store
	app, err := store.AppByID(ctx, p.AppID(name))
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if !p.owns(app, name) {
		return state.ErrConflict
	}
	if app.Status != state.AppDeleted {
		if _, err := store.SetPreviewPrState(ctx, app.ID, state.PreviewPrStateTearingDown); err != nil {
			return err
		}
		if _, err := store.SoftDeleteAppCascade(ctx, app.ID); err != nil {
			return err
		}
	}
	if app.PreviewPrState != state.PreviewPrStateTornDown {
		if _, err := store.SetPreviewPrState(ctx, app.ID, state.PreviewPrStateTornDown); err != nil {
			return err
		}
	}
	return p.removeCredentials(ctx, app.ID)
}

func (p *Prepared) removeCredentials(ctx context.Context, appID string) error {
	store := p.provisioner.Store
	secrets, err := store.ListAppSecretsInScope(ctx, p.account.ID, appID, api.DefaultEnvScope)
	if err != nil {
		return err
	}
	var errs []error
	for _, secret := range secrets {
		if secret.Key != databaseKey && secret.SecretClass != api.SecretClassEphemeral {
			continue
		}
		if err := store.DeleteAppSecret(ctx, p.account.ID, appID, secret.Key); err != nil && !errors.Is(err, state.ErrNotFound) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (p *Prepared) persistConnection(ctx context.Context, app state.App, role, connection string, env *runnerEnvironment) error {
	if env == nil {
		return p.provisioner.PersistDatabaseURL(ctx, p.account, app, connection)
	}
	values := env.roles[role]
	key := env.databaseKeys[role]
	values[key] = connection
	defer delete(values, key)
	return p.provisioner.PersistEnvironment(ctx, p.account, app, values)
}
