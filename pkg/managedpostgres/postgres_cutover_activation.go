package managedpostgres

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ CutoverActivationStore = (*PostgresStore)(nil)

// ActivateCutover moves the app's bindings to the cutover's target in one
// transaction (ADR-20261023181931). It takes the account, app, cutover,
// database and binding locks in the order every cutover path uses, then
// requires the fence the switch installed, fresh verification, a drain plan
// whose members are all retired or parked, unchanged databases and bindings,
// and the expected live release. It publishes the target credentials to
// app_secrets under the source keys and hands the source bindings to the
// binding reconciler for revocation.
func (s *PostgresStore) ActivateCutover(ctx context.Context, a CutoverActivation) (ActivatedCutover, error) {
	if a.AccountID == "" || a.CutoverID == "" || a.FencedAt.IsZero() || a.LiveDeploymentID == "" || a.Now.IsZero() {
		return ActivatedCutover{}, ErrInvalid
	}
	q := sqlc.New()
	initial, err := q.GetManagedPostgresCutover(ctx, s.catalog, sqlc.GetManagedPostgresCutoverParams{AccountID: a.AccountID, ID: a.CutoverID})
	if err != nil {
		return ActivatedCutover{}, mapPostgresError(err)
	}
	tx, err := s.catalog.Begin(ctx)
	if err != nil {
		return ActivatedCutover{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	c, err := lockCutoverForActivation(ctx, tx, q, a, cutoverUUID(initial.AppID))
	if err != nil {
		return ActivatedCutover{}, err
	}
	if err := checkActivationDatabases(ctx, tx, q, c); err != nil {
		return ActivatedCutover{}, err
	}
	sources, err := lockActivationSourceBindings(ctx, tx, q, c)
	if err != nil {
		return ActivatedCutover{}, err
	}
	if err := checkActivationDrain(ctx, tx, q, c, a.FencedAt); err != nil {
		return ActivatedCutover{}, err
	}
	if err := checkActivationRelease(ctx, tx, q, c, a.LiveDeploymentID); err != nil {
		return ActivatedCutover{}, err
	}
	out, err := publishActivatedCutover(ctx, tx, q, c, sources, a.Now)
	if err != nil {
		return ActivatedCutover{}, err
	}
	return out, mapPostgresError(tx.Commit(ctx))
}

func lockCutoverForActivation(ctx context.Context, tx pgx.Tx, q *sqlc.Queries, a CutoverActivation, appID string) (Cutover, error) {
	owner, err := q.LockManagedPostgresCutoverAccount(ctx, tx, a.AccountID)
	if err != nil {
		return Cutover{}, mapPostgresError(err)
	}
	app, err := q.LockManagedPostgresCutoverAdmissionApp(ctx, tx, appID)
	if err != nil {
		return Cutover{}, mapPostgresError(err)
	}
	if owner == "deleted_pending" || app.AccountID != a.AccountID || (app.Status != "active" && app.Status != "evicted_cold") {
		return Cutover{}, ErrConflict
	}
	if !app.ManagedPostgresAdmissionCutoverID.Valid || cutoverUUID(app.ManagedPostgresAdmissionCutoverID) != a.CutoverID ||
		!app.ManagedPostgresAdmissionFencedAt.Valid || !app.ManagedPostgresAdmissionFencedAt.Time.Equal(a.FencedAt) {
		return Cutover{}, activationBlocked(ActivationFenceChanged)
	}
	r, err := q.LockManagedPostgresCutoverForVerification(ctx, tx, sqlc.LockManagedPostgresCutoverForVerificationParams{AccountID: a.AccountID, ID: a.CutoverID})
	if err != nil {
		return Cutover{}, mapPostgresError(err)
	}
	c, err := loadCutoverCredentials(ctx, tx, cutoverFromRow(r))
	if err != nil {
		return Cutover{}, err
	}
	if c.State != CutoverVerified || c.LeaseToken != "" || c.AppID != appID {
		return Cutover{}, activationBlocked(ActivationNotVerified)
	}
	if !c.VerificationFresh(a.Now) {
		return Cutover{}, activationBlocked(ActivationVerificationStale)
	}
	fences, err := q.ManagedPostgresCutoverUnreleasedSourceFences(ctx, tx, c.ID)
	if err != nil {
		return Cutover{}, mapPostgresError(err)
	}
	if fences > 0 {
		return Cutover{}, activationBlocked(ActivationSourceFenced)
	}
	return c, nil
}

func checkActivationDatabases(ctx context.Context, tx pgx.Tx, q *sqlc.Queries, c Cutover) error {
	rows, err := q.LockManagedPostgresCutoverDatabases(ctx, tx, []string{c.Source.ID, c.Target.ID})
	if err != nil {
		return mapPostgresError(err)
	}
	if len(rows) != 2 {
		return activationBlocked(ActivationDatabasesChanged)
	}
	for _, row := range rows {
		want := c.Source
		if cutoverUUID(row.ID) == c.Target.ID {
			want = c.Target
		}
		if row.State != string(StateReady) || cutoverUUID(row.CutoverID) != c.ID || row.BackendID != want.BackendID ||
			row.BackendFingerprint != want.BackendFingerprint || row.ProviderResourceID.String != want.ProviderResourceID ||
			row.DesiredGeneration != want.DesiredGeneration {
			return activationBlocked(ActivationDatabasesChanged)
		}
	}
	return nil
}

func lockActivationSourceBindings(ctx context.Context, tx pgx.Tx, q *sqlc.Queries, c Cutover) ([]sqlc.ManagedPostgresBinding, error) {
	rows, err := q.LockManagedPostgresCutoverBindings(ctx, tx, sqlc.LockManagedPostgresCutoverBindingsParams{
		AccountID: c.AccountID, DatabaseID: c.Source.ID, AppID: c.AppID, Scope: c.Scope})
	if err != nil {
		return nil, mapPostgresError(err)
	}
	if len(rows) != len(c.Credentials) {
		return nil, activationBlocked(ActivationBindingsChanged)
	}
	byID := map[string]sqlc.ManagedPostgresBinding{}
	for _, row := range rows {
		byID[cutoverUUID(row.ID)] = row
	}
	sources := make([]sqlc.ManagedPostgresBinding, 0, len(c.Credentials))
	for _, m := range c.Credentials {
		row, ok := byID[m.SourceBindingID]
		if !ok || row.State != string(BindingStateReady) || cutoverUUID(row.CutoverID) != c.ID ||
			row.CredentialGeneration != m.SourceCredentialGeneration || row.EnvironmentKey != m.EnvironmentKey ||
			row.Access != string(m.Access) || row.RotationPreviousGeneration.Valid {
			return nil, activationBlocked(ActivationBindingsChanged)
		}
		sources = append(sources, row)
	}
	return sources, nil
}

// checkActivationDrain applies the rollback bar (ADR-20261023181931): every
// captured member is retired with its native receipt or parked, and the
// plan carries no blocker other than an empty inventory.
func checkActivationDrain(ctx context.Context, tx pgx.Tx, q *sqlc.Queries, c Cutover, fencedAt time.Time) error {
	id, err := q.ManagedPostgresCutoverDrainPlanForFence(ctx, tx, sqlc.ManagedPostgresCutoverDrainPlanForFenceParams{
		CutoverID: c.ID, AppID: c.AppID, FencedAt: healthTimestamp(fencedAt)})
	if errors.Is(err, pgx.ErrNoRows) {
		return activationBlocked(ActivationDrainIncomplete)
	}
	if err != nil {
		return mapPostgresError(err)
	}
	plan, err := state.ObserveManagedPostgresDrainPlan(ctx, tx, id)
	if err != nil {
		return mapPostgresError(err)
	}
	if !RollbackDrainComplete(plan) {
		return activationBlocked(ActivationDrainIncomplete)
	}
	return nil
}

// RollbackDrainComplete reports whether a drain plan meets the rollback bar.
func RollbackDrainComplete(plan state.ManagedPostgresDrainPlan) bool {
	for _, blocker := range plan.Blockers {
		if blocker != "inventory_empty" {
			return false
		}
	}
	for _, m := range plan.Members {
		if m.State != "retired" && m.BlockerCode != "parked_instance_unproven" {
			return false
		}
	}
	return true
}

func checkActivationRelease(ctx context.Context, tx pgx.Tx, q *sqlc.Queries, c Cutover, liveID string) error {
	rows, err := q.ManagedPostgresActivationLiveDeployments(ctx, tx, sqlc.ManagedPostgresActivationLiveDeploymentsParams{AppID: c.AppID, Scope: c.Scope})
	if err != nil {
		return mapPostgresError(err)
	}
	if len(rows) != 1 || rows[0].ID != liveID || rows[0].TrafficPercent != 100 {
		return activationBlocked(ActivationReleaseChanged)
	}
	return nil
}

func publishActivatedCutover(ctx context.Context, tx pgx.Tx, q *sqlc.Queries, c Cutover, sources []sqlc.ManagedPostgresBinding, now time.Time) (ActivatedCutover, error) {
	n, err := q.ActivateManagedPostgresCutover(ctx, tx, sqlc.ActivateManagedPostgresCutoverParams{Now: healthTimestamp(now), ID: c.ID, AccountID: c.AccountID})
	if err != nil {
		return ActivatedCutover{}, mapPostgresError(err)
	}
	if n != 1 {
		return ActivatedCutover{}, activationBlocked(ActivationNotVerified)
	}
	// The triggers let an activated cutover release its pins, like a
	// cancelled one; the bindings below need both databases unpinned.
	if err := q.UnpinManagedPostgresCutoverDatabases(ctx, tx, c.ID); err != nil {
		return ActivatedCutover{}, mapPostgresError(err)
	}
	if err := q.UnpinManagedPostgresCutoverBindings(ctx, tx, c.ID); err != nil {
		return ActivatedCutover{}, mapPostgresError(err)
	}
	out := ActivatedCutover{SecretsPublished: true}
	for i, m := range c.Credentials {
		source := sources[i]
		if err := q.LockManagedPostgresActivationSecretTarget(ctx, tx, sqlc.LockManagedPostgresActivationSecretTargetParams{
			AppID: c.AppID, Scope: c.Scope, Key: m.EnvironmentKey}); err != nil {
			return ActivatedCutover{}, mapPostgresError(err)
		}
		if _, err := q.DeleteManagedPostgresBindingSecrets(ctx, tx, cutoverUUID(source.ID)); err != nil {
			return ActivatedCutover{}, mapPostgresError(err)
		}
		retired, err := q.RetireManagedPostgresCutoverSourceBinding(ctx, tx, sqlc.RetireManagedPostgresCutoverSourceBindingParams{Now: healthTimestamp(now), ID: cutoverUUID(source.ID)})
		if err != nil {
			return ActivatedCutover{}, mapPostgresError(err)
		}
		if retired != 1 {
			return ActivatedCutover{}, activationBlocked(ActivationBindingsChanged)
		}
		published := cutoverBinding(c, m)
		published.State, published.ProviderIdentityID, published.CredentialRef = BindingStateReady, m.Sealed.ProviderIdentityID, m.Sealed.Ref
		if err := q.InsertActivatedManagedPostgresBinding(ctx, tx, sqlc.InsertActivatedManagedPostgresBindingParams{
			ID: published.ID, AccountID: c.AccountID, DatabaseID: c.Target.ID, AppID: c.AppID, Scope: c.Scope,
			EnvironmentKey: m.EnvironmentKey, Access: string(m.Access), ProviderIdentityID: m.Sealed.ProviderIdentityID,
			CredentialRef: m.Sealed.Ref, Now: healthTimestamp(now)}); err != nil {
			return ActivatedCutover{}, mapPostgresError(err)
		}
		if err := q.InsertActivatedManagedPostgresSecret(ctx, tx, sqlc.InsertActivatedManagedPostgresSecretParams{
			AccountID: c.AccountID, AppID: c.AppID, Scope: c.Scope, Key: m.EnvironmentKey, Ciphertext: m.Sealed.Ciphertext,
			Kid: m.Sealed.Kid, ValueHash: m.Sealed.ValueHash, BindingID: published.ID, CredentialRef: m.Sealed.Ref}); err != nil {
			return ActivatedCutover{}, mapPostgresError(err)
		}
		out.RetiredSources = append(out.RetiredSources, Binding{ID: cutoverUUID(source.ID), AccountID: c.AccountID, AppID: c.AppID,
			DatabaseID: c.Source.ID, Scope: c.Scope, EnvironmentKey: source.EnvironmentKey, Access: CredentialAccess(source.Access),
			CredentialGeneration: source.CredentialGeneration, State: BindingStateDeleting})
		out.Published = append(out.Published, published)
	}
	published, err := q.PublishManagedPostgresCutoverCredentials(ctx, tx, c.ID)
	if err != nil {
		return ActivatedCutover{}, mapPostgresError(err)
	}
	if int(published) != len(c.Credentials) || slices.ContainsFunc(c.Credentials, func(m CutoverCredential) bool { return m.State != "sealed" }) {
		return ActivatedCutover{}, activationBlocked(ActivationNotVerified)
	}
	if err := q.StampManagedPostgresActivationRuntimeConfig(ctx, tx, c.AppID); err != nil {
		return ActivatedCutover{}, mapPostgresError(err)
	}
	return out, nil
}

// ReleaseCutoverAdmission lifts the fence of an activated cutover. An app
// already released, or fenced by another cutover, is left as it is.
func (s *PostgresStore) ReleaseCutoverAdmission(ctx context.Context, account, id string) error {
	q := sqlc.New()
	initial, err := q.GetManagedPostgresCutover(ctx, s.catalog, sqlc.GetManagedPostgresCutoverParams{AccountID: account, ID: id})
	if err != nil {
		return mapPostgresError(err)
	}
	if CutoverState(initial.State) != CutoverActivated {
		return ErrConflict
	}
	tx, err := s.catalog.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := q.LockManagedPostgresCutoverAccount(ctx, tx, account); err != nil {
		return mapPostgresError(err)
	}
	if _, err := q.LockManagedPostgresCutoverAdmissionApp(ctx, tx, cutoverUUID(initial.AppID)); err != nil {
		return mapPostgresError(err)
	}
	if _, err := q.ReleaseActivatedManagedPostgresCutoverAdmission(ctx, tx, sqlc.ReleaseActivatedManagedPostgresCutoverAdmissionParams{
		AppID: cutoverUUID(initial.AppID), CutoverID: id}); err != nil {
		return mapPostgresError(err)
	}
	return mapPostgresError(tx.Commit(ctx))
}
