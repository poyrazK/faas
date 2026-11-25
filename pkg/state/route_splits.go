package state

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Route splits (ADR-20261121170000 amendment 2). Applying a split creates a
// second app from the source app's live build, routes one path to it with an
// edge rule and, for a memory split, sizes the source app down. apid writes
// the row and its worker moves the phase forward.

// Route split phases.
const (
	// RouteSplitCreating: the row is recorded and the target app is being
	// created under TargetSlug.
	RouteSplitCreating = "creating"
	// RouteSplitDeploying: the target's deployment, seeded from the source's
	// live artifact, is coming up.
	RouteSplitDeploying = "deploying"
	// RouteSplitRuleLive: the edge rule sends the route to the target.
	RouteSplitRuleLive = "rule_live"
	// RouteSplitDone: the split is in place (a memory split's resize may
	// have failed; Warning says so).
	RouteSplitDone = "done"
	// RouteSplitRolledBack: the split failed before traffic moved and the
	// target app was deleted.
	RouteSplitRolledBack = "rolled_back"
	// RouteSplitUndone: a finished split was reversed.
	RouteSplitUndone = "undone"
)

// RouteSplitSeedReason prefixes a split target's deployment reason; the
// split's ID follows.
const RouteSplitSeedReason = "route-split:"

// ErrRouteSplitInFlight refuses a second split of an app while one is in
// flight.
var ErrRouteSplitInFlight = fmt.Errorf("%w: a route split of this app is in flight", ErrConflict)

// RouteSplit is one applied split.
type RouteSplit struct {
	ID                 string
	AccountID          string
	SourceAppID        string
	TargetSlug         string
	TargetAppID        string
	TargetDeploymentID string
	Kind               string // memory | cpu
	Route              string
	Method             string
	PathGlob           string
	RouteRAMMB         int
	MainRAMMB          int
	PreviousRAMMB      int
	MainResized        bool
	EdgeRuleIDs        []string
	Phase              string
	Warning            string
	Error              string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// InFlight reports whether the worker still moves the split.
func (s RouteSplit) InFlight() bool {
	return s.Phase == RouteSplitCreating || s.Phase == RouteSplitDeploying || s.Phase == RouteSplitRuleLive
}

// RouteSplitSeed names the split whose target deployment is seeded from the
// source app's live artifact.
type RouteSplitSeed struct {
	AccountID   string
	SplitID     string
	SourceAppID string
	TargetAppID string
}

func (s RouteSplitSeed) valid() bool {
	return s.AccountID != "" && s.SplitID != "" && s.SourceAppID != "" && s.TargetAppID != "" && s.SourceAppID != s.TargetAppID
}

// RouteSplitStore keeps route splits.
type RouteSplitStore interface {
	// CreateRouteSplit records a split in phase creating.
	// ErrRouteSplitInFlight when the source app has one in flight.
	CreateRouteSplit(ctx context.Context, split RouteSplit) (RouteSplit, error)
	// RouteSplit reads one split of the account. ErrNotFound when missing.
	RouteSplit(ctx context.Context, accountID, id string) (RouteSplit, error)
	// RouteSplitsForApp lists an app's splits, newest first.
	RouteSplitsForApp(ctx context.Context, accountID, sourceAppID string, limit int) ([]RouteSplit, error)
	// InFlightRouteSplits lists splits the worker still moves, oldest
	// update first.
	InFlightRouteSplits(ctx context.Context, limit int) ([]RouteSplit, error)
	// AdvanceRouteSplit writes the split's mutable fields when its phase is
	// still fromPhase. ErrConflict when it moved.
	AdvanceRouteSplit(ctx context.Context, split RouteSplit, fromPhase string) (RouteSplit, error)
	// SeedRouteSplitTarget creates the target's deployment from the source
	// app's live artifact and copies the source's default-scope variables
	// and non-managed secrets, or returns the deployment already seeded for
	// the split. ErrNotFound when the source has no live artifact.
	SeedRouteSplitTarget(ctx context.Context, seed RouteSplitSeed) (Deployment, error)
}

func validRouteSplit(s RouteSplit) bool {
	return s.AccountID != "" && s.SourceAppID != "" && s.TargetSlug != "" && (s.Kind == "memory" || s.Kind == "cpu") &&
		s.Route != "" && s.PathGlob != "" && s.RouteRAMMB > 0 && s.MainRAMMB > 0 && s.PreviousRAMMB > 0
}

func validRouteSplitPhase(phase string) bool {
	switch phase {
	case RouteSplitCreating, RouteSplitDeploying, RouteSplitRuleLive, RouteSplitDone, RouteSplitRolledBack, RouteSplitUndone:
		return true
	}
	return false
}

// routeSplitTargetDeployment builds the target's deployment from the source
// artifact: the request workload only. The source's release already ran
// against the same data, and its workflows stay with the source so the work
// is not processed twice.
func routeSplitTargetDeployment(a projectCloneArtifact, seed RouteSplitSeed) Deployment {
	d := branchDeploymentFromArtifact(a, DefaultEnvScope, nil)
	d.branchInput = nil
	d.AppID = seed.TargetAppID
	d.Reason = RouteSplitSeedReason + seed.SplitID
	d.Workflows = nil
	return d
}

// MemStore

func (m *MemStore) CreateRouteSplit(_ context.Context, split RouteSplit) (RouteSplit, error) {
	if !validRouteSplit(split) {
		return RouteSplit{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[split.SourceAppID]
	if !ok || app.AccountID != split.AccountID || app.Status == AppDeleted {
		return RouteSplit{}, ErrNotFound
	}
	for _, existing := range m.routeSplits {
		if existing.SourceAppID == split.SourceAppID && existing.InFlight() {
			return RouteSplit{}, ErrRouteSplitInFlight
		}
	}
	if m.routeSplits == nil {
		m.routeSplits = map[string]RouteSplit{}
	}
	now := time.Now().UTC()
	split.ID = uuid.NewString()
	split.TargetAppID, split.TargetDeploymentID, split.MainResized = "", "", false
	split.EdgeRuleIDs = []string{}
	split.Phase, split.Warning, split.Error = RouteSplitCreating, "", ""
	split.CreatedAt, split.UpdatedAt = now, now
	m.routeSplits[split.ID] = split
	return cloneRouteSplit(split), nil
}

func (m *MemStore) RouteSplit(_ context.Context, accountID, id string) (RouteSplit, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	split, ok := m.routeSplits[id]
	if !ok || split.AccountID != accountID {
		return RouteSplit{}, ErrNotFound
	}
	return cloneRouteSplit(split), nil
}

func (m *MemStore) RouteSplitsForApp(_ context.Context, accountID, sourceAppID string, limit int) ([]RouteSplit, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []RouteSplit{}
	for _, split := range m.routeSplits {
		if split.AccountID == accountID && split.SourceAppID == sourceAppID {
			out = append(out, cloneRouteSplit(split))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID > out[j].ID
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemStore) InFlightRouteSplits(_ context.Context, limit int) ([]RouteSplit, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []RouteSplit{}
	for _, split := range m.routeSplits {
		if split.InFlight() {
			out = append(out, cloneRouteSplit(split))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].UpdatedAt.Before(out[j].UpdatedAt)
		}
		return out[i].ID < out[j].ID
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemStore) AdvanceRouteSplit(_ context.Context, split RouteSplit, fromPhase string) (RouteSplit, error) {
	if !validRouteSplitPhase(split.Phase) {
		return RouteSplit{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.routeSplits[split.ID]
	if !ok || current.AccountID != split.AccountID {
		return RouteSplit{}, ErrNotFound
	}
	if current.Phase != fromPhase {
		return RouteSplit{}, ErrConflict
	}
	current.TargetAppID, current.TargetDeploymentID = split.TargetAppID, split.TargetDeploymentID
	current.MainResized = split.MainResized
	current.EdgeRuleIDs = slices.Clone(split.EdgeRuleIDs)
	if current.EdgeRuleIDs == nil {
		current.EdgeRuleIDs = []string{}
	}
	current.Phase, current.Warning, current.Error = split.Phase, split.Warning, split.Error
	current.UpdatedAt = time.Now().UTC()
	m.routeSplits[current.ID] = current
	return cloneRouteSplit(current), nil
}

func cloneRouteSplit(s RouteSplit) RouteSplit {
	s.EdgeRuleIDs = slices.Clone(s.EdgeRuleIDs)
	if s.EdgeRuleIDs == nil {
		s.EdgeRuleIDs = []string{}
	}
	return s
}

func (m *MemStore) SeedRouteSplitTarget(ctx context.Context, seed RouteSplitSeed) (Deployment, error) {
	if !seed.valid() {
		return Deployment{}, ErrInvalidArgument
	}
	input := &projectEnvironmentBranchDeploymentInput{AccountID: seed.AccountID, RouteSplit: &seed}
	created, _, err := m.createDeployment(ctx, Deployment{AppID: seed.TargetAppID, Scope: DefaultEnvScope, branchInput: input}, nil, nil)
	return created, err
}

// prepareRouteSplitDeploymentLocked resolves the source's live artifact, or
// the deployment already seeded for the split.
func (m *MemStore) prepareRouteSplitDeploymentLocked(d Deployment) (Deployment, *Deployment, string, error) {
	seed := *d.branchInput.RouteSplit
	source, okSource := m.apps[seed.SourceAppID]
	target, okTarget := m.apps[seed.TargetAppID]
	if !okSource || !okTarget || source.AccountID != seed.AccountID || target.AccountID != seed.AccountID ||
		source.Status == AppDeleted || target.Status == AppDeleted {
		return Deployment{}, nil, "", ErrNotFound
	}
	if source.Type != target.Type {
		return Deployment{}, nil, "", ErrConflict
	}
	reason := RouteSplitSeedReason + seed.SplitID
	var existing *Deployment
	var live Deployment
	for _, dep := range m.deployments {
		switch {
		case dep.AppID == target.ID && dep.Reason == reason && dep.Status != DeployFailed && dep.Status != DeployCancelled:
			if existing == nil || dep.CreatedAt.After(existing.CreatedAt) {
				dep := dep
				existing = &dep
			}
		case dep.AppID == source.ID && normalizedDeploymentScope(dep.Scope) == DefaultEnvScope && dep.Status == DeployLive && dep.TrafficPercent > 0:
			if live.ID == "" || dep.CreatedAt.After(live.CreatedAt) {
				live = dep
			}
		}
	}
	if existing != nil {
		return Deployment{}, existing, "", nil
	}
	if live.ID == "" || live.RootfsKey == "" {
		return Deployment{}, nil, "", ErrNotFound
	}
	if err := m.requireLayerArtifactsRetainedLocked(m.deploymentLayerKeysLocked(live)); err != nil {
		return Deployment{}, nil, "", err
	}
	return routeSplitTargetDeployment(normalizeProjectCloneArtifact(projectCloneArtifactFromDeployment(live)), seed), nil, live.ID, nil
}

// attachRouteSplitDeploymentLocked copies the source deployment's sidecar
// layers and the source's default-scope variables and non-managed secrets.
// Values the target already has win.
func (m *MemStore) attachRouteSplitDeploymentLocked(sourceID string, created Deployment) {
	m.copyBranchDeploymentLayersLocked(sourceID, created.ID)
	source := m.deployments[sourceID]
	now := time.Now().UTC()
	for _, env := range m.envs {
		if env.AppID != source.AppID || env.Scope != DefaultEnvScope {
			continue
		}
		key := envKey{AppID: created.AppID, Scope: DefaultEnvScope, Key: env.Key}
		if _, ok := m.envs[key]; ok {
			continue
		}
		env.AppID, env.CreatedAt, env.UpdatedAt = created.AppID, now, now
		m.envs[key] = env
	}
	for _, secret := range m.secrets {
		if secret.AppID != source.AppID || secret.Scope != DefaultEnvScope || secret.ManagedPostgresBindingID != "" || secret.ManagedObjectStorageCredentialID != "" {
			continue
		}
		key := secretKey{AppID: created.AppID, Scope: DefaultEnvScope, Key: secret.Key}
		if _, ok := m.secrets[key]; ok {
			continue
		}
		secret.AppID, secret.CreatedAt, secret.UpdatedAt = created.AppID, now, now
		secret.Ciphertext = append([]byte(nil), secret.Ciphertext...)
		secret.DeliveryVersion, secret.DeliveredVersion, secret.DeliveryStatus = 1, 0, SecretDeliveryPending
		secret.LastDeliveryAttemptAt, secret.LastDeliveredAt = nil, nil
		secret.LastDeliveryErrorCode, secret.LastDeliveredWakeID, secret.LastDeliveredInstanceID = "", "", ""
		m.secrets[key] = secret
	}
}

// PgStore

const routeSplitsOneActiveIndex = "route_splits_one_active"

func (s *PgStore) CreateRouteSplit(ctx context.Context, split RouteSplit) (RouteSplit, error) {
	if !validRouteSplit(split) {
		return RouteSplit{}, ErrInvalidArgument
	}
	_, err1 := uuid.Parse(split.AccountID)
	_, err2 := uuid.Parse(split.SourceAppID)
	if err1 != nil || err2 != nil {
		return RouteSplit{}, ErrNotFound
	}
	var out RouteSplit
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var found bool
		if err := tx.QueryRow(ctx, `select true from apps where id = $1 and account_id = $2 and status <> 'deleted' for share`,
			split.SourceAppID, split.AccountID).Scan(&found); err != nil {
			return err
		}
		row, err := new(sqlc.Queries).InsertRouteSplit(ctx, tx, sqlc.InsertRouteSplitParams{
			AccountID: mustPgUUID(split.AccountID), SourceAppID: mustPgUUID(split.SourceAppID), TargetSlug: split.TargetSlug, Kind: split.Kind,
			Route: split.Route, Method: split.Method, PathGlob: split.PathGlob,
			RouteRamMb: int32(split.RouteRAMMB), MainRamMb: int32(split.MainRAMMB), PreviousRamMb: int32(split.PreviousRAMMB),
		})
		if err != nil {
			return err
		}
		out = routeSplitFromRow(row)
		return nil
	})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.ConstraintName == routeSplitsOneActiveIndex {
		return RouteSplit{}, ErrRouteSplitInFlight
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return RouteSplit{}, ErrNotFound
	}
	if err != nil {
		return RouteSplit{}, mapErr(err)
	}
	return out, nil
}

func (s *PgStore) RouteSplit(ctx context.Context, accountID, id string) (RouteSplit, error) {
	_, err1 := uuid.Parse(accountID)
	_, err2 := uuid.Parse(id)
	if err1 != nil || err2 != nil {
		return RouteSplit{}, ErrNotFound
	}
	row, err := new(sqlc.Queries).ReadRouteSplit(ctx, s.pool, sqlc.ReadRouteSplitParams{ID: mustPgUUID(id), AccountID: mustPgUUID(accountID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return RouteSplit{}, ErrNotFound
	}
	if err != nil {
		return RouteSplit{}, mapErr(err)
	}
	return routeSplitFromRow(row), nil
}

func (s *PgStore) RouteSplitsForApp(ctx context.Context, accountID, sourceAppID string, limit int) ([]RouteSplit, error) {
	_, err1 := uuid.Parse(accountID)
	_, err2 := uuid.Parse(sourceAppID)
	if err1 != nil || err2 != nil {
		return []RouteSplit{}, nil
	}
	rows, err := new(sqlc.Queries).ListRouteSplitsForApp(ctx, s.pool, sqlc.ListRouteSplitsForAppParams{
		AccountID: mustPgUUID(accountID), SourceAppID: mustPgUUID(sourceAppID), RowLimit: routeSplitLimit(limit),
	})
	if err != nil {
		return nil, mapErr(err)
	}
	return routeSplitsFromRows(rows), nil
}

func (s *PgStore) InFlightRouteSplits(ctx context.Context, limit int) ([]RouteSplit, error) {
	rows, err := new(sqlc.Queries).ListInFlightRouteSplits(ctx, s.pool, routeSplitLimit(limit))
	if err != nil {
		return nil, mapErr(err)
	}
	return routeSplitsFromRows(rows), nil
}

func (s *PgStore) AdvanceRouteSplit(ctx context.Context, split RouteSplit, fromPhase string) (RouteSplit, error) {
	if !validRouteSplitPhase(split.Phase) {
		return RouteSplit{}, ErrInvalidArgument
	}
	_, err1 := uuid.Parse(split.AccountID)
	_, err2 := uuid.Parse(split.ID)
	if err1 != nil || err2 != nil {
		return RouteSplit{}, ErrNotFound
	}
	rules := make([]pgtype.UUID, 0, len(split.EdgeRuleIDs))
	for _, id := range split.EdgeRuleIDs {
		parsed, err := uuid.Parse(id)
		if err != nil {
			return RouteSplit{}, ErrInvalidArgument
		}
		rules = append(rules, mustPgUUID(parsed.String()))
	}
	row, err := new(sqlc.Queries).AdvanceRouteSplit(ctx, s.pool, sqlc.AdvanceRouteSplitParams{
		ID: mustPgUUID(split.ID), AccountID: mustPgUUID(split.AccountID), FromPhase: fromPhase,
		TargetAppID: routeSplitPgUUID(split.TargetAppID), TargetDeploymentID: routeSplitPgUUID(split.TargetDeploymentID),
		MainResized: split.MainResized, EdgeRuleIds: rules, Phase: split.Phase, Warning: split.Warning, Error: split.Error,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		if _, readErr := s.RouteSplit(ctx, split.AccountID, split.ID); readErr != nil {
			return RouteSplit{}, readErr
		}
		return RouteSplit{}, ErrConflict
	}
	if err != nil {
		return RouteSplit{}, mapErr(err)
	}
	return routeSplitFromRow(row), nil
}

func (s *PgStore) SeedRouteSplitTarget(ctx context.Context, seed RouteSplitSeed) (Deployment, error) {
	if !seed.valid() {
		return Deployment{}, ErrInvalidArgument
	}
	for _, id := range []string{seed.AccountID, seed.SplitID, seed.SourceAppID, seed.TargetAppID} {
		if _, err := uuid.Parse(id); err != nil {
			return Deployment{}, ErrNotFound
		}
	}
	input := &projectEnvironmentBranchDeploymentInput{AccountID: seed.AccountID, RouteSplit: &seed}
	created, _, err := s.createDeployment(ctx, Deployment{AppID: seed.TargetAppID, Scope: DefaultEnvScope, branchInput: input}, nil, nil)
	return created, err
}

// prepareRouteSplitDeploymentTx runs inside createDeployment under the
// target app's lock.
func prepareRouteSplitDeploymentTx(ctx context.Context, tx pgx.Tx, d Deployment) (Deployment, *Deployment, string, error) {
	seed := *d.branchInput.RouteSplit
	var sourceType, targetType string
	err := tx.QueryRow(ctx, `
		select s.type, t.type
		  from apps s, apps t
		 where s.id = $1 and t.id = $2 and s.account_id = $3 and t.account_id = $3
		   and s.status <> 'deleted' and t.status <> 'deleted'
	`, seed.SourceAppID, seed.TargetAppID, seed.AccountID).Scan(&sourceType, &targetType)
	if errors.Is(err, pgx.ErrNoRows) {
		return Deployment{}, nil, "", ErrNotFound
	}
	if err != nil {
		return Deployment{}, nil, "", mapErr(err)
	}
	if sourceType != targetType {
		return Deployment{}, nil, "", ErrConflict
	}
	var existingID string
	err = tx.QueryRow(ctx, `
		select id::text from deployments
		 where app_id = $1 and reason = $2 and status not in ('failed', 'cancelled')
		 order by created_at desc limit 1
	`, seed.TargetAppID, RouteSplitSeedReason+seed.SplitID).Scan(&existingID)
	if err == nil {
		existing, err := deploymentByIDDB(ctx, tx, existingID)
		if err != nil {
			return Deployment{}, nil, "", err
		}
		return Deployment{}, &existing, "", nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Deployment{}, nil, "", mapErr(err)
	}
	source, err := scanDeploymentWithRootfs(tx.QueryRow(ctx, `select `+deploymentSelectColumnsWithRootfs+`
		  from deployments
		 where app_id = $1 and scope = 'default' and status = 'live' and traffic_percent > 0
		 order by created_at desc, id desc
		 limit 1`, seed.SourceAppID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Deployment{}, nil, "", ErrNotFound
	}
	if err != nil {
		return Deployment{}, nil, "", mapErr(err)
	}
	if source.RootfsKey == "" || source.RootfsBytes <= 0 {
		return Deployment{}, nil, "", ErrNotFound
	}
	// Serialize with layer GC, as a branch deployment does.
	if err := requireDeploymentLayerArtifactsTx(ctx, tx, source.ID); err != nil {
		return Deployment{}, nil, "", err
	}
	return routeSplitTargetDeployment(normalizeProjectCloneArtifact(projectCloneArtifactFromDeployment(source)), seed), nil, source.ID, nil
}

// attachRouteSplitDeploymentTx copies the artifact, sidecar layers and the
// source's default-scope variables and non-managed secrets, then moves the
// target's deployment to snapshotting. There is no lineage row: schedd's
// prime boots it.
func attachRouteSplitDeploymentTx(ctx context.Context, tx pgx.Tx, sourceID string, d Deployment, created *Deployment) error {
	if err := copyBranchDeploymentArtifactTx(ctx, tx, sourceID, d, created); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		insert into app_envs (account_id, app_id, scope, key, value)
		select e.account_id, $1, 'default', e.key, e.value
		  from app_envs e join deployments d on d.app_id = e.app_id
		 where d.id = $2 and e.scope = 'default'
		on conflict (app_id, scope, key) do nothing
	`, created.AppID, sourceID); err != nil {
		return mapErr(err)
	}
	if _, err := tx.Exec(ctx, `
		insert into app_secrets (account_id, app_id, scope, key, ciphertext, kid, value_hash, secret_version, secret_class)
		select s.account_id, $1, 'default', s.key, s.ciphertext, s.kid, s.value_hash, s.secret_version, s.secret_class
		  from app_secrets s join deployments d on d.app_id = s.app_id
		 where d.id = $2 and s.scope = 'default'
		   and s.managed_postgres_binding_id is null and s.managed_object_storage_credential_id is null
		on conflict (app_id, scope, key) do nothing
	`, created.AppID, sourceID); err != nil {
		return mapErr(err)
	}
	return stageBranchDeploymentTx(ctx, tx, created)
}

func routeSplitLimit(limit int) int32 {
	if limit <= 0 || limit > 500 {
		return 500
	}
	return int32(limit)
}

func routeSplitPgUUID(id string) pgtype.UUID {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return pgtype.UUID{}
	}
	return mustPgUUID(parsed.String())
}

func routeSplitsFromRows(rows []sqlc.RouteSplit) []RouteSplit {
	out := make([]RouteSplit, 0, len(rows))
	for _, row := range rows {
		out = append(out, routeSplitFromRow(row))
	}
	return out
}

func routeSplitFromRow(row sqlc.RouteSplit) RouteSplit {
	rules := make([]string, 0, len(row.EdgeRuleIds))
	for _, id := range row.EdgeRuleIds {
		rules = append(rules, pgUUIDString(id))
	}
	return RouteSplit{
		ID: pgUUIDString(row.ID), AccountID: pgUUIDString(row.AccountID), SourceAppID: pgUUIDString(row.SourceAppID),
		TargetSlug: row.TargetSlug, TargetAppID: pgUUIDString(row.TargetAppID), TargetDeploymentID: pgUUIDString(row.TargetDeploymentID),
		Kind: row.Kind, Route: row.Route, Method: row.Method, PathGlob: row.PathGlob,
		RouteRAMMB: int(row.RouteRamMb), MainRAMMB: int(row.MainRamMb), PreviousRAMMB: int(row.PreviousRamMb),
		MainResized: row.MainResized, EdgeRuleIDs: rules, Phase: row.Phase, Warning: row.Warning, Error: row.Error,
		CreatedAt: timestamptzToTime(row.CreatedAt), UpdatedAt: timestamptzToTime(row.UpdatedAt),
	}
}
