package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ProjectReleaseSet is an immutable app->deployment graph. Active is only the
// default ingress choice; clients carrying its ID can continue until expiry.
type ProjectReleaseSet struct {
	ID              string                 `json:"id"`
	AccountID       string                 `json:"account_id"`
	ProjectID       string                 `json:"project_id"`
	EnvironmentSlug string                 `json:"environment"`
	Active          bool                   `json:"active"`
	TTLSeconds      int                    `json:"ttl_seconds"`
	ExpiresAt       *time.Time             `json:"expires_at,omitempty"`
	CreatedAt       time.Time              `json:"created_at"`
	Members         []ProjectReleaseMember `json:"members"`
}

type ProjectReleaseMember struct {
	AppID        string `json:"app_id"`
	DeploymentID string `json:"deployment_id"`
}

type ProjectReleaseSetStore interface {
	PublishProjectReleaseSet(context.Context, string, string, string, int, []ProjectReleaseMember) (ProjectReleaseSet, error)
	ResolveProjectRelease(context.Context, string, string, string) (string, string, error)
	ResolveServiceRelease(context.Context, string, string, string, string) (string, string, error)
}

// ProjectReleaseSetPromotionStore exposes compare-and-swap graph activation
// for environment promotion and rollback. An empty expected ID means that no
// release set may be active.
type ProjectReleaseSetPromotionStore interface {
	PublishProjectReleaseSetIfActive(context.Context, string, string, string, string, []ProjectReleaseMember, int, []ProjectReleaseMember) (ProjectReleaseSet, error)
	DeactivateProjectReleaseSetIfActive(context.Context, string, string, string, string, []ProjectReleaseMember) error
}

// ProjectEnvironmentPromotionReleaseSetStore atomically switches a promotion's
// release graph together with its explicitly requested non-secret config sync.
type ProjectEnvironmentPromotionReleaseSetStore interface {
	PublishProjectEnvironmentPromotionReleaseSet(context.Context, string, string, int, []ProjectReleaseMember) (ProjectReleaseSet, error)
	RollbackProjectEnvironmentPromotionReleaseSet(context.Context, string, string, int, []ProjectReleaseMember) (ProjectReleaseSet, error)
}

// ProjectPromotionDeploymentStore stages a live deployment at zero traffic
// without changing the workload's current weighted route.
type ProjectPromotionDeploymentStore interface {
	MarkDeploymentLiveDark(context.Context, string) error
}

func validReleaseTTL(seconds int) bool {
	return seconds > 0 && seconds <= api.RevisionPinMaxTTLSeconds
}

func releaseMemberDeployment(ctx context.Context, tx pgx.Tx, appID, deploymentID, scope string) error {
	app, err := operationUUID(appID)
	if err != nil {
		return err
	}
	deployment, err := operationUUID(deploymentID)
	if err != nil {
		return err
	}
	_, err = sqlc.New().RetainedReleaseMemberDeploymentForUpdate(ctx, tx, sqlc.RetainedReleaseMemberDeploymentForUpdateParams{
		AppID: app, DeploymentID: deployment, Scope: normalizedDeploymentScope(scope)})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	return err
}

func (s *PgStore) PublishProjectReleaseSet(ctx context.Context, accountID, projectID, environment string, ttlSeconds int, members []ProjectReleaseMember) (ProjectReleaseSet, error) {
	return s.publishProjectReleaseSet(ctx, accountID, projectID, environment, nil, nil, ttlSeconds, members)
}

func (s *PgStore) PublishProjectReleaseSetIfActive(ctx context.Context, accountID, projectID, environment, expectedActiveID string, expectedFallback []ProjectReleaseMember, ttlSeconds int, members []ProjectReleaseMember) (ProjectReleaseSet, error) {
	return s.publishProjectReleaseSet(ctx, accountID, projectID, environment, &expectedActiveID, expectedFallback, ttlSeconds, members)
}

func (s *PgStore) publishProjectReleaseSet(ctx context.Context, accountID, projectID, environment string, expectedActiveID *string, expectedFallback []ProjectReleaseMember, ttlSeconds int, members []ProjectReleaseMember) (ProjectReleaseSet, error) {
	if !validReleaseTTL(ttlSeconds) || len(members) == 0 || len(members) > api.ProjectReleaseSetMaxMembers || !api.ValidProjectEnvironmentSlug(environment) {
		return ProjectReleaseSet{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectReleaseSet{}, fmt.Errorf("state: publish release begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	release, err := publishProjectReleaseSetTx(ctx, tx, accountID, projectID, environment, expectedActiveID, expectedFallback, ttlSeconds, members, false, nil)
	if err != nil {
		return ProjectReleaseSet{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ProjectReleaseSet{}, err
	}
	return release, nil
}

type projectReleaseSetBeforePublish func(context.Context, pgx.Tx) error

func publishProjectReleaseSetTx(ctx context.Context, tx pgx.Tx, accountID, projectID, environment string, expectedActiveID *string,
	expectedFallback []ProjectReleaseMember, ttlSeconds int, members []ProjectReleaseMember, allowGitOpsReplacement bool,
	beforePublish projectReleaseSetBeforePublish, skipFallbackApps ...map[string]struct{}) (ProjectReleaseSet, error) {
	if !validReleaseTTL(ttlSeconds) || len(members) == 0 || len(members) > api.ProjectReleaseSetMaxMembers || !api.ValidProjectEnvironmentSlug(environment) {
		return ProjectReleaseSet{}, ErrInvalidArgument
	}
	var found int
	if err := tx.QueryRow(ctx, `select 1 from projects where id = $1 and account_id = $2 for update`, projectID, accountID).Scan(&found); err != nil {
		return ProjectReleaseSet{}, mapErr(err)
	}
	if err := tx.QueryRow(ctx, `select 1 from project_environments where project_id = $1 and slug = $2`, projectID, environment).Scan(&found); err != nil {
		return ProjectReleaseSet{}, mapErr(err)
	}
	var activeID string
	activeErr := tx.QueryRow(ctx, `select id::text from project_release_sets
		where project_id = $1 and environment_slug = $2 and active for update`, projectID, environment).Scan(&activeID)
	if activeErr != nil && !errors.Is(activeErr, pgx.ErrNoRows) {
		return ProjectReleaseSet{}, activeErr
	}
	if errors.Is(activeErr, pgx.ErrNoRows) {
		activeID = ""
	}
	if expectedActiveID != nil && activeID != *expectedActiveID {
		return ProjectReleaseSet{}, ErrConflict
	}
	byApp := make(map[string]string, len(members))
	for _, member := range members {
		if _, err := uuid.Parse(member.AppID); err != nil {
			return ProjectReleaseSet{}, ErrInvalidArgument
		}
		if _, err := uuid.Parse(member.DeploymentID); err != nil {
			return ProjectReleaseSet{}, ErrInvalidArgument
		}
		if _, duplicate := byApp[member.AppID]; duplicate {
			return ProjectReleaseSet{}, ErrInvalidArgument
		}
		byApp[member.AppID] = member.DeploymentID
	}
	rows, err := tx.Query(ctx, `select id, coalesce(manifest, '{}'::jsonb)
		from apps where project_id = $1 and account_id = $2 and status <> 'deleted'
		  and coalesce(preview_of_slug, '') = ''
		order by id for update`, projectID, accountID)
	if err != nil {
		return ProjectReleaseSet{}, fmt.Errorf("state: publish release list workloads: %w", err)
	}
	var appRows []struct {
		id       string
		manifest []byte
	}
	for rows.Next() {
		var appRow struct {
			id       string
			manifest []byte
		}
		if err := rows.Scan(&appRow.id, &appRow.manifest); err != nil {
			rows.Close()
			return ProjectReleaseSet{}, err
		}
		appRows = append(appRows, appRow)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return ProjectReleaseSet{}, err
	}
	if activeID != "" && !allowGitOpsReplacement {
		if err := validateGitOpsReleaseMembersTx(ctx, tx, activeID, byApp); err != nil {
			return ProjectReleaseSet{}, err
		}
	}
	if expectedActiveID != nil && *expectedActiveID == "" {
		appIDs := make([]string, 0, len(appRows))
		skipped := map[string]struct{}{}
		if len(skipFallbackApps) > 0 {
			skipped = skipFallbackApps[0]
		}
		for _, appRow := range appRows {
			if _, skip := skipped[appRow.id]; !skip {
				appIDs = append(appIDs, appRow.id)
			}
		}
		if err := validateProjectReleaseFallbackTx(ctx, tx, appIDs, environment, expectedFallback); err != nil {
			return ProjectReleaseSet{}, err
		}
	}
	if len(appRows) != len(byApp) {
		return ProjectReleaseSet{}, ErrConflict
	}
	if beforePublish != nil {
		if err := beforePublish(ctx, tx); err != nil {
			return ProjectReleaseSet{}, err
		}
	}
	for _, appRow := range appRows {
		depID, ok := byApp[appRow.id]
		if !ok {
			return ProjectReleaseSet{}, ErrConflict
		}
		var manifest AppManifest
		if err := json.Unmarshal(appRow.manifest, &manifest); err != nil {
			return ProjectReleaseSet{}, err
		}
		if manifest.RevisionPinTTLSeconds < ttlSeconds {
			return ProjectReleaseSet{}, ErrConflict
		}
		if err := releaseMemberDeployment(ctx, tx, appRow.id, depID, environment); err != nil {
			return ProjectReleaseSet{}, err
		}
	}
	// A member may already have reached 0% when this graph is replaced.
	// Its direct revision deadline began at the deployment cutover, which
	// can precede this graph switch. Keep it alive through the old graph's
	// compatibility window measured from this transaction.
	if _, err := tx.Exec(ctx, `insert into deployment_revision_pins (deployment_id, app_id, expires_at)
		select rm.deployment_id, rm.app_id, now() + (rs.ttl_seconds * interval '1 second')
		  from project_release_sets rs
		  join project_release_members rm on rm.release_id = rs.id
		  join deployments d on d.id = rm.deployment_id
		 where rs.project_id = $1 and rs.environment_slug = $2 and rs.active
		   and d.status = 'live' and d.traffic_percent = 0
		on conflict (deployment_id) do update
		   set expires_at = greatest(deployment_revision_pins.expires_at, excluded.expires_at)`, projectID, environment); err != nil {
		return ProjectReleaseSet{}, fmt.Errorf("state: extend retained release members: %w", err)
	}
	if err := deactivateProjectReleaseSetsTx(ctx, tx, projectID, environment, ""); err != nil {
		return ProjectReleaseSet{}, err
	}
	release := ProjectReleaseSet{AccountID: accountID, ProjectID: projectID, EnvironmentSlug: environment, Active: true, TTLSeconds: ttlSeconds, Members: append([]ProjectReleaseMember(nil), members...)}
	if err := tx.QueryRow(ctx, `insert into project_release_sets (account_id, project_id, environment_slug, active, ttl_seconds)
		values ($1, $2, $3, true, $4)
		returning id, created_at`, accountID, projectID, environment, ttlSeconds).Scan(&release.ID, &release.CreatedAt); err != nil {
		return ProjectReleaseSet{}, err
	}
	for _, member := range members {
		if _, err := tx.Exec(ctx, `insert into project_release_members (release_id, app_id, deployment_id) values ($1, $2, $3)`, release.ID, member.AppID, member.DeploymentID); err != nil {
			return ProjectReleaseSet{}, err
		}
	}
	return release, nil
}

func (s *PgStore) DeactivateProjectReleaseSetIfActive(ctx context.Context, accountID, projectID, environment, expectedActiveID string, expectedFallback []ProjectReleaseMember) error {
	if expectedActiveID == "" {
		return ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("state: deactivate release set begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var found int
	if err := tx.QueryRow(ctx, `select 1 from projects where id = $1 and account_id = $2 for update`, projectID, accountID).Scan(&found); err != nil {
		return mapErr(err)
	}
	var activeID string
	err = tx.QueryRow(ctx, `select id::text from project_release_sets
		where project_id = $1 and environment_slug = $2 and active for update`, projectID, environment).Scan(&activeID)
	if err != nil {
		return mapErr(err)
	}
	if activeID != expectedActiveID {
		return ErrConflict
	}
	rows, err := tx.Query(ctx, `select id::text from apps
		where project_id = $1 and account_id = $2 and status <> 'deleted'
		  and coalesce(preview_of_slug, '') = '' order by id for update`, projectID, accountID)
	if err != nil {
		return fmt.Errorf("state: lock workloads for release rollback: %w", err)
	}
	var appIDs []string
	for rows.Next() {
		var appID string
		if err := rows.Scan(&appID); err != nil {
			rows.Close()
			return err
		}
		appIDs = append(appIDs, appID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	managed, err := gitOpsReleaseSetHasManagedMembersTx(ctx, tx, activeID)
	if err != nil {
		return err
	}
	if managed {
		return ErrConflict
	}
	if err := validateProjectReleaseFallbackTx(ctx, tx, appIDs, environment, expectedFallback); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `insert into deployment_revision_pins (deployment_id, app_id, expires_at)
		select rm.deployment_id, rm.app_id, now() + (rs.ttl_seconds * interval '1 second')
		  from project_release_sets rs
		  join project_release_members rm on rm.release_id = rs.id
		  join deployments d on d.id = rm.deployment_id
		 where rs.id = $1 and rs.active and d.status = 'live' and d.traffic_percent = 0
		on conflict (deployment_id) do update
		   set expires_at = greatest(deployment_revision_pins.expires_at, excluded.expires_at)`, expectedActiveID); err != nil {
		return fmt.Errorf("state: retain deactivated release members: %w", err)
	}
	if err := deactivateProjectReleaseSetsTx(ctx, tx, projectID, environment, expectedActiveID); err != nil {
		return fmt.Errorf("state: deactivate release set: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("state: commit release set deactivation: %w", err)
	}
	return nil
}

var _ ProjectReleaseSetPromotionStore = (*PgStore)(nil)

func validateProjectReleaseFallbackTx(ctx context.Context, tx pgx.Tx, appIDs []string, environment string, expected []ProjectReleaseMember) error {
	expectedByApp := make(map[string]string, len(expected))
	for _, member := range expected {
		if _, err := uuid.Parse(member.AppID); err != nil {
			return ErrInvalidArgument
		}
		if _, err := uuid.Parse(member.DeploymentID); err != nil {
			return ErrInvalidArgument
		}
		if _, exists := expectedByApp[member.AppID]; exists {
			return ErrInvalidArgument
		}
		expectedByApp[member.AppID] = member.DeploymentID
	}
	knownApps := make(map[string]struct{}, len(appIDs))
	for _, appID := range appIDs {
		knownApps[appID] = struct{}{}
	}
	for appID := range expectedByApp {
		if _, ok := knownApps[appID]; !ok {
			return ErrConflict
		}
	}
	for _, appID := range appIDs {
		rows, err := tx.Query(ctx, `select id::text, traffic_percent from deployments
			where app_id = $1 and scope = $2 and status = 'live' and traffic_percent > 0
			order by id for update`, appID, normalizedDeploymentScope(environment))
		if err != nil {
			return fmt.Errorf("state: inspect weighted fallback: %w", err)
		}
		type route struct {
			id     string
			weight int
		}
		var routes []route
		for rows.Next() {
			var item route
			if err := rows.Scan(&item.id, &item.weight); err != nil {
				rows.Close()
				return err
			}
			routes = append(routes, item)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		want := expectedByApp[appID]
		if want == "" {
			if len(routes) != 0 {
				return ErrConflict
			}
			continue
		}
		if len(routes) != 1 || routes[0].id != want || routes[0].weight != 100 {
			return ErrConflict
		}
	}
	return nil
}

// ResolveProjectRelease returns empty IDs only when no active release exists.
// An explicit but unknown/expired release, or a missing member, fails closed.
func (s *PgStore) ResolveProjectRelease(ctx context.Context, appID, scope, requestedID string) (string, string, error) {
	app, err := operationUUID(appID)
	if err != nil {
		return "", "", err
	}
	release, err := optionalOperationUUID(requestedID)
	if err != nil {
		return "", "", err
	}
	row, err := sqlc.New().ResolvePublicProjectRelease(ctx, s.pool, sqlc.ResolvePublicProjectReleaseParams{
		AppID: app, Scope: normalizedDeploymentScope(scope), ReleaseID: release})
	if errors.Is(err, pgx.ErrNoRows) {
		if requestedID != "" {
			return "", "", ErrNotFound
		}
		return "", "", nil
	}
	if err != nil {
		return "", "", err
	}
	if row.DeploymentID == "" {
		return "", "", ErrConflict
	}
	if err := s.releaseTargetLive(ctx, appID, row.DeploymentID); err != nil {
		return "", "", err
	}
	return row.ReleaseID, row.DeploymentID, nil
}

func optionalOperationUUID(id string) (pgtype.UUID, error) {
	if id == "" {
		return pgtype.UUID{}, nil
	}
	return operationUUID(id)
}

func (s *PgStore) releaseTargetLive(ctx context.Context, appID, deploymentID string) error {
	app, err := operationUUID(appID)
	if err != nil {
		return err
	}
	deployment, err := operationUUID(deploymentID)
	if err != nil {
		return err
	}
	usable, err := sqlc.New().RetainedReleaseTargetUsable(ctx, s.pool, sqlc.RetainedReleaseTargetUsableParams{
		AppID: app, DeploymentID: deployment})
	if err != nil {
		return err
	}
	if !usable {
		return ErrConflict
	}
	return nil
}

// ResolveServiceRelease uses the verified source deployment, not a guest
// header, to prove the caller belongs to the selected graph. Without a header
// it can infer a release only when membership is unambiguous.
func (s *PgStore) ResolveServiceRelease(ctx context.Context, callerAppID, callerDeploymentID, targetAppID, requestedID string) (string, string, error) {
	if callerDeploymentID == "" {
		var active bool
		if err := s.pool.QueryRow(ctx, `select exists (
			select 1 from apps caller join apps target on target.project_id = caller.project_id
			join project_release_sets rs on rs.project_id = caller.project_id
			where caller.id = $1 and target.id = $2 and rs.active
		)`, callerAppID, targetAppID).Scan(&active); err != nil {
			return "", "", err
		}
		if active || requestedID != "" {
			return "", "", ErrConflict
		}
		return "", "", nil
	}
	caller, err := operationUUID(callerAppID)
	if err != nil {
		return "", "", err
	}
	deployment, err := operationUUID(callerDeploymentID)
	if err != nil {
		return "", "", err
	}
	target, err := operationUUID(targetAppID)
	if err != nil {
		return "", "", err
	}
	release, err := optionalOperationUUID(requestedID)
	if err != nil {
		return "", "", err
	}
	matches, err := sqlc.New().ListRetainedServiceReleases(ctx, s.pool, sqlc.ListRetainedServiceReleasesParams{
		CallerAppID: caller, CallerDeploymentID: deployment, TargetAppID: target, ReleaseID: release})
	if err != nil {
		return "", "", err
	}
	if len(matches) == 0 {
		if requestedID != "" {
			return "", "", ErrNotFound
		}
		var active bool
		if err := s.pool.QueryRow(ctx, `select exists (
			select 1 from apps caller join apps target on target.project_id = caller.project_id
			join project_release_sets rs on rs.project_id = caller.project_id
			where caller.id = $1 and target.id = $2 and rs.active
		)`, callerAppID, targetAppID).Scan(&active); err != nil {
			return "", "", err
		}
		if active {
			return "", "", ErrConflict
		}
		return "", "", nil
	}
	if len(matches) > 1 {
		return "", "", ErrConflict
	}
	if err := s.releaseTargetLive(ctx, targetAppID, matches[0].DeploymentID); err != nil {
		return "", "", err
	}
	return matches[0].ReleaseID, matches[0].DeploymentID, nil
}

func (s *PgStore) publishProjectReleaseSetTx(ctx context.Context, tx pgx.Tx, accountID, projectID, environment string, ttlSeconds int, members []ProjectReleaseMember, expectedActiveID *string) (ProjectReleaseSet, error) {
	if !validReleaseTTL(ttlSeconds) || len(members) == 0 || len(members) > api.ProjectReleaseSetMaxMembers || !api.ValidProjectEnvironmentSlug(environment) {
		return ProjectReleaseSet{}, ErrInvalidArgument
	}
	if err := lockProjectForReleaseSetTx(ctx, tx, accountID, projectID); err != nil {
		return ProjectReleaseSet{}, err
	}
	var found int
	if err := tx.QueryRow(ctx, `select 1 from project_environments where project_id = $1 and slug = $2`, projectID, environment).Scan(&found); err != nil {
		return ProjectReleaseSet{}, mapErr(err)
	}
	var activeID string
	err := tx.QueryRow(ctx, `select id::text from project_release_sets where project_id = $1 and environment_slug = $2 and active for update`, projectID, environment).Scan(&activeID)
	if errors.Is(err, pgx.ErrNoRows) {
		activeID = ""
	} else if err != nil {
		return ProjectReleaseSet{}, err
	}
	if expectedActiveID != nil && activeID != *expectedActiveID {
		return ProjectReleaseSet{}, ErrConflict
	}
	byApp := make(map[string]string, len(members))
	for _, member := range members {
		if _, err := uuid.Parse(member.AppID); err != nil {
			return ProjectReleaseSet{}, ErrInvalidArgument
		}
		if _, err := uuid.Parse(member.DeploymentID); err != nil {
			return ProjectReleaseSet{}, ErrInvalidArgument
		}
		if _, duplicate := byApp[member.AppID]; duplicate {
			return ProjectReleaseSet{}, ErrInvalidArgument
		}
		byApp[member.AppID] = member.DeploymentID
	}
	rows, err := tx.Query(ctx, `select id, coalesce(manifest, '{}'::jsonb)
		from apps where project_id = $1 and account_id = $2 and status <> 'deleted'
		  and coalesce(preview_of_slug, '') = ''
		order by id for update`, projectID, accountID)
	if err != nil {
		return ProjectReleaseSet{}, fmt.Errorf("state: publish release list workloads: %w", err)
	}
	var appRows []struct {
		id       string
		manifest []byte
	}
	for rows.Next() {
		var appRow struct {
			id       string
			manifest []byte
		}
		if err := rows.Scan(&appRow.id, &appRow.manifest); err != nil {
			rows.Close()
			return ProjectReleaseSet{}, err
		}
		appRows = append(appRows, appRow)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return ProjectReleaseSet{}, err
	}
	if activeID != "" {
		if err := validateGitOpsReleaseMembersTx(ctx, tx, activeID, byApp); err != nil {
			return ProjectReleaseSet{}, err
		}
	}
	if len(appRows) != len(byApp) {
		return ProjectReleaseSet{}, ErrConflict
	}
	for _, appRow := range appRows {
		depID, ok := byApp[appRow.id]
		if !ok {
			return ProjectReleaseSet{}, ErrConflict
		}
		var manifest AppManifest
		if err := json.Unmarshal(appRow.manifest, &manifest); err != nil {
			return ProjectReleaseSet{}, err
		}
		if manifest.RevisionPinTTLSeconds < ttlSeconds {
			return ProjectReleaseSet{}, ErrConflict
		}
		if err := releaseMemberDeployment(ctx, tx, appRow.id, depID, environment); err != nil {
			return ProjectReleaseSet{}, err
		}
	}
	// A member may already have reached 0% when this graph is replaced.
	// Its direct revision deadline began at the deployment cutover, which
	// can precede this graph switch. Keep it alive through the old graph's
	// compatibility window measured from this transaction.
	if _, err := tx.Exec(ctx, `insert into deployment_revision_pins (deployment_id, app_id, expires_at)
		select rm.deployment_id, rm.app_id, now() + (rs.ttl_seconds * interval '1 second')
		  from project_release_sets rs
		  join project_release_members rm on rm.release_id = rs.id
		  join deployments d on d.id = rm.deployment_id
		 where rs.project_id = $1 and rs.environment_slug = $2 and rs.active
		   and d.status = 'live' and d.traffic_percent = 0
		on conflict (deployment_id) do update
		   set expires_at = greatest(deployment_revision_pins.expires_at, excluded.expires_at)`, projectID, environment); err != nil {
		return ProjectReleaseSet{}, fmt.Errorf("state: extend retained release members: %w", err)
	}
	if err := deactivateProjectReleaseSetsTx(ctx, tx, projectID, environment, ""); err != nil {
		return ProjectReleaseSet{}, err
	}
	release := ProjectReleaseSet{AccountID: accountID, ProjectID: projectID, EnvironmentSlug: environment, Active: true, TTLSeconds: ttlSeconds, Members: append([]ProjectReleaseMember(nil), members...)}
	if err := tx.QueryRow(ctx, `insert into project_release_sets (account_id, project_id, environment_slug, active, ttl_seconds)
		values ($1, $2, $3, true, $4)
		returning id, created_at`, accountID, projectID, environment, ttlSeconds).Scan(&release.ID, &release.CreatedAt); err != nil {
		return ProjectReleaseSet{}, err
	}
	for _, member := range members {
		if _, err := tx.Exec(ctx, `insert into project_release_members (release_id, app_id, deployment_id) values ($1, $2, $3)`, release.ID, member.AppID, member.DeploymentID); err != nil {
			return ProjectReleaseSet{}, err
		}
	}
	return release, nil
}

func (s *PgStore) PublishProjectEnvironmentPromotionReleaseSet(ctx context.Context, accountID, promotionID string, ttlSeconds int, members []ProjectReleaseMember) (ProjectReleaseSet, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectReleaseSet{}, fmt.Errorf("state: begin promotion release publish: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	promotion, err := scanProjectEnvironmentPromotion(tx.QueryRow(ctx, `
		select `+projectEnvironmentPromotionSelectColumns+`
		  from project_environment_promotions where id = $1 and account_id = $2`, promotionID, accountID))
	if err != nil {
		return ProjectReleaseSet{}, err
	}
	if err := lockProjectForReleaseSetTx(ctx, tx, promotion.AccountID, promotion.ProjectID); err != nil {
		return ProjectReleaseSet{}, err
	}
	promotion, err = scanProjectEnvironmentPromotion(tx.QueryRow(ctx, `
		select `+projectEnvironmentPromotionSelectColumns+`
		  from project_environment_promotions where id = $1 and account_id = $2 for update`, promotionID, accountID))
	if err != nil {
		return ProjectReleaseSet{}, err
	}
	if promotion.PreviousTargetReleaseSetID == "" {
		return ProjectReleaseSet{}, ErrConflict
	}
	if promotion.TargetReleaseSetID != "" {
		if err := lockProjectForReleaseSetTx(ctx, tx, promotion.AccountID, promotion.ProjectID); err != nil {
			return ProjectReleaseSet{}, err
		}
		activeID, err := activeProjectReleaseSetIDTx(ctx, tx, promotion.ProjectID, promotion.ToEnvironment)
		if err != nil || activeID != promotion.TargetReleaseSetID {
			if err != nil {
				return ProjectReleaseSet{}, err
			}
			return ProjectReleaseSet{}, ErrConflict
		}
		release, err := readProjectReleaseSetTx(ctx, tx, promotion.AccountID, promotion.ProjectID, promotion.ToEnvironment, promotion.TargetReleaseSetID)
		if err != nil {
			return ProjectReleaseSet{}, err
		}
		if err := verifyProjectEnvironmentPromotionConfigTx(ctx, tx, promotion, false); err != nil {
			return ProjectReleaseSet{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return ProjectReleaseSet{}, err
		}
		return release, nil
	}
	targetConfigVersion, err := applyProjectEnvironmentPromotionConfigTx(ctx, tx, promotion)
	if err != nil {
		return ProjectReleaseSet{}, err
	}
	release, err := s.publishProjectReleaseSetTx(ctx, tx, promotion.AccountID, promotion.ProjectID, promotion.ToEnvironment,
		ttlSeconds, members, &promotion.PreviousTargetReleaseSetID)
	if err != nil {
		return ProjectReleaseSet{}, err
	}
	if _, err := tx.Exec(ctx, `update project_environment_promotions set target_release_set_id = $2, target_config_version = $3, updated_at = now() where id = $1`, promotionID, release.ID, targetConfigVersion); err != nil {
		return ProjectReleaseSet{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ProjectReleaseSet{}, err
	}
	return release, nil
}

func (s *PgStore) RollbackProjectEnvironmentPromotionReleaseSet(ctx context.Context, accountID, promotionID string, ttlSeconds int, members []ProjectReleaseMember) (ProjectReleaseSet, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectReleaseSet{}, fmt.Errorf("state: begin promotion release rollback: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	promotion, err := scanProjectEnvironmentPromotion(tx.QueryRow(ctx, `
		select `+projectEnvironmentPromotionSelectColumns+`
		  from project_environment_promotions where id = $1 and account_id = $2`, promotionID, accountID))
	if err != nil {
		return ProjectReleaseSet{}, err
	}
	if err := lockProjectForReleaseSetTx(ctx, tx, promotion.AccountID, promotion.ProjectID); err != nil {
		return ProjectReleaseSet{}, err
	}
	promotion, err = scanProjectEnvironmentPromotion(tx.QueryRow(ctx, `
		select `+projectEnvironmentPromotionSelectColumns+`
		  from project_environment_promotions where id = $1 and account_id = $2 for update`, promotionID, accountID))
	if err != nil {
		return ProjectReleaseSet{}, err
	}
	if promotion.PreviousTargetReleaseSetID == "" || promotion.TargetReleaseSetID == "" {
		return ProjectReleaseSet{}, ErrConflict
	}
	if promotion.RestoredTargetReleaseSetID != "" {
		if err := lockProjectForReleaseSetTx(ctx, tx, promotion.AccountID, promotion.ProjectID); err != nil {
			return ProjectReleaseSet{}, err
		}
		activeID, err := activeProjectReleaseSetIDTx(ctx, tx, promotion.ProjectID, promotion.ToEnvironment)
		if err != nil || activeID != promotion.RestoredTargetReleaseSetID {
			if err != nil {
				return ProjectReleaseSet{}, err
			}
			return ProjectReleaseSet{}, ErrConflict
		}
		release, err := readProjectReleaseSetTx(ctx, tx, promotion.AccountID, promotion.ProjectID, promotion.ToEnvironment, promotion.RestoredTargetReleaseSetID)
		if err != nil {
			return ProjectReleaseSet{}, err
		}
		if err := verifyProjectEnvironmentPromotionConfigTx(ctx, tx, promotion, true); err != nil {
			return ProjectReleaseSet{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return ProjectReleaseSet{}, err
		}
		return release, nil
	}
	rollbackConfigVersion, err := rollbackProjectEnvironmentPromotionConfigTx(ctx, tx, promotion)
	if err != nil {
		return ProjectReleaseSet{}, err
	}
	release, err := s.publishProjectReleaseSetTx(ctx, tx, promotion.AccountID, promotion.ProjectID, promotion.ToEnvironment,
		ttlSeconds, members, &promotion.TargetReleaseSetID)
	if err != nil {
		return ProjectReleaseSet{}, err
	}
	if _, err := tx.Exec(ctx, `update project_environment_promotions set restored_target_release_set_id = $2, rollback_config_version = $3, updated_at = now() where id = $1`, promotionID, release.ID, rollbackConfigVersion); err != nil {
		return ProjectReleaseSet{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ProjectReleaseSet{}, err
	}
	return release, nil
}

func lockProjectForReleaseSetTx(ctx context.Context, tx pgx.Tx, accountID, projectID string) error {
	var found int
	if err := tx.QueryRow(ctx, `select 1 from projects where id = $1 and account_id = $2 for update`, projectID, accountID).Scan(&found); err != nil {
		return mapErr(err)
	}
	return nil
}

func activeProjectReleaseSetIDTx(ctx context.Context, tx pgx.Tx, projectID, environment string) (string, error) {
	var id string
	err := tx.QueryRow(ctx, `select id::text from project_release_sets where project_id = $1 and environment_slug = $2 and active`, projectID, environment).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return id, err
}

func validateGitOpsReleaseMembersTx(ctx context.Context, tx pgx.Tx, releaseID string, proposed map[string]string) error {
	rows, err := tx.Query(ctx, `select rm.app_id::text, rm.deployment_id::text
		from project_release_members rm
		join deployments d on d.id = rm.deployment_id
		where rm.release_id = $1::uuid and d.environment_workload_runtime is not null
		order by rm.app_id for update of d`, releaseID)
	if err != nil {
		return fmt.Errorf("state: lock managed release members: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var appID, deploymentID string
		if err := rows.Scan(&appID, &deploymentID); err != nil {
			return err
		}
		if proposed[appID] != deploymentID {
			return ErrConflict
		}
	}
	return rows.Err()
}

func gitOpsReleaseSetHasManagedMembersTx(ctx context.Context, tx pgx.Tx, releaseID string) (bool, error) {
	var managed bool
	err := tx.QueryRow(ctx, `select exists (
		select 1 from project_release_members rm
		join deployments d on d.id = rm.deployment_id
		where rm.release_id = $1::uuid and d.environment_workload_runtime is not null
	)`, releaseID).Scan(&managed)
	return managed, err
}

func projectEnvironmentConfigLatestTx(ctx context.Context, tx pgx.Tx, accountID, projectID, environment string) (ProjectEnvironmentConfig, error) {
	config, err := scanProjectEnvironmentConfig(tx.QueryRow(ctx, `
		select c.id, c.account_id, c.project_id, c.environment_slug,
		       c.version, c.config_hash, c.config_json, c.created_at
		  from project_environment_config_versions c
		 where c.account_id = $1 and c.project_id = $2 and c.environment_slug = $3
		 order by c.version desc
		 limit 1
	`, accountID, projectID, environment))
	if errors.Is(err, ErrNotFound) {
		return ProjectEnvironmentConfig{
			AccountID: accountID, ProjectID: projectID, EnvironmentSlug: environment,
			ConfigHash: api.EmptyProjectEnvironmentConfigHash(), Values: json.RawMessage(`{}`),
		}, nil
	}
	return config, err
}

func insertProjectEnvironmentConfigVersionTx(ctx context.Context, tx pgx.Tx, accountID, projectID, environment string, values json.RawMessage, hash string) (int64, error) {
	canonical, actualHash, err := api.NormalizeProjectEnvironmentConfig(values)
	if err != nil || actualHash != hash {
		return 0, ErrConflict
	}
	var version int64
	if err := tx.QueryRow(ctx, `
		select coalesce(max(version), 0) + 1
		  from project_environment_config_versions
		 where project_id = $1 and environment_slug = $2
	`, projectID, environment).Scan(&version); err != nil {
		return 0, mapErr(err)
	}
	if _, err := tx.Exec(ctx, `
		insert into project_environment_config_versions
			(account_id, project_id, environment_slug, version, config_hash, config_json)
		values ($1, $2, $3, $4, $5, $6::jsonb)
	`, accountID, projectID, environment, version, hash, []byte(canonical)); err != nil {
		return 0, mapErr(err)
	}
	return version, nil
}

func applyProjectEnvironmentPromotionConfigTx(ctx context.Context, tx pgx.Tx, promotion ProjectEnvironmentPromotion) (int64, error) {
	if !promotion.SyncConfig {
		return 0, nil
	}
	if promotion.TargetConfigVersion != 0 {
		if err := verifyProjectEnvironmentPromotionConfigTx(ctx, tx, promotion, false); err != nil {
			return 0, err
		}
		return promotion.TargetConfigVersion, nil
	}
	source, err := projectEnvironmentConfigLatestTx(ctx, tx, promotion.AccountID, promotion.ProjectID, promotion.FromEnvironment)
	if err != nil {
		return 0, err
	}
	target, err := projectEnvironmentConfigLatestTx(ctx, tx, promotion.AccountID, promotion.ProjectID, promotion.ToEnvironment)
	if err != nil {
		return 0, err
	}
	if source.ConfigHash != promotion.SourceConfigHash || target.ConfigHash != promotion.PreviousTargetConfigHash {
		return 0, ErrConflict
	}
	return insertProjectEnvironmentConfigVersionTx(ctx, tx, promotion.AccountID, promotion.ProjectID,
		promotion.ToEnvironment, promotion.SourceConfigSnapshot, promotion.SourceConfigHash)
}

func rollbackProjectEnvironmentPromotionConfigTx(ctx context.Context, tx pgx.Tx, promotion ProjectEnvironmentPromotion) (int64, error) {
	if !promotion.SyncConfig {
		return 0, nil
	}
	if promotion.RollbackConfigVersion != 0 {
		if err := verifyProjectEnvironmentPromotionConfigTx(ctx, tx, promotion, true); err != nil {
			return 0, err
		}
		return promotion.RollbackConfigVersion, nil
	}
	target, err := projectEnvironmentConfigLatestTx(ctx, tx, promotion.AccountID, promotion.ProjectID, promotion.ToEnvironment)
	if err != nil {
		return 0, err
	}
	if target.Version != promotion.TargetConfigVersion || target.ConfigHash != promotion.SourceConfigHash {
		return 0, ErrConflict
	}
	return insertProjectEnvironmentConfigVersionTx(ctx, tx, promotion.AccountID, promotion.ProjectID,
		promotion.ToEnvironment, promotion.PreviousTargetConfigSnapshot, promotion.PreviousTargetConfigHash)
}

func verifyProjectEnvironmentPromotionConfigTx(ctx context.Context, tx pgx.Tx, promotion ProjectEnvironmentPromotion, rolledBack bool) error {
	if !promotion.SyncConfig {
		return nil
	}
	target, err := projectEnvironmentConfigLatestTx(ctx, tx, promotion.AccountID, promotion.ProjectID, promotion.ToEnvironment)
	if err != nil {
		return err
	}
	if rolledBack {
		if promotion.RollbackConfigVersion == 0 || target.Version != promotion.RollbackConfigVersion || target.ConfigHash != promotion.PreviousTargetConfigHash {
			return ErrConflict
		}
		return nil
	}
	if promotion.TargetConfigVersion == 0 || target.Version != promotion.TargetConfigVersion || target.ConfigHash != promotion.SourceConfigHash {
		return ErrConflict
	}
	return nil
}

func readProjectReleaseSetTx(ctx context.Context, tx pgx.Tx, accountID, projectID, environment, releaseID string) (ProjectReleaseSet, error) {
	parsed, err := uuid.Parse(releaseID)
	if err != nil {
		return ProjectReleaseSet{}, ErrInvalidArgument
	}
	data, err := sqlc.New().ReadProjectReleaseSet(ctx, tx, sqlc.ReadProjectReleaseSetParams{
		AccountID: mustPgUUID(accountID), ProjectID: mustPgUUID(projectID), Environment: environment,
		ReleaseID: pgtype.UUID{Bytes: parsed, Valid: true},
	})
	if err != nil {
		return ProjectReleaseSet{}, mapErr(err)
	}
	return decodeProjectReleaseSet(data)
}

func deactivateProjectReleaseSetsTx(ctx context.Context, tx pgx.Tx, projectID, environment, releaseID string) error {
	project, err := operationUUID(projectID)
	if err != nil {
		return err
	}
	release, err := optionalOperationUUID(releaseID)
	if err != nil {
		return err
	}
	return sqlc.New().DeactivateProjectReleaseSets(ctx, tx, sqlc.DeactivateProjectReleaseSetsParams{
		ProjectID: project, Environment: environment, ReleaseID: release})
}
