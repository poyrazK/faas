package state

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/onebox-faas/faas/pkg/api"
)

// admissionFunctionProbe remembers whether the database has the instance
// admission functions (migration 20261018153840637, ADR-1741) in the form
// that takes a reservation allowance (migration 20261026090412734, ADR-909
// amendment 1), the admission's vCPU and CPU (migration 20261027220445082,
// ADR-20261027220445) and the app owner fence (migration 20261028200212696,
// ADR-20261028200212). A schedd can start before its
// release's migrations run; until they do, admission keeps its
// statement-by-statement transaction. A missing function is probed again
// after a minute. discount records whether the database also has the forms
// that take the idle ladder's reservation discount (ADR-20261107122928);
// without them admission calls the previous forms and books no discount.
type admissionFunctionProbe struct {
	mu                  sync.Mutex
	known, ok, discount bool
	checked             time.Time
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
	var ok, discount bool
	err := s.pool.QueryRow(ctx, `select to_regprocedure('admit_wake_instance(uuid,text,text,integer,uuid,text,text,bigint,boolean,integer,boolean,uuid,bigint,integer,integer,integer,integer,integer,integer,integer,uuid)') is not null
		and to_regprocedure('admission_check_app_owner(uuid,uuid)') is not null
		and to_regprocedure('admission_reserve_node(uuid,bigint,integer,boolean,integer,integer,integer,integer,integer,integer,integer,integer)') is not null
		and to_regprocedure('snapshot_restore_pressure_acquire(text)') is not null
		and to_regprocedure('publish_instance_on_generation(uuid,uuid,uuid,uuid,uuid,text,text,text,inet,integer,text,uuid,text,text,text,jsonb,text,text,boolean,text,timestamptz,jsonb,jsonb,boolean,jsonb,jsonb,bigint)') is not null,
		to_regprocedure('admit_wake_instance(uuid,text,text,integer,uuid,text,text,bigint,boolean,integer,boolean,uuid,bigint,integer,integer,integer,integer,integer,integer,integer,uuid,integer)') is not null
		and to_regprocedure('admission_reserve_node(uuid,bigint,integer,boolean,integer,integer,integer,integer,integer,integer,integer,integer,integer)') is not null`).Scan(&ok, &discount)
	if err != nil {
		return false
	}
	p.known, p.ok, p.discount, p.checked = true, ok, ok && discount, time.Now()
	return ok
}

// admissionDiscountUsable reports whether the last probe found the admission
// functions that take the idle ladder's reservation discount. Call it after
// admissionFunctionsUsable.
func (s *PgStore) admissionDiscountUsable() bool {
	p := s.admission
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.known && p.discount
}

func (s *PgStore) markAdmissionFunctionsMissing() {
	if p := s.admission; p != nil {
		p.mu.Lock()
		p.known, p.ok, p.checked = true, false, time.Now()
		p.mu.Unlock()
	}
}

// reserveNodeDB is checkNodeAdmissionDB, the per-node advisory lock, the
// RAM, vCPU and CPU headroom checks and ctx's app owner fence in one
// statement, inside the caller's transaction.
// discount selects the form that takes ctx's reservation discount
// (ADR-20261107122928).
func reserveNodeDB(ctx context.Context, tx pgx.Tx, nodeID, newState string, ramMB int, discount bool) error {
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
	compute := nodeComputeAdmission(ctx, nodeID)
	fenceAppID, fenceOwner := fenceParams(ctx)
	args := []any{nodeID, expectedGeneration, ramMB, nodeUsageCounts(State(newState)), api.PerVMOverheadMB, nodeReservationPercent(ctx, nodeID),
		compute.VCPU, compute.QuietVCPU, compute.CPUMillicores, compute.QuietCPUMillicores, api.DefaultAppCPUMillicores, api.CPUOvercommit,
		fenceAppID, fenceOwner}
	query := `select admission_reserve_node($1::uuid, $2::bigint, $3::integer, $4::boolean, $5::integer, $6::integer,
		$7::integer, $8::integer, $9::integer, $10::integer, $11::integer, $12::integer),
		admission_check_app_owner($13::uuid, $14::uuid)`
	if discount {
		args = append(args, nodeReservationDiscountMB(ctx, nodeID))
		query = `select admission_reserve_node($1::uuid, $2::bigint, $3::integer, $4::boolean, $5::integer, $6::integer,
		$7::integer, $8::integer, $9::integer, $10::integer, $11::integer, $12::integer, $15::integer),
		admission_check_app_owner($13::uuid, $14::uuid)`
	}
	_, err = tx.Exec(ctx, query, args...)
	return admissionFunctionError(err, nodeID)
}

// reserveThawDB runs the durable node reservation for a frozen VM going back
// into service, inside the caller's transaction, before its row leaves the
// frozen state: a frozen row is outside the node's bookings, so a thaw adds
// its plan RAM, vCPU and CPU back like an admission (ADR 20261102072403). It
// uses ctx's thaw binding (WithThawAdmission) and no placement generation;
// the node must still admit. A database without the admission functions
// keeps the previous unchecked thaw.
func (s *PgStore) reserveThawDB(ctx context.Context, tx pgx.Tx, instanceID, nodeID string) error {
	if !validUUID(instanceID) || !validUUID(nodeID) || !s.admissionFunctionsUsable(ctx) {
		return nil
	}
	percent, c, discountMB := thawAdmission(ctx, nodeID)
	args := []any{instanceID, nodeID, api.PerVMOverheadMB, percent,
		c.VCPU, c.QuietVCPU, c.CPUMillicores, c.QuietCPUMillicores, api.DefaultAppCPUMillicores, api.CPUOvercommit}
	query := `select admission_reserve_node(i.node_id, NULL::bigint, i.ram_mb, true, $3::integer, $4::integer,
		$5::integer, $6::integer, $7::integer, $8::integer, $9::integer, $10::integer)
		  from instances i where i.id = $1::uuid and i.node_id = $2::uuid`
	if s.admissionDiscountUsable() {
		args = append(args, discountMB)
		query = `select admission_reserve_node(i.node_id, NULL::bigint, i.ram_mb, true, $3::integer, $4::integer,
		$5::integer, $6::integer, $7::integer, $8::integer, $9::integer, $10::integer, $11::integer)
		  from instances i where i.id = $1::uuid and i.node_id = $2::uuid`
	}
	_, err := tx.Exec(ctx, query, args...)
	return admissionFunctionError(err, nodeID)
}

// fenceParams is ctx's app owner fence as nullable function arguments.
func fenceParams(ctx context.Context) (appID, ownerNodeID *string) {
	app, owner, ok := fencedApp(ctx)
	if !ok {
		return nil, nil
	}
	return &app, &owner
}

// admitWakeInstance makes a wake's whole admission in one statement: the
// node checks, the instance insert, the snapshot-recovery binding ctx asks
// for and the runtime-generation check ctx carries. handled is false when
// the database lacks the functions or the request needs the transaction
// (another app's guard), and the caller admits as before.
func (s *PgStore) admitWakeInstance(ctx context.Context, appID, deploymentID, newState string, ramMB int, nodeID, wakeID, mode string) (Instance, bool, error) {
	// An unparsable node id is the transaction path's to refuse.
	if !validUUID(nodeID) {
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
	compute := nodeComputeAdmission(ctx, nodeID)
	_, fenceOwner := fenceParams(fenceApp(ctx, appID))
	booked := nodeBookedCPUMillicores(ctx, nodeID)
	bookedVCPU := nodeBookedVCPU(ctx, nodeID)
	// ADR-20261107122928: the form that takes the reservation discount, when
	// the database has it.
	admitArgs := []any{appID, deploymentID, newState, ramMB, nodeID, wakeID, mode,
		expectedGeneration, nodeUsageCounts(State(newState)), api.PerVMOverheadMB, binding, snapshotID, guardGeneration,
		nodeReservationPercent(ctx, nodeID),
		compute.VCPU, compute.QuietVCPU, compute.CPUMillicores, compute.QuietCPUMillicores, api.DefaultAppCPUMillicores, api.CPUOvercommit,
		fenceOwner}
	admitCall := `admit_wake_instance($1::uuid, $2::text, $3::text, $4::integer, $5::uuid, $6::text, $7::text,
		                           $8::bigint, $9::boolean, $10::integer, $11::boolean, $12::uuid, $13::bigint, $14::integer,
		                           $15::integer, $16::integer, $17::integer, $18::integer, $19::integer, $20::integer, $21::uuid)`
	if s.admissionDiscountUsable() {
		admitArgs = append(admitArgs, nodeReservationDiscountMB(ctx, nodeID))
		admitCall = `admit_wake_instance($1::uuid, $2::text, $3::text, $4::integer, $5::uuid, $6::text, $7::text,
		                           $8::bigint, $9::boolean, $10::integer, $11::boolean, $12::uuid, $13::bigint, $14::integer,
		                           $15::integer, $16::integer, $17::integer, $18::integer, $19::integer, $20::integer, $21::uuid, $22::integer)`
	}
	inst, err := retryRuntimeAdmissionBusy(ctx, func() (Instance, error) {
		admit := func(queryRow func(context.Context, string, ...any) pgx.Row) (Instance, error) {
			row := queryRow(ctx, `
		select id, coalesce(app_id::text, ''), coalesce(deployment_id::text, ''), state, coalesce(netns,''), coalesce(guest_uid,0),
		       coalesce(host(host_ip),''), ram_mb, started_at, last_request_at, parked_at, node_id, wake_id, framework_ready_at, tail_count, mode, request_count
		  from `+admitCall, admitArgs...)
			inst, err := scanInstanceCols(row.Scan)
			if err == nil {
				return inst, nil
			}
			if mapped, ok := mapAdmissionFunctionError(err, nodeID); ok {
				return Instance{}, mapped
			}
			return scanCreatedInstance(errorRow{err}, wakeID, appID)
		}
		if booked <= 0 && bookedVCPU <= 0 {
			return admit(s.pool.QueryRow)
		}
		return admitWithBookings(ctx, s.pool, booked, bookedVCPU, admit)
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
	if mapped, ok := mapAdmissionFunctionError(err, nodeID); ok {
		return mapped
	}
	return err
}

// mapAdmissionFunctionError reports whether err is one of the admission
// functions' refusals, and the error it stands for.
func mapAdmissionFunctionError(err error, nodeID string) (error, bool) {
	var pgErr *pgconn.PgError
	if err == nil || !errors.As(err, &pgErr) {
		return nil, false
	}
	switch pgErr.ConstraintName {
	case "instance_publication_current", "instance_runtime_config_receipt_current", "instance_snapshot_recovery_current":
		return ErrConflict, true
	case "instance_node_admission_current":
		return ErrNodeAdmissionStale, true
	case "instance_node_capacity":
		return fmt.Errorf("state: %w: node=%s %s", ErrNodeCapacity, nodeID, strings.TrimSpace(pgErr.Detail)), true
	case "app_runtime_generation_current":
		return ErrRuntimeGenerationMoved, true
	case "app_owner_current":
		return fmt.Errorf("state: %w: %s", ErrAppOwnerMoved, strings.TrimSpace(pgErr.Detail)), true
	}
	return nil, false
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
		if mapped, ok := mapAdmissionFunctionError(err, p.NodeID); ok {
			return Instance{}, mapped
		}
		return Instance{}, runtimeSecretFenceError(err)
	}
	if p.Bookkeeping != nil {
		p.Bookkeeping.SnapshotBackoffRecorded = backoffRecorded
	}
	return instance, nil
}

// admitWithBookings runs a one-statement admission in a transaction that
// first sets the CPU booking and guest vCPU count capture_instance_capacity
// stores on the new row (ADR-20261101160231 step 5b, ADR-20261109172421
// step 5). A non-positive value is left unset.
func admitWithBookings(ctx context.Context, pool interface {
	Begin(context.Context) (pgx.Tx, error)
}, booked, bookedVCPU int,
	admit func(func(context.Context, string, ...any) pgx.Row) (Instance, error)) (Instance, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return Instance{}, fmt.Errorf("state: admission: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := setInstanceBookings(ctx, tx, booked, bookedVCPU); err != nil {
		return Instance{}, fmt.Errorf("state: admission: %w", err)
	}
	inst, err := admit(tx.QueryRow)
	if err != nil {
		return Instance{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Instance{}, fmt.Errorf("state: admission: commit: %w", mapErr(err))
	}
	return inst, nil
}

// setInstanceBookings sets, for the rest of tx, what capture_instance_capacity
// stores on the instance row tx inserts: the CPU booking and the guest vCPU
// count. A non-positive value is left unset, keeping the app's and the
// plan's.
func setInstanceBookings(ctx context.Context, tx pgx.Tx, bookedCPU, bookedVCPU int) error {
	if bookedCPU > 0 {
		if _, err := tx.Exec(ctx, `SELECT set_config('faas.instance_booked_cpu_millicores', $1::text, true)`, strconv.Itoa(bookedCPU)); err != nil {
			return fmt.Errorf("booked CPU: %w", err)
		}
	}
	if bookedVCPU > 0 {
		if _, err := tx.Exec(ctx, `SELECT set_config('faas.instance_booked_vcpu', $1::text, true)`, strconv.Itoa(bookedVCPU)); err != nil {
			return fmt.Errorf("booked vCPU: %w", err)
		}
	}
	return nil
}
