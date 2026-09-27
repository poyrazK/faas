package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
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

// ProjectPromotionDeploymentStore stages a live deployment at zero traffic
// without changing the workload's current weighted route.
type ProjectPromotionDeploymentStore interface {
	MarkDeploymentLiveDark(context.Context, string) error
}

func validReleaseTTL(seconds int) bool {
	return seconds > 0 && seconds <= api.RevisionPinMaxTTLSeconds
}

func releaseMemberDeployment(ctx context.Context, tx pgx.Tx, appID, deploymentID, scope string) error {
	var found int
	err := tx.QueryRow(ctx, `select 1 from deployments d
		where d.id = $1 and d.app_id = $2 and d.scope = $3 and d.status = 'live'
		  and (d.traffic_percent > 0 or d.traffic_percent_explicit
		    or exists (select 1 from deployment_revision_pins p
		      where p.deployment_id = d.id and p.expires_at > now())
		    or exists (select 1 from project_release_members rm
		      join project_release_sets rs on rs.id = rm.release_id
		      where rm.deployment_id = d.id and rm.app_id = d.app_id
		        and (rs.active or rs.expires_at > now())))
		for update`,
		deploymentID, appID, normalizedDeploymentScope(scope)).Scan(&found)
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
	if expectedActiveID != nil && *expectedActiveID == "" {
		appIDs := make([]string, 0, len(appRows))
		for _, appRow := range appRows {
			appIDs = append(appIDs, appRow.id)
		}
		if err := validateProjectReleaseFallbackTx(ctx, tx, appIDs, environment, expectedFallback); err != nil {
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
	if _, err := tx.Exec(ctx, `update project_release_sets set active = false, expires_at = now() + (ttl_seconds * interval '1 second')
		where project_id = $1 and environment_slug = $2 and active`, projectID, environment); err != nil {
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
	if err := tx.Commit(ctx); err != nil {
		return ProjectReleaseSet{}, err
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
	if _, err := tx.Exec(ctx, `update project_release_sets set active = false,
		expires_at = now() + (ttl_seconds * interval '1 second')
		where id = $1 and project_id = $2 and environment_slug = $3 and active`, expectedActiveID, projectID, environment); err != nil {
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
	if requestedID != "" {
		if _, err := uuid.Parse(requestedID); err != nil {
			return "", "", ErrInvalidArgument
		}
	}
	query := `select rs.id, rm.deployment_id
		from apps a join project_release_sets rs on rs.project_id = a.project_id
		left join project_release_members rm on rm.release_id = rs.id and rm.app_id = a.id
		where a.id = $1 and a.status <> 'deleted' and rs.environment_slug = $2
		  and rs.active`
	args := []any{appID, normalizedDeploymentScope(scope)}
	if requestedID != "" {
		query = `select rs.id, rm.deployment_id
			from apps a join project_release_sets rs on rs.project_id = a.project_id
			left join project_release_members rm on rm.release_id = rs.id and rm.app_id = a.id
			where a.id = $1 and a.status <> 'deleted' and rs.environment_slug = $2
			  and (rs.active or rs.expires_at > now()) and rs.id = $3`
		args = append(args, requestedID)
	}
	var releaseID string
	var deploymentID *string
	err := s.pool.QueryRow(ctx, query, args...).Scan(&releaseID, &deploymentID)
	if errors.Is(err, pgx.ErrNoRows) {
		if requestedID != "" {
			return "", "", ErrNotFound
		}
		return "", "", nil
	}
	if err != nil {
		return "", "", err
	}
	if deploymentID == nil {
		return "", "", ErrConflict
	}
	if err := s.releaseTargetLive(ctx, appID, *deploymentID); err != nil {
		return "", "", err
	}
	return releaseID, *deploymentID, nil
}

func (s *PgStore) releaseTargetLive(ctx context.Context, appID, deploymentID string) error {
	var found int
	err := s.pool.QueryRow(ctx, `select 1 from deployments d where d.id = $1 and d.app_id = $2 and d.status = 'live'
		and (d.traffic_percent > 0 or exists (select 1 from deployment_revision_pins p
			where p.deployment_id = d.id and p.expires_at > now())
			or exists (select 1 from project_release_members rm
			join project_release_sets rs on rs.id = rm.release_id
			where rm.deployment_id = d.id and rm.app_id = d.app_id
			  and (rs.active or rs.expires_at > now())))`, deploymentID, appID).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	return err
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
	if requestedID != "" {
		if _, err := uuid.Parse(requestedID); err != nil {
			return "", "", ErrInvalidArgument
		}
	}
	query := `select rs.id, target.deployment_id
		from project_release_sets rs
		join project_release_members caller on caller.release_id = rs.id
		join project_release_members target on target.release_id = rs.id
		where caller.app_id = $1 and caller.deployment_id = $2 and target.app_id = $3
		  and (rs.active or rs.expires_at > now())`
	args := []any{callerAppID, callerDeploymentID, targetAppID}
	if requestedID != "" {
		query += ` and rs.id = $4`
		args = append(args, requestedID)
	}
	query += ` order by rs.created_at desc limit 2`
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return "", "", err
	}
	defer rows.Close()
	var matches []struct{ release, deployment string }
	for rows.Next() {
		var row struct{ release, deployment string }
		if err := rows.Scan(&row.release, &row.deployment); err != nil {
			return "", "", err
		}
		matches = append(matches, row)
	}
	if err := rows.Err(); err != nil {
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
	if err := s.releaseTargetLive(ctx, targetAppID, matches[0].deployment); err != nil {
		return "", "", err
	}
	return matches[0].release, matches[0].deployment, nil
}
