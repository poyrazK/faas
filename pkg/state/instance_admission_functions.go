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
		and to_regprocedure('admission_reserve_node(uuid,bigint,integer,boolean,integer)') is not null
		and to_regprocedure('publish_instance_on_generation(uuid,uuid,uuid,uuid,uuid,text,text,text,inet,integer,text,uuid,text,text,text,jsonb,text,text,boolean,text,timestamptz,jsonb,jsonb,boolean,jsonb,jsonb,bigint)') is not null`).Scan(&ok)
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
	case "instance_publication_current", "instance_runtime_config_receipt_current":
		return ErrConflict
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

// publishOnGenerationFunction is the guarded publication in one statement
// (publish_instance_on_generation): the compare-and-swap, the config proof
// with the wake's event and notification, the runtime-config receipt and
// the runtime-generation check, taken last.
func (s *PgStore) publishOnGenerationFunction(ctx context.Context, p RuntimeInstancePublication, guard runtimeGenerationGuard) (Instance, error) {
	var eventKind, notifyChannel, notifyPayload *string
	var eventData []byte
	if b := p.Bookkeeping; b != nil {
		if b.EventKind != "" {
			eventKind, eventData = &b.EventKind, b.EventData
		}
		if b.NotifyChannel != "" {
			notifyChannel, notifyPayload = &b.NotifyChannel, &b.NotifyPayload
		}
	}
	var environmentID *string
	if p.Fence.EnvironmentID != "" {
		environmentID = &p.Fence.EnvironmentID
	}
	recordReceipt := p.Inputs != nil && p.targetState() == string(StateRunning)
	var receiptScope string
	var boundary any
	variables, secrets, refs, sidecars := []byte(`{}`), []byte(`{}`), []byte(`{}`), []byte(`{}`)
	allSecrets := false
	if recordReceipt {
		receiptScope, boundary, allSecrets = p.Inputs.Scope, gitOpsTime(p.Inputs.Boundary), p.Inputs.AllSecrets
		variables, secrets, refs, sidecars = runtimeConfigInputsJSON(*p.Inputs)
	}
	var backoffRecorded bool
	row := s.pool.QueryRow(ctx, `
	select (r.published).id, coalesce((r.published).app_id::text, ''), coalesce((r.published).deployment_id::text, ''), (r.published).state,
	       coalesce((r.published).netns,''), coalesce((r.published).guest_uid,0), coalesce(host((r.published).host_ip),''), (r.published).ram_mb,
	       (r.published).started_at, (r.published).last_request_at, (r.published).parked_at, (r.published).node_id, (r.published).wake_id,
	       (r.published).framework_ready_at, (r.published).tail_count, (r.published).mode, (r.published).request_count,
	       r.snapshot_backoff_recorded
	  from publish_instance_on_generation($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, $6::text, $7::text, $8::text, $9::inet,
	       $10::integer, $11::text, $12::uuid, $13::text, $14::text, $15::text, $16::jsonb, $17::text, $18::text, $19::boolean,
	       $20::text, $21::timestamptz, $22::jsonb, $23::jsonb, $24::boolean, $25::jsonb, $26::jsonb, $27::bigint) r`,
		p.InstanceID, p.AppID, p.Fence.DeploymentID, p.NodeID, p.WakeID, p.ExpectedState, p.targetState(), p.Netns, p.HostIP,
		p.GuestUID, p.ConfigFence.Fingerprint, environmentID, p.Fence.Scope, p.Fence.Fingerprint, eventKind, eventData,
		notifyChannel, notifyPayload, recordReceipt, receiptScope, boundary, variables, secrets, allSecrets, refs, sidecars,
		guard.generation)
	instance, err := scanInstanceCols(func(dest ...any) error { return row.Scan(append(dest, &backoffRecorded)...) })
	if err != nil {
		if mapped := admissionFunctionError(err, p.NodeID); mapped != err {
			return Instance{}, mapped
		}
		return Instance{}, runtimeSecretFenceError(err)
	}
	if p.Bookkeeping != nil {
		p.Bookkeeping.SnapshotBackoffRecorded = backoffRecorded
	}
	return instance, nil
}
