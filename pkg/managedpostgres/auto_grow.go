package managedpostgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dbmaintenance"
	"github.com/onebox-faas/faas/pkg/dbstorage"
)

const autoGrowMinIncrease = int64(1 << 30)

var autoGrowNamespace = uuid.MustParse("fa7f00ad-7780-471e-ab24-c5b55a3c4f61")

type autoGrowCandidate struct {
	databaseID, subject string
	cap, size           int64
	idleAt, runningAt   time.Time
	updatedAt           time.Time
	window              dbmaintenance.Interval
}

func autoGrowTarget(size, ceiling int64) int64 {
	if size <= 0 || ceiling <= size {
		return 0
	}
	return size + min(ceiling-size, max(size/4, autoGrowMinIncrease))
}

func (s *Service) SetAutoGrow(ctx context.Context, accountID, id string, maxBytes int64) (api.ManagedPostgresAutoGrowStatus, error) {
	store, ok := s.store.(*PostgresStore)
	if !ok {
		return api.ManagedPostgresAutoGrowStatus{}, ErrUnsupported
	}
	database, err := customerDatabase(ctx, s.store, accountID, id)
	if err != nil {
		return api.ManagedPostgresAutoGrowStatus{}, err
	}
	if maxBytes > 0 && !s.operationsEnabled() {
		return api.ManagedPostgresAutoGrowStatus{}, ErrProvisioningClosed
	}
	backend, err := s.registry.Resolve(database.BackendID, database.BackendFingerprint)
	if err != nil {
		return api.ManagedPostgresAutoGrowStatus{}, err
	}
	if backend.Driver != "firecracker" || maxBytes > 0 && !autoGrowEligible(database) {
		return api.ManagedPostgresAutoGrowStatus{}, ErrUnsupported
	}
	if maxBytes < 0 || maxBytes > 0 && maxBytes < database.Spec.StorageLimitBytes {
		return api.ManagedPostgresAutoGrowStatus{}, fmt.Errorf("%w: max_bytes must be at least the current volume size", ErrInvalid)
	}
	if maxBytes > 0 {
		target := database.Spec
		target.StorageLimitBytes = maxBytes
		if err := backend.Capabilities.Supports(target); err != nil {
			return api.ManagedPostgresAutoGrowStatus{}, err
		}
		if s.admitResize != nil {
			if err := s.admitResize(ctx, PolicySubject(ctx, accountID), target); err != nil {
				return api.ManagedPostgresAutoGrowStatus{}, err
			}
		}
	}
	key, err := postgresUUID(id)
	if err != nil {
		return api.ManagedPostgresAutoGrowStatus{}, err
	}
	_, err = store.pool.Exec(ctx, `
		INSERT INTO native_postgres_auto_grow AS policy (database_id, max_bytes, updated_at)
		VALUES ($1, $2, clock_timestamp())
		ON CONFLICT (database_id) DO UPDATE SET
			max_bytes = EXCLUDED.max_bytes, updated_at = EXCLUDED.updated_at,
			checked_at = NULL, reason = 'waiting_for_pressure'
		WHERE policy.max_bytes <> EXCLUDED.max_bytes`, key, maxBytes)
	if err != nil {
		return api.ManagedPostgresAutoGrowStatus{}, mapPostgresError(err)
	}
	reason := "waiting_for_pressure"
	if maxBytes == 0 {
		reason = "disabled"
	}
	return api.ManagedPostgresAutoGrowStatus{MaxBytes: maxBytes, Reason: reason}, nil
}

func autoGrowEligible(d Database) bool {
	return d.Spec.Availability == AvailabilitySingleZone &&
		d.EnvironmentCloneOperationID == "" && d.Preview.SourceDatabaseID == ""
}

// Idle growth binds to the last natural park. Scheduled growth binds to
// the current running incarnation and the window that can authorize its stop.
func (s *Service) autoGrowSweep(ctx context.Context, limit int) error {
	store, ok := s.store.(*PostgresStore)
	if !ok || !s.operationsEnabled() || s.autoGrowAllowance == nil {
		return nil
	}
	rows, err := store.pool.Query(ctx, `
		SELECT d.id::text, (CASE WHEN o.personal_org THEN d.account_id ELSE d.org_id END)::text,
			p.max_bytes, v.volume_bytes, v.idle_parked_at, v.state_changed_at, p.updated_at,
			period.starts, period.starts + make_interval(mins => w.duration_minutes)
		FROM native_postgres_auto_grow p
		JOIN managed_postgres_databases d ON d.id = p.database_id
		JOIN orgs o ON o.id = d.org_id
		JOIN database_vms v ON v.database_id::text = d.provider_resource_id
		JOIN native_postgres_storage_samples s ON s.database_id = v.database_id AND s.role = 'primary'
		LEFT JOIN native_postgres_maintenance_windows w ON w.database_id = v.database_id AND v.always_on
		CROSS JOIN LATERAL (SELECT native_postgres_open_window(w.weekday, w.start_minute, w.duration_minutes, statement_timestamp()) AS starts) period
		WHERE p.max_bytes > v.volume_bytes AND d.state = 'ready' AND d.data_resource_id IS NOT NULL
			AND d.environment_clone_operation_id IS NULL AND d.preview_source_database_id IS NULL
			AND NOT v.replicated AND NOT v.resize_pending AND NOT v.held
			AND d.availability = 'single_zone' AND v.volume_bytes = d.storage_limit_bytes
			AND s.allocated_bytes = v.volume_bytes AND s.node_id = v.node_id
			AND s.sampled_at >= $1 AND s.low AND s.low_since IS NOT NULL
			AND s.sampled_at - greatest(s.low_since, p.updated_at) >= interval '1 minute'
			AND ((d.scale_to_zero AND NOT v.always_on AND v.state = 'parked'
				AND v.idle_parked_at = v.state_changed_at AND s.state_changed_at = v.idle_parked_from
				AND s.sampled_at <= v.idle_parked_at AND (p.checked_at IS NULL OR p.checked_at < v.idle_parked_at))
				OR (NOT d.scale_to_zero AND v.always_on AND v.state = 'running' AND v.standby_node IS NULL
				AND s.state_changed_at = v.state_changed_at AND period.starts IS NOT NULL
				AND (p.checked_at IS NULL OR p.checked_at < period.starts)
				AND NOT EXISTS (SELECT 1 FROM native_postgres_base_refreshes a WHERE a.database_id = v.database_id
					AND (a.window_start = period.starts OR a.finished_at IS NULL))))
		ORDER BY COALESCE(v.idle_parked_at, period.starts), d.id LIMIT $2`, s.now().Add(-dbstorage.FreshFor), limit)
	if err != nil {
		return err
	}
	var candidates []autoGrowCandidate
	for rows.Next() {
		var c autoGrowCandidate
		var idleAt, windowStart, windowEnd pgtype.Timestamptz
		if err := rows.Scan(&c.databaseID, &c.subject, &c.cap, &c.size, &idleAt, &c.runningAt, &c.updatedAt,
			&windowStart, &windowEnd); err != nil {
			rows.Close()
			return err
		}
		c.window = dbmaintenance.Interval{Start: windowStart.Time, End: windowEnd.Time}
		if !windowStart.Valid {
			c.idleAt = idleAt.Time
		}
		candidates = append(candidates, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, c := range candidates {
		if err := s.processAutoGrow(ctx, store, c); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) processAutoGrow(ctx context.Context, store *PostgresStore, c autoGrowCandidate) error {
	d, err := store.DatabaseOwnership(ctx, c.databaseID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err
	}
	scheduled := !c.window.Start.IsZero()
	if !autoGrowEligible(d) || d.Spec.ScaleToZero == scheduled {
		return nil
	}
	backend, err := s.registry.Resolve(d.BackendID, d.BackendFingerprint)
	if err != nil || backend.Driver != "firecracker" {
		return nil //nolint:nilerr // A missing backend skips this database and leaves other candidates eligible.
	}
	scope := WithOwnership(ctx, d.OrgID, c.subject)
	allowance, err := s.autoGrowAllowance(scope, c.subject)
	if err != nil && !errors.Is(err, ErrQuotaExceeded) && !errors.Is(err, ErrNotInPlan) {
		return err
	}
	opportunity := c.idleAt
	if scheduled {
		opportunity = c.window.Start
	}
	reason := "storage_allowance"
	if allowance > 0 {
		token := uuid.NewSHA1(autoGrowNamespace, []byte(c.databaseID+"/"+opportunity.UTC().Format(time.RFC3339Nano))).String()
		operation, resizeErr := s.Resize(scope, ResizeDatabaseRequest{
			AccountID: d.AccountID, DatabaseID: d.ID, RequestID: token,
			TargetStorageLimitBytes: autoGrowTarget(c.size, c.cap), Automatic: true,
			autoGrowPoolBytes: allowance, autoGrowIdleAt: c.idleAt, autoGrowPolicyAt: c.updatedAt,
			autoGrowWindow: c.window, autoGrowRunningAt: c.runningAt,
		})
		switch {
		case resizeErr == nil:
			reason = "resize_queued"
			if operation.State == ResizePending {
				_, _ = s.Reconcile(scope, d.AccountID, d.ID)
			}
		case errors.Is(resizeErr, ErrQuotaExceeded), errors.Is(resizeErr, ErrNotInPlan):
		case errors.Is(resizeErr, ErrConflict):
			reason = "resize_conflict"
		case errors.Is(resizeErr, ErrProvisioningClosed), errors.Is(resizeErr, ErrUnsupported), errors.Is(resizeErr, ErrInvalid):
			reason = "resize_unavailable"
		default:
			return resizeErr
		}
	}
	_, err = store.pool.Exec(ctx, `UPDATE native_postgres_auto_grow
		SET checked_at = clock_timestamp(), reason = $3
		WHERE database_id = $1 AND updated_at = $2
			AND (checked_at IS NULL OR checked_at < $4)`, c.databaseID, c.updatedAt, reason, opportunity)
	return err
}

func reserveAutoGrow(ctx context.Context, tx pgx.Tx, expected Database, operation ResizeOperation, id pgtype.UUID) error {
	// Lock the billing subject so parallel automatic grows cannot reserve
	// the same bytes. The ordinary journal already locks each lifecycle.
	subject := PolicySubject(ctx, expected.AccountID)
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext('native-postgres-auto-grow'), hashtext($1))", subject); err != nil {
		return err
	}
	if err := verifyAutoGrowCandidate(ctx, tx, expected, operation, id); err != nil {
		return err
	}
	var allocated int64
	err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(GREATEST(d.storage_limit_bytes, COALESCE(r.target_storage_limit_bytes, 0))), 0)::bigint
		FROM managed_postgres_databases d JOIN orgs o ON o.id = d.org_id
		LEFT JOIN managed_postgres_resizes r ON r.database_id = d.id AND r.state = 'pending'
		WHERE (CASE WHEN o.personal_org THEN d.account_id ELSE d.org_id END) = $1::uuid`, subject).Scan(&allocated)
	if err != nil {
		return err
	}
	if allocated > operation.autoGrowPoolBytes-(operation.TargetStorageLimitBytes-expected.Spec.StorageLimitBytes) {
		return ErrQuotaExceeded
	}
	return nil
}

func verifyAutoGrowCandidate(ctx context.Context, tx pgx.Tx, expected Database, operation ResizeOperation, id pgtype.UUID) error {
	var maxBytes int64
	err := tx.QueryRow(ctx, `SELECT p.max_bytes
		FROM native_postgres_auto_grow p
		JOIN database_vms v ON v.database_id::text = $2
		JOIN native_postgres_storage_samples obs ON obs.database_id = v.database_id AND obs.role = 'primary'
		LEFT JOIN native_postgres_maintenance_windows w ON w.database_id = v.database_id AND v.always_on
		WHERE p.database_id = $1 AND p.updated_at = $3 AND p.max_bytes >= $4
			AND NOT v.replicated AND NOT v.held AND NOT v.resize_pending
			AND v.volume_bytes = $6 AND obs.allocated_bytes = $6 AND obs.node_id = v.node_id
			AND obs.sampled_at >= clock_timestamp() - interval '5 minutes'
			AND obs.low AND obs.low_since IS NOT NULL
			AND obs.sampled_at - greatest(obs.low_since, p.updated_at) >= interval '1 minute'
			AND ((NOT v.always_on AND v.state = 'parked' AND v.idle_parked_at = $5 AND v.state_changed_at = $5
				AND obs.state_changed_at = v.idle_parked_from)
				OR (v.always_on AND v.state = 'running' AND v.standby_node IS NULL AND v.state_changed_at = $7
				AND obs.state_changed_at = $7
				AND native_postgres_open_window(w.weekday, w.start_minute, w.duration_minutes, clock_timestamp()) = $8
				AND $9::timestamptz = $8::timestamptz + make_interval(mins => w.duration_minutes)
				AND NOT EXISTS (SELECT 1 FROM native_postgres_base_refreshes a WHERE a.database_id = v.database_id
					AND (a.window_start = $8 OR a.finished_at IS NULL))))
		FOR SHARE OF p`,
		id, expected.ProviderResourceID, operation.autoGrowPolicyAt, operation.TargetStorageLimitBytes,
		operation.autoGrowIdleAt, expected.Spec.StorageLimitBytes, operation.autoGrowRunningAt,
		operation.autoGrowWindow.Start, operation.autoGrowWindow.End).Scan(&maxBytes)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	return err
}

// AutoGrowStatus reads the policy and journal for a customer-scoped database
// whose native storage status is already authorized.
func (s *Service) AutoGrowStatus(ctx context.Context, d Database, storage *api.ManagedPostgresStorageStatus) (*api.ManagedPostgresAutoGrowStatus, error) {
	store, ok := s.store.(*PostgresStore)
	if !ok || storage == nil {
		return nil, nil
	}
	key, err := postgresUUID(d.ID)
	if err != nil {
		return nil, err
	}
	out := api.ManagedPostgresAutoGrowStatus{Reason: "disabled"}
	var recordedReason string
	var checkedAt, idleAt, lastGrown pgtype.Timestamptz
	var lastID, lastState, lastError string
	var targetBytes int64
	var hasWindow, windowOpen, usedWindow bool
	err = store.pool.QueryRow(ctx, `
		SELECT p.max_bytes, p.reason, p.checked_at, v.idle_parked_at,
			COALESCE(latest.id::text, ''), COALESCE(latest.state, ''), COALESCE(latest.error_code, ''),
			COALESCE(latest.target_storage_limit_bytes, 0), w.database_id IS NOT NULL, period.starts IS NOT NULL,
			EXISTS (SELECT 1 FROM native_postgres_base_refreshes a
				WHERE a.database_id = v.database_id AND a.window_start = period.starts),
			(SELECT max(r.completed_at) FROM managed_postgres_resizes r
			 WHERE r.database_id = p.database_id AND r.automatic AND r.state = 'succeeded')
		FROM native_postgres_auto_grow p
		LEFT JOIN database_vms v ON v.database_id::text = $2
		LEFT JOIN native_postgres_maintenance_windows w ON w.database_id = v.database_id AND v.always_on
		CROSS JOIN LATERAL (SELECT native_postgres_open_window(w.weekday, w.start_minute, w.duration_minutes, statement_timestamp()) AS starts) period
		LEFT JOIN LATERAL (
			SELECT id, state, error_code, target_storage_limit_bytes FROM managed_postgres_resizes r
			WHERE r.database_id = p.database_id AND r.automatic AND r.created_at >= p.updated_at
			ORDER BY r.created_at DESC, r.id DESC LIMIT 1
		) latest ON true
		WHERE p.database_id = $1`, key, d.ProviderResourceID).
		Scan(&out.MaxBytes, &recordedReason, &checkedAt, &idleAt, &lastID, &lastState, &lastError,
			&targetBytes, &hasWindow, &windowOpen, &usedWindow, &lastGrown)
	if errors.Is(err, pgx.ErrNoRows) {
		return &out, nil
	}
	if err != nil {
		return nil, err
	}
	if lastGrown.Valid {
		at := lastGrown.Time.UTC()
		out.LastGrownAt = &at
	}
	switch {
	case lastState == string(ResizePending):
		out.Reason, out.ResizeID = "resize_queued", lastID
		out.ProposedBytes = targetBytes
	case out.MaxBytes == 0:
	case !autoGrowEligible(d):
		out.Reason = "database_ineligible"
	case out.MaxBytes <= d.Spec.StorageLimitBytes:
		out.Reason = "ceiling_reached"
	case !d.Spec.ScaleToZero:
		out.Reason = scheduledGrowthReason(storage.Primary, hasWindow, windowOpen, usedWindow, recordedReason, lastState, lastError)
		if storage.Primary != nil && storage.Primary.Fresh && storage.Primary.Low {
			out.ProposedBytes = autoGrowTarget(d.Spec.StorageLimitBytes, out.MaxBytes)
		}
	case idleAt.Valid && checkedAt.Valid && !checkedAt.Time.Before(idleAt.Time) && recordedReason != "resize_queued":
		out.Reason = recordedReason
	case lastState == string(ResizeFailed) && idleAt.Valid:
		out.Reason, out.ResizeID = lastError, lastID
	case storage.Primary != nil && storage.Primary.Fresh && storage.Primary.Low:
		out.Reason = "waiting_for_idle"
		out.ProposedBytes = autoGrowTarget(d.Spec.StorageLimitBytes, out.MaxBytes)
	default:
		out.Reason = "waiting_for_pressure"
	}
	return &out, nil
}

func scheduledGrowthReason(primary *api.ManagedPostgresVolumeStatus, hasWindow, windowOpen, usedWindow bool, reason, lastState, lastError string) string {
	switch {
	case primary != nil && primary.Fresh && primary.ReadOnly:
		return "manual_resize_required"
	case !hasWindow:
		return "maintenance_window_required"
	case primary == nil || !primary.Fresh || !primary.Low:
		return "waiting_for_pressure"
	case lastState == string(ResizeFailed) && (windowOpen || lastError == "node_capacity"):
		return lastError
	case !windowOpen || usedWindow:
		return "waiting_for_maintenance_window"
	case reason != "waiting_for_pressure" && reason != "resize_queued":
		return reason
	default:
		return "waiting_for_sustained_pressure"
	}
}
