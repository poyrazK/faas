package state

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/onebox-faas/faas/pkg/api"
)

// admissionFunctionProbe remembers whether the database has the instance
// admission functions (migration 20261018153840637, ADR-1741). A schedd
// can start before its release's migrations run; until they do, admission
// keeps its statement-by-statement transaction. A missing function is
// probed again after a minute.
type admissionFunctionProbe struct {
	mu        sync.Mutex
	known, ok bool
	checked   time.Time
}

const admissionFunctionReprobe = time.Minute

func (s *PgStore) admissionFunctionsUsable(ctx context.Context) bool {
	p := s.admission
	if p == nil || s.pool == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.known && (p.ok || time.Since(p.checked) < admissionFunctionReprobe) {
		return p.ok
	}
	var ok bool
	err := s.pool.QueryRow(ctx, `select to_regprocedure('admit_wake_instance(uuid,text,text,integer,uuid,text,text,bigint,boolean,integer,boolean,uuid,bigint)') is not null
		and to_regprocedure('admission_reserve_node(uuid,bigint,integer,boolean,integer)') is not null`).Scan(&ok)
	if err != nil {
		return false
	}
	p.known, p.ok, p.checked = true, ok, time.Now()
	return ok
}

func (s *PgStore) markAdmissionFunctionsMissing() {
	if p := s.admission; p != nil {
		p.mu.Lock()
		p.known, p.ok, p.checked = true, false, time.Now()
		p.mu.Unlock()
	}
}

// reserveNodeDB is checkNodeAdmissionDB, the per-node advisory lock and the
// headroom check in one statement, inside the caller's transaction.
func reserveNodeDB(ctx context.Context, tx pgx.Tx, nodeID, newState string, ramMB int) error {
	expected, bound, err := nodeAdmissionExpectation(ctx, nodeID)
	if err != nil {
		return err
	}
	if _, err := uuid.Parse(nodeID); err != nil {
		return ErrInvalidArgument
	}
	var expectedGeneration *int64
	if bound {
		expectedGeneration = &expected.Generation
	}
	_, err = tx.Exec(ctx, `select admission_reserve_node($1::uuid, $2::bigint, $3::integer, $4::boolean, $5::integer)`,
		nodeID, expectedGeneration, ramMB, nodeUsageCounts(State(newState)), api.PerVMOverheadMB)
	return admissionFunctionError(err, nodeID)
}

// admitWakeInstance makes a wake's whole admission in one statement: the
// node checks, the instance insert, the snapshot-recovery binding ctx asks
// for and the runtime-generation check ctx carries. handled is false when
// the database lacks the functions or the request needs the transaction
// (another app's guard), and the caller admits as before.
func (s *PgStore) admitWakeInstance(ctx context.Context, appID, deploymentID, newState string, ramMB int, nodeID, wakeID, mode string) (Instance, bool, error) {
	if _, err := uuid.Parse(nodeID); err != nil {
		return Instance{}, false, nil
	}
	guard, guarded := runtimeGenerationGuardFrom(ctx)
	if guarded && guard.appID != appID {
		return Instance{}, false, nil
	}
	if !s.admissionFunctionsUsable(ctx) {
		return Instance{}, false, nil
	}
	expected, bound, err := nodeAdmissionExpectation(ctx, nodeID)
	if err != nil {
		return Instance{}, true, err
	}
	var expectedGeneration, guardGeneration *int64
	if bound {
		expectedGeneration = &expected.Generation
	}
	if guarded {
		guardGeneration = &guard.generation
	}
	recovery, binding := snapshotRecoveryAdmissionFrom(ctx)
	var snapshotID *string
	if binding {
		recovery.bound = false
		if recovery.SnapshotID != "" {
			if err := validateSnapshotRecoveryIDs(recovery.SnapshotID); err != nil {
				return Instance{}, true, err
			}
			snapshotID = &recovery.SnapshotID
		}
	}
	inst, err := retryRuntimeAdmissionBusy(ctx, func() (Instance, error) {
		row := s.pool.QueryRow(ctx, `
		select id, coalesce(app_id::text, ''), coalesce(deployment_id::text, ''), state, coalesce(netns,''), coalesce(guest_uid,0),
		       coalesce(host(host_ip),''), ram_mb, started_at, last_request_at, parked_at, node_id, wake_id, framework_ready_at, tail_count, mode, request_count
		  from admit_wake_instance($1::uuid, $2::text, $3::text, $4::integer, $5::uuid, $6::text, $7::text,
		                           $8::bigint, $9::boolean, $10::integer, $11::boolean, $12::uuid, $13::bigint)`,
			appID, deploymentID, newState, ramMB, nodeID, wakeID, mode,
			expectedGeneration, nodeUsageCounts(State(newState)), api.PerVMOverheadMB, binding, snapshotID, guardGeneration)
		inst, err := scanInstanceCols(row.Scan)
		if err == nil {
			return inst, nil
		}
		if mapped := admissionFunctionError(err, nodeID); mapped != err {
			return Instance{}, mapped
		}
		return scanCreatedInstance(errorRow{err}, wakeID, appID)
	})
	if undefinedFunction(err) {
		s.markAdmissionFunctionsMissing()
		return Instance{}, false, nil
	}
	if err == nil && binding {
		recovery.bound = true
	}
	return inst, true, err
}

// admissionFunctionError maps the refusals the admission functions raise to
// the errors the statement-by-statement transaction returned; any other
// error is returned as it is.
func admissionFunctionError(err error, nodeID string) error {
	var pgErr *pgconn.PgError
	if err == nil || !errors.As(err, &pgErr) {
		return err
	}
	switch pgErr.ConstraintName {
	case "instance_node_admission_current":
		return ErrNodeAdmissionStale
	case "instance_node_capacity":
		return fmt.Errorf("state: %w: node=%s %s", ErrNodeCapacity, nodeID, strings.TrimSpace(pgErr.Detail))
	case "instance_snapshot_recovery_current":
		return ErrConflict
	case "app_runtime_generation_current":
		return ErrRuntimeGenerationMoved
	}
	return err
}

func undefinedFunction(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "42883"
}

// errorRow hands an error already returned by a scan to a row consumer.
type errorRow struct{ err error }

func (r errorRow) Scan(...any) error { return r.err }
