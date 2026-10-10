package state

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ EnvironmentQualificationServiceStore = (*PgStore)(nil)
var _ EnvironmentQualificationServiceAliasStore = (*PgStore)(nil)

func qualificationNetworkInstance(ctx context.Context, db sqlc.DBTX, nodeID, hostIP string) (string, bool, error) {
	rows, err := sqlc.New().EnvironmentQualificationNetworkInstances(ctx, db, sqlc.EnvironmentQualificationNetworkInstancesParams{NodeID: nodeID, HostIp: hostIP})
	if err != nil {
		return "", false, mapErr(err)
	}
	if len(rows) > 1 {
		return "", false, ErrConflict
	}
	if len(rows) == 0 {
		return "", false, nil
	}
	return pgUUIDString(rows[0].ID), rows[0].Held, nil
}

func (s *PgStore) EnvironmentQualificationNetworkNode(ctx context.Context, nodeName, hostIP string) (string, error) {
	ip, err := netip.ParseAddr(hostIP)
	if nodeName != strings.TrimSpace(nodeName) || err != nil || !ip.Is4() || ip.String() != hostIP {
		return "", ErrInvalidArgument
	}
	rows, err := sqlc.New().EnvironmentQualificationNetworkInstances(ctx, s.pool, sqlc.EnvironmentQualificationNetworkInstancesParams{NodeName: nodeName, HostIp: hostIP})
	if err != nil {
		return "", mapErr(err)
	}
	if len(rows) > 1 {
		return "", ErrConflict
	}
	if len(rows) == 0 {
		return "", nil
	}
	return pgUUIDString(rows[0].NodeID), nil
}

func (s *PgStore) EnvironmentQualificationNetworkCaller(ctx context.Context, nodeID, hostIP string) (bool, error) {
	if !qualificationNetworkIdentityValid(nodeID, hostIP) {
		return false, ErrInvalidArgument
	}
	_, held, err := qualificationNetworkInstance(ctx, s.pool, nodeID, hostIP)
	return held, err
}

// EnvironmentQualificationServiceAliasAllowed lets a current held graph
// caller resolve only aliases present in its frozen scoped bindings. The
// request route rechecks the observed node/IP and graph before forwarding.
func (s *PgStore) EnvironmentQualificationServiceAliasAllowed(ctx context.Context, callerAppID, service string) (bool, error) {
	if !qualificationRecoveryUUIDValid(callerAppID) {
		return false, ErrInvalidArgument
	}
	service = strings.ToLower(strings.TrimSpace(service))
	if !api.ValidAppSlug(service) {
		return false, ErrInvalidArgument
	}
	const query = `
SELECT EXISTS (
 SELECT 1
 FROM environment_qualification_executions e
 JOIN environment_workload_qualification_requests q ON q.id=e.request_id
 JOIN environment_workload_graphs g ON g.id=q.graph_id
 JOIN environment_git_sources s ON s.id=g.source_id
 JOIN accounts c ON c.id=s.account_id
 JOIN apps a ON a.id=q.app_id
 JOIN instances i ON i.id=e.instance_id
 JOIN deployments d ON d.id=i.deployment_id
 WHERE e.dispatch_started AND e.retired_at IS NULL
  AND e.frame->>'instance_id'=i.id::text
  AND e.frame->>'request_id'=q.id::text
  AND e.frame->>'graph_id'=g.id::text
  AND e.frame->>'app_id'=q.app_id::text AND q.app_id=$1::uuid
  AND e.frame->>'resource'=q.resource AND e.frame->>'attempt'=q.attempt::text
  AND coalesce(e.frame->>'capture_instance_id',e.frame->>'instance_id')=q.reserved_instance_id::text
  AND e.frame->>'source_id'=s.id::text AND e.frame->>'environment_id'=g.environment_id::text
  AND e.frame->>'revision_id'=g.revision_id::text AND e.frame->>'generation'=g.generation::text
  AND e.frame->>'intent_version'=g.intent_version::text AND e.frame->>'plan_hash'=g.plan_hash
  AND e.frame->>'deployment_id'=q.deployment_id::text AND i.deployment_id=q.deployment_id
  AND e.frame->>'node_id'=i.node_id::text AND e.frame->>'wake_id'=i.wake_id::text
  AND i.app_id=q.app_id AND i.state='running' AND d.environment_workload_runtime=q.frozen_inputs
  AND a.account_id=s.account_id AND a.project_id=s.project_id AND a.status IN ('active','evicted_cold')
  AND q.phase='claimed' AND q.lease_until>clock_timestamp()
  AND g.phase='prepared' AND g.generation=s.generation AND g.intent_version=s.intent_version
  AND g.revision_id=s.approved_revision_id AND g.environment_id=s.environment_id
  AND s.mode='enforce' AND NOT s.suspended AND c.status='active' AND c.abuse_hold_at IS NULL
  AND EXISTS (
   SELECT 1 FROM environment_qualification_config_receipts config
   WHERE config.instance_id=e.instance_id AND config.request_id=q.id AND config.attempt=q.attempt
    AND config.graph_id=g.id AND config.capture_instance_id IS NOT DISTINCT FROM e.capture_instance_id
  )
  AND EXISTS (
   SELECT 1 FROM jsonb_each(coalesce(q.frozen_inputs->'service_bindings','{}'::jsonb)) AS binding(name,value)
   WHERE lower(binding.value->>'workload')=$2::text
  )
)
`
	var allowed bool
	if err := s.pool.QueryRow(ctx, query, callerAppID, service).Scan(&allowed); err != nil {
		return false, mapErr(err)
	}
	return allowed, nil
}

func (s *PgStore) qualificationServiceEndpointTx(ctx context.Context, tx pgx.Tx, request EnvironmentWorkloadQualificationRequest, instanceID string) (EnvironmentQualificationExecutionStatus, Instance, error) {
	var zero EnvironmentQualificationExecutionStatus
	q := sqlc.New()
	row, err := q.LockEnvironmentQualificationExecution(ctx, tx, mustPgUUID(instanceID))
	if err != nil {
		return zero, Instance{}, mapErr(err)
	}
	status, err := qualificationExecutionFromSQL(row)
	if err != nil {
		return zero, Instance{}, err
	}
	if status.CaptureInstanceID != "" {
		if _, err := q.SetEnvironmentWorkloadQualificationContext(ctx, tx, request.LeaseToken); err != nil {
			return zero, Instance{}, mapErr(err)
		}
		reservation, err := q.EnvironmentQualificationRestoreReservation(ctx, tx, sqlc.EnvironmentQualificationRestoreReservationParams{RequestID: mustPgUUID(request.ID), Attempt: request.Attempt})
		if err != nil || pgUUIDString(reservation.InstanceID) != instanceID || pgUUIDString(reservation.CaptureInstanceID) != request.ReservedInstanceID {
			return zero, Instance{}, ErrConflict
		}
		current, err := q.EnvironmentQualificationRestoreCurrent(ctx, tx, sqlc.EnvironmentQualificationRestoreCurrentParams{RequestID: mustPgUUID(request.ID), CaptureInstanceID: mustPgUUID(request.ReservedInstanceID)})
		if err != nil || !current {
			return zero, Instance{}, ErrConflict
		}
	} else if instanceID != request.ReservedInstanceID {
		return zero, Instance{}, ErrConflict
	}
	if _, err := q.EnvironmentQualificationAdmissionInputs(ctx, tx, sqlc.EnvironmentQualificationAdmissionInputsParams{AppID: mustPgUUID(request.AppID), NodeID: mustPgUUID(status.Execution.NodeID)}); err != nil {
		return zero, Instance{}, mapErr(err)
	}
	mayDeploy, err := q.LockEnvironmentQualificationAccount(ctx, tx, mustPgUUID(request.AppID))
	if err != nil {
		return zero, Instance{}, mapErr(err)
	}
	if !mayDeploy.Valid || !mayDeploy.Bool {
		return zero, Instance{}, ErrConflict
	}
	instance, err := q.LockEnvironmentQualificationRuntimeInstance(ctx, tx, row.InstanceID)
	if err != nil {
		return zero, Instance{}, mapErr(err)
	}
	ins := qualificationInstanceFromSQL(instance)
	if !qualificationServiceExecutionCurrent(request, status, ins, time.Now()) {
		return zero, Instance{}, ErrConflict
	}
	config, err := q.InstanceRuntimeConfigReceipt(ctx, tx, row.InstanceID)
	if err != nil {
		return zero, Instance{}, mapErr(err)
	}
	guestConfig, err := q.EnvironmentQualificationConfigReceipt(ctx, tx, row.InstanceID)
	if err != nil {
		return zero, Instance{}, mapErr(err)
	}
	if !qualificationConfigReceiptMatchesFrame(qualificationConfigReceiptFromSQL(guestConfig), request, status.Execution) {
		return zero, Instance{}, ErrConflict
	}
	if pgUUIDString(config.WakeID) != ins.WakeID || config.Scope != request.FrozenInputs.Scope {
		return zero, Instance{}, ErrConflict
	}
	inputs, err := runtimeConfigInputsFromSQL(config.Scope, config.BoundaryAt, config.Variables, config.SecretVersions, config.SecretRefs, config.SidecarSecretVersions, config.AllSecrets)
	if err != nil {
		return zero, Instance{}, err
	}
	fresh, err := readRuntimeConfigInputsFresh(ctx, tx, request.AppID, inputs)
	if err != nil {
		return zero, Instance{}, err
	}
	if !fresh {
		return zero, Instance{}, ErrConflict
	}
	return status, ins, nil
}

func (s *PgStore) ResolveEnvironmentQualificationService(ctx context.Context, request EnvironmentQualificationServiceRequest) (EnvironmentQualificationServiceRoute, error) {
	var zero EnvironmentQualificationServiceRoute
	if !qualificationServiceRequestValid(request) {
		return zero, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return zero, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	instanceID, held, err := qualificationNetworkInstance(ctx, tx, request.NodeID, request.HostIP)
	if err != nil {
		return zero, err
	}
	if instanceID == "" || !held {
		return zero, ErrConflict
	}
	q := sqlc.New()
	execution, err := q.EnvironmentQualificationExecution(ctx, tx, mustPgUUID(instanceID))
	if err != nil {
		return zero, mapErr(err)
	}
	row, err := s.qualificationCurrentTx(ctx, tx, pgUUIDString(execution.RequestID))
	if err != nil {
		return zero, err
	}
	caller := qualificationRequestFromSQL(row)
	if caller.GraphID != request.GraphID {
		return zero, ErrConflict
	}
	binding, exists := caller.FrozenInputs.ServiceBindings[request.Binding]
	if !exists {
		return zero, ErrConflict
	}
	requests, err := q.EnvironmentWorkloadQualificationsByGraph(ctx, tx, row.GraphID)
	if err != nil {
		return zero, mapErr(err)
	}
	var target EnvironmentWorkloadQualificationRequest
	for _, candidate := range requests {
		if candidate.Resource == "workload/"+binding.Workload && pgUUIDString(candidate.AppID) == binding.TargetAppID {
			locked, err := q.EnvironmentWorkloadQualificationForUpdate(ctx, tx, candidate.ID)
			if err != nil {
				return zero, mapErr(err)
			}
			target = qualificationRequestFromSQL(locked)
		}
	}
	if target.ID == "" || target.ReservedInstanceID == "" {
		return zero, ErrConflict
	}
	protocol, err := q.EnvironmentQualificationAppProtocol(ctx, tx, mustPgUUID(target.AppID))
	if err != nil {
		return zero, mapErr(err)
	}
	if protocol != "" && protocol != api.AppProtocolHTTP1 {
		return zero, ErrEnvironmentWorkloadPreparationUnavailable
	}
	callerStatus, callerIns, err := s.qualificationServiceEndpointTx(ctx, tx, caller, instanceID)
	if err != nil {
		return zero, err
	}
	if callerIns.NodeID != request.NodeID || callerIns.HostIP != request.HostIP {
		return zero, ErrConflict
	}
	targetInstanceID := target.ReservedInstanceID
	restore, restoreErr := q.EnvironmentQualificationRestoreReservation(ctx, tx, sqlc.EnvironmentQualificationRestoreReservationParams{RequestID: mustPgUUID(target.ID), Attempt: target.Attempt})
	if restoreErr == nil {
		targetInstanceID = pgUUIDString(restore.InstanceID)
	} else if !errors.Is(restoreErr, pgx.ErrNoRows) {
		return zero, mapErr(restoreErr)
	}
	targetStatus, _, err := s.qualificationServiceEndpointTx(ctx, tx, target, targetInstanceID)
	if err != nil {
		return zero, err
	}
	// Recheck IP uniqueness after taking the original source/execution locks.
	currentID, held, err := qualificationNetworkInstance(ctx, tx, request.NodeID, request.HostIP)
	if err != nil {
		return zero, err
	}
	if currentID != instanceID || !held {
		return zero, ErrConflict
	}
	route, err := qualificationServiceRoute(caller, target, callerStatus, targetStatus, request.Binding)
	if err != nil {
		return zero, err
	}
	if !time.Now().Before(route.Deadline) {
		return zero, ErrConflict
	}
	return route, mapErr(tx.Commit(ctx))
}
