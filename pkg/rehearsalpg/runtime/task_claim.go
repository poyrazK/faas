package runtime

import (
	"context"
	"encoding/hex"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/rehearsalpg/runtime/sqlc"
	"github.com/onebox-faas/faas/pkg/rehearsalwork"
	"github.com/onebox-faas/faas/pkg/tasktoolrehearsal"
)

// TaskClaim names the live lease reported by a worker. It contains no lease
// token or credential. The observer checks it in the reserved namespace.
type TaskClaim struct {
	TaskID, DeploymentID, WorkerID, LeaseSHA256 string
	Attempt                                     int
}

type TaskClaimObserver func(context.Context, TaskClaim) (bool, error)

// TaskObservation keeps both read-only checks bound to the same credential and namespace.
type TaskObservation struct {
	Claim  TaskClaimObserver
	Result func(context.Context, string) (tasktoolrehearsal.ObservedResult, error)
}

// TaskObserver retains the startup-only claim check for callers of that path.
func (s *Service) TaskObserver(ctx context.Context, account, run, app, connection string) (TaskClaimObserver, error) {
	observer, err := s.TaskObservations(ctx, account, run, app, connection)
	return observer.Claim, err
}

// TaskObservations keeps the restricted login inside the owning rehearsal. The
// host, database and user come from the dedicated backend and live catalog.
// The guest connection string supplies only the freshly issued password.
func (s *Service) TaskObservations(ctx context.Context, account, run, app, connection string) (TaskObservation, error) {
	p, ok := s.Backend.(*Postgres)
	if !ok {
		return TaskObservation{}, ErrFixture
	}
	resource, err := s.Catalog.GetScenarioPostgres(ctx, account, run)
	if err != nil || !p.resourceNames(resource) || resource.OwnerID != s.OwnerID || resource.Status != "ready" {
		return TaskObservation{}, ErrFixture
	}
	bound, err := s.Catalog.ScenarioPostgresForApp(ctx, app)
	role := rehearsalwork.PostgresParticipantRole(resource.ID, app, rehearsalwork.PostgresTaskObserver)
	if err != nil || bound.ID != resource.ID || bound.ParticipantAccess != rehearsalwork.PostgresTaskObserver || bound.RoleName != role {
		return TaskObservation{}, ErrFixture
	}
	guest, err := url.Parse(connection)
	if err != nil || guest.User == nil || guest.User.Username() != role || guest.Path != "/"+resource.DatabaseName {
		return TaskObservation{}, ErrFixture
	}
	password, set := guest.User.Password()
	if !set || !validTaskLeaseDigest(password) {
		return TaskObservation{}, ErrFixture
	}
	runID, err := uuid.Parse(run)
	if err != nil || strings.ReplaceAll(runID.String(), "-", "") != run || runID == uuid.Nil {
		return TaskObservation{}, ErrFixture
	}

	config := p.config.Copy()
	config.User, config.Password, config.Database = role, password, resource.DatabaseName
	// Retain server verification on TLS fixtures. Authenticate only with
	// the observer login, even when the administrator uses a client certificate.
	if config.TLSConfig != nil {
		config.TLSConfig.Certificates = nil
		config.TLSConfig.GetClientCertificate = nil
	}
	config.RuntimeParams = map[string]string{"application_name": "gregale_rehearsal_task_observer", "statement_timeout": "5000", "search_path": "pg_catalog"}

	namespace := "rehearsal-" + runID.String()
	return TaskObservation{
		Claim: func(ctx context.Context, claim TaskClaim) (bool, error) {
			if !resource.ExpiresAt.After(time.Now()) {
				return false, ErrFixture
			}
			return observeTaskClaim(ctx, config, namespace, claim)
		},
		Result: func(ctx context.Context, taskID string) (tasktoolrehearsal.ObservedResult, error) {
			if !resource.ExpiresAt.After(time.Now()) {
				return tasktoolrehearsal.ObservedResult{}, ErrFixture
			}
			return observeTaskResult(ctx, config, namespace, taskID)
		},
	}, nil
}

func observeTaskClaim(ctx context.Context, config *pgx.ConnConfig, namespace string, claim TaskClaim) (bool, error) {
	task, taskErr := uuid.Parse(claim.TaskID)
	worker, workerErr := uuid.Parse(claim.WorkerID)
	if taskErr != nil || workerErr != nil || task == uuid.Nil || worker == uuid.Nil ||
		task.String() != claim.TaskID || worker.String() != claim.WorkerID || claim.Attempt < 1 || claim.Attempt > 2 ||
		!validTaskLeaseDigest(claim.LeaseSHA256) {
		return false, ErrFixture
	}
	return readTaskObservation(ctx, config, func(tx pgx.Tx) (bool, error) {
		return sqlc.New().ObserveTaskClaim(ctx, tx, sqlc.ObserveTaskClaimParams{
			Namespace: namespace, TaskID: pgtype.UUID{Bytes: task, Valid: true}, WorkerID: pgtype.UUID{Bytes: worker, Valid: true},
			Attempt: int32(claim.Attempt), LeaseSha256: claim.LeaseSHA256, DeploymentID: claim.DeploymentID,
		})
	})
}

func observeTaskResult(ctx context.Context, config *pgx.ConnConfig, namespace, taskID string) (tasktoolrehearsal.ObservedResult, error) {
	var result tasktoolrehearsal.ObservedResult
	task, err := uuid.Parse(taskID)
	if err != nil || task == uuid.Nil || task.String() != taskID {
		return result, ErrFixture
	}
	row, err := readTaskObservation(ctx, config, func(tx pgx.Tx) (sqlc.ObserveTaskResultRow, error) {
		return sqlc.New().ObserveTaskResult(ctx, tx, sqlc.ObserveTaskResultParams{
			Namespace: namespace, TaskID: pgtype.UUID{Bytes: task, Valid: true},
		})
	})
	if err != nil {
		return result, err
	}
	return tasktoolrehearsal.ObservedResult{TaskID: taskID, Namespace: namespace, Attempt: int(row.Attempt),
		TaskRows: int(row.TaskRows), CompletedResultRows: int(row.CompletedResultRows)}, nil
}

func readTaskObservation[T any](ctx context.Context, config *pgx.ConnConfig, read func(pgx.Tx) (T, error)) (T, error) {
	var zero T
	conn, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		return zero, ErrFixture
	}
	defer closeTaskParticipantConnection(ctx, conn)
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return zero, ErrFixture
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	value, err := read(tx)
	if err != nil {
		return zero, ErrFixture
	}
	return value, nil
}

func validTaskLeaseDigest(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
