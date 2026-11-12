// Package secretpg is secretd's Postgres store (ADR-20261110210000 §3).
package secretpg

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/secretd"
	"github.com/onebox-faas/faas/pkg/secretd/secretpg/sqlc"
	"github.com/onebox-faas/faas/pkg/state"
)

// Store reads apps and nodes through the state store and writes secretd's
// grants and audit. db is a pool or connection logged in as faas_secretd.
type Store struct {
	db    sqlc.DBTX
	q     *sqlc.Queries
	state *state.PgStore
}

// New returns a store over db; st reads apps, accounts and nodes.
func New(db sqlc.DBTX, st *state.PgStore) *Store {
	return &Store{db: db, q: sqlc.New(), state: st}
}

var _ secretd.Store = (*Store)(nil)

// NodeIDByName implements secretd.Store: only an active node answers.
func (s *Store) NodeIDByName(ctx context.Context, name string) (string, error) {
	node, err := s.state.ComputeNodeByName(ctx, name)
	if errors.Is(err, state.ErrNotFound) {
		return "", secretd.ErrUnknownNode
	}
	if err != nil {
		return "", fmt.Errorf("secretpg: node by name: %w", err)
	}
	if !node.Active {
		return "", secretd.ErrUnknownNode
	}
	return node.ID, nil
}

// AppTarget implements secretd.Store.
func (s *Store) AppTarget(ctx context.Context, appID string) (secretd.AppTarget, error) {
	app, err := s.state.AppByID(ctx, appID)
	if err != nil {
		return secretd.AppTarget{}, fmt.Errorf("secretpg: app: %w", err)
	}
	account, err := state.AppResourceAccount(ctx, s.state, app)
	if err != nil {
		return secretd.AppTarget{}, fmt.Errorf("secretpg: app resource account: %w", err)
	}
	return secretd.AppTarget{OwnerNodeID: app.NodeID, AccountID: account.ID}, nil
}

func pgUUID(id string) (pgtype.UUID, error) {
	u, err := uuid.Parse(id)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("secretpg: bad id %q: %w", id, err)
	}
	return pgtype.UUID{Bytes: u, Valid: true}, nil
}

func pgTime(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }

// GrantValid implements secretd.Store.
func (s *Store) GrantValid(ctx context.Context, appID, nodeID string, at time.Time) (bool, error) {
	app, err := pgUUID(appID)
	if err != nil {
		return false, err
	}
	node, err := pgUUID(nodeID)
	if err != nil {
		return false, err
	}
	return s.q.SecretGrantValid(ctx, s.db, sqlc.SecretGrantValidParams{AppID: app, NodeID: node, At: pgTime(at)})
}

// UpsertGrant implements secretd.Store.
func (s *Store) UpsertGrant(ctx context.Context, appID, nodeID, grantedByNodeID string, expiresAt time.Time) (time.Time, error) {
	app, err := pgUUID(appID)
	if err != nil {
		return time.Time{}, err
	}
	node, err := pgUUID(nodeID)
	if err != nil {
		return time.Time{}, err
	}
	by, err := pgUUID(grantedByNodeID)
	if err != nil {
		return time.Time{}, err
	}
	got, err := s.q.UpsertSecretGrant(ctx, s.db, sqlc.UpsertSecretGrantParams{
		AppID: app, NodeID: node, GrantedByNodeID: by, ExpiresAt: pgTime(expiresAt),
	})
	if err != nil {
		return time.Time{}, fmt.Errorf("secretpg: upsert grant: %w", err)
	}
	return got.Time, nil
}

// RecordOpen implements secretd.Store.
func (s *Store) RecordOpen(ctx context.Context, nodeID, appID, via string, entries int) error {
	app, err := pgUUID(appID)
	if err != nil {
		return err
	}
	node, err := pgUUID(nodeID)
	if err != nil {
		return err
	}
	if err := s.q.InsertSecretOpenAudit(ctx, s.db, sqlc.InsertSecretOpenAuditParams{
		NodeID: node, AppID: app, Via: via, Entries: int32(min(entries, 1<<30)),
	}); err != nil {
		return fmt.Errorf("secretpg: record open: %w", err)
	}
	return nil
}

// CaptureKey implements secretd.Store: a live sealed capture's app and key.
func (s *Store) CaptureKey(ctx context.Context, captureID string) (string, []byte, error) {
	id, err := pgUUID(captureID)
	if err != nil {
		return "", nil, secretd.ErrUnknownCapture
	}
	row, err := s.q.CrashCaptureSealedKey(ctx, s.db, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, secretd.ErrUnknownCapture
	}
	if err != nil {
		return "", nil, fmt.Errorf("secretpg: capture key: %w", err)
	}
	return uuid.UUID(row.AppID.Bytes).String(), row.SealedKey, nil
}

// SidecarEnv implements secretd.Store: the deployment must be appID's.
func (s *Store) SidecarEnv(ctx context.Context, appID, deploymentID, sidecarName string) (map[string][]byte, error) {
	if _, err := uuid.Parse(deploymentID); err != nil {
		return nil, secretd.ErrUnknownSidecar
	}
	dep, err := s.state.DeploymentByID(ctx, deploymentID)
	if errors.Is(err, state.ErrNotFound) {
		return nil, secretd.ErrUnknownSidecar
	}
	if err != nil {
		return nil, fmt.Errorf("secretpg: deployment: %w", err)
	}
	if dep.AppID != appID {
		return nil, secretd.ErrUnknownSidecar
	}
	rows, err := s.state.ListDeploymentSidecarEnvReseals(ctx, deploymentID)
	if err != nil {
		return nil, fmt.Errorf("secretpg: sidecar env reseals: %w", err)
	}
	reseals := make([]secretd.SidecarEnvReseal, 0, len(rows))
	for _, r := range rows {
		reseals = append(reseals, secretd.SidecarEnvReseal{SidecarName: r.SidecarName, Key: r.Key,
			SourceSHA256: r.SourceSHA256, Ciphertext: r.Ciphertext})
	}
	return secretd.SidecarCiphertexts(dep.Sidecars, sidecarName, reseals)
}

// AppSecretCiphertext implements secretd.Store: the app's own row only.
func (s *Store) AppSecretCiphertext(ctx context.Context, appID, scope, key string) ([]byte, error) {
	app, err := pgUUID(appID)
	if err != nil {
		return nil, secretd.ErrUnknownSecret
	}
	ct, err := s.q.AppSecretCiphertext(ctx, s.db, sqlc.AppSecretCiphertextParams{AppID: app, Scope: scope, Key: key})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, secretd.ErrUnknownSecret
	}
	if err != nil {
		return nil, fmt.Errorf("secretpg: app secret: %w", err)
	}
	return ct, nil
}

// LogDrainHeader implements secretd.Store: a drain of appID only. A drain
// with no header is ErrUnknownDrain too: there is nothing to open.
func (s *Store) LogDrainHeader(ctx context.Context, appID, drainID string) ([]byte, error) {
	app, err := pgUUID(appID)
	if err != nil {
		return nil, secretd.ErrUnknownDrain
	}
	drain, err := pgUUID(drainID)
	if err != nil {
		return nil, secretd.ErrUnknownDrain
	}
	sealed, err := s.q.LogDrainHeaderSealed(ctx, s.db, sqlc.LogDrainHeaderSealedParams{ID: drain, AppID: app})
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && len(sealed) == 0) {
		return nil, secretd.ErrUnknownDrain
	}
	if err != nil {
		return nil, fmt.Errorf("secretpg: log drain header: %w", err)
	}
	return sealed, nil
}

// EdgeHeaderSecret implements secretd.Store: a value of appID that one of
// appID's headers rules references.
func (s *Store) EdgeHeaderSecret(ctx context.Context, appID, secretID string) ([]byte, error) {
	app, err := pgUUID(appID)
	if err != nil {
		return nil, secretd.ErrUnknownEdgeHeaderSecret
	}
	id, err := pgUUID(secretID)
	if err != nil {
		return nil, secretd.ErrUnknownEdgeHeaderSecret
	}
	sealed, err := s.q.EdgeHeaderSecretSealed(ctx, s.db, sqlc.EdgeHeaderSecretSealedParams{ID: id, AppID: app})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, secretd.ErrUnknownEdgeHeaderSecret
	}
	if err != nil {
		return nil, fmt.Errorf("secretpg: edge header secret: %w", err)
	}
	return sealed, nil
}

// RegistryCredential implements secretd.Store: appID's own credential for
// registry.
func (s *Store) RegistryCredential(ctx context.Context, appID, registry string) (string, []byte, error) {
	app, err := pgUUID(appID)
	if err != nil || registry == "" {
		return "", nil, secretd.ErrUnknownRegistryCredential
	}
	row, err := s.q.RegistryCredentialSealed(ctx, s.db, sqlc.RegistryCredentialSealedParams{AppID: app, Registry: registry})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, secretd.ErrUnknownRegistryCredential
	}
	if err != nil {
		return "", nil, fmt.Errorf("secretpg: registry credential: %w", err)
	}
	return row.Username, row.PasswordEncrypted, nil
}

// PreparesImage implements secretd.Store.
func (s *Store) PreparesImage(ctx context.Context, appID, nodeName string, since time.Time) (bool, error) {
	app, perr := pgUUID(appID)
	if perr != nil || nodeName == "" {
		return false, nil //nolint:nilerr // a malformed app id prepares nothing
	}
	ok, err := s.q.NodePreparesAppImage(ctx, s.db, sqlc.NodePreparesAppImageParams{AppID: app, NodeName: nodeName,
		Since: pgtype.Timestamptz{Time: since, Valid: true}})
	if err != nil {
		return false, fmt.Errorf("secretpg: image preparation: %w", err)
	}
	return ok, nil
}

// TriggerConfig implements secretd.Store: a trigger of appID only.
func (s *Store) TriggerConfig(ctx context.Context, appID, triggerID string) (string, []byte, error) {
	app, err := pgUUID(appID)
	if err != nil {
		return "", nil, secretd.ErrUnknownTrigger
	}
	trigger, err := pgUUID(triggerID)
	if err != nil {
		return "", nil, secretd.ErrUnknownTrigger
	}
	row, err := s.q.TriggerConfig(ctx, s.db, sqlc.TriggerConfigParams{ID: trigger, AppID: app})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, secretd.ErrUnknownTrigger
	}
	if err != nil {
		return "", nil, fmt.Errorf("secretpg: trigger config: %w", err)
	}
	return row.Kind, row.Config, nil
}

// DeleteExpiredGrants removes grants that expired before before.
func (s *Store) DeleteExpiredGrants(ctx context.Context, before time.Time) (int64, error) {
	return s.q.DeleteExpiredSecretGrants(ctx, s.db, pgTime(before))
}
