// adr: 20261022155812
package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ EnvironmentQualificationPullSmokeStore = (*PgStore)(nil)

const pullSmokeColumns = `binding, queue_name, message_id::text, payload, deadline, receipt_sha256, lease_expires_at, deliveries, outcome, settled_at, armed_at`

type pgQualificationPullSmoke struct {
	smoke          EnvironmentQualificationPullSmoke
	receiptSHA256  string
	leaseExpiresAt *time.Time
}

func scanQualificationPullSmoke(row pgx.Row, out *pgQualificationPullSmoke) error {
	var payload []byte
	if err := row.Scan(&out.smoke.Binding, &out.smoke.QueueName, &out.smoke.MessageID, &payload, &out.smoke.Deadline, &out.receiptSHA256,
		&out.leaseExpiresAt, &out.smoke.Deliveries, &out.smoke.Outcome, &out.smoke.SettledAt, &out.smoke.ArmedAt); err != nil {
		return err
	}
	out.smoke.Payload = json.RawMessage(payload)
	return nil
}

func (s *PgStore) ArmEnvironmentQualificationPullSmoke(ctx context.Context, smoke EnvironmentQualificationPullSmoke) error {
	if !qualificationPullSmokeArmValid(smoke) {
		return ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	row, err := s.qualificationCurrentTx(ctx, tx, smoke.RequestID)
	if err != nil {
		return err
	}
	request := qualificationRequestFromSQL(row)
	if request.Attempt != smoke.Attempt || request.LeaseUntil == nil {
		return ErrConflict
	}
	if _, _, err := s.qualificationServiceEndpointTx(ctx, tx, request, smoke.InstanceID); err != nil {
		return err
	}
	queue, payload, err := qualificationPullSmokeFrozen(request, smoke.Binding)
	if err != nil {
		return err
	}
	deadline := smoke.Deadline
	if request.LeaseUntil.Before(deadline) {
		deadline = *request.LeaseUntil
	}
	const insert = `
INSERT INTO environment_qualification_pull_smokes(request_id,attempt,instance_id,binding,queue_name,message_id,payload,deadline)
SELECT $1::uuid,$2,$3::uuid,$4,$5,$6::uuid,$7::jsonb,$8
WHERE $8>clock_timestamp()
ON CONFLICT (request_id,attempt,instance_id,binding) DO NOTHING`
	if _, err := tx.Exec(ctx, insert, smoke.RequestID, smoke.Attempt, smoke.InstanceID, smoke.Binding, queue, smoke.MessageID, []byte(payload), deadline); err != nil {
		return mapErr(err)
	}
	var messageID, outcome string
	const armed = `SELECT message_id::text, outcome FROM environment_qualification_pull_smokes WHERE request_id=$1::uuid AND attempt=$2 AND instance_id=$3::uuid AND binding=$4`
	if err := tx.QueryRow(ctx, armed, smoke.RequestID, smoke.Attempt, smoke.InstanceID, smoke.Binding).Scan(&messageID, &outcome); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrConflict // the deadline had already passed
		}
		return mapErr(err)
	}
	if messageID != smoke.MessageID || outcome != "" {
		return ErrConflict
	}
	return mapErr(tx.Commit(ctx))
}

func (s *PgStore) EnvironmentQualificationPullSmoke(ctx context.Context, requestID string, attempt int64, instanceID, binding string) (EnvironmentQualificationPullSmoke, error) {
	if !qualificationRecoveryUUIDValid(requestID) || !qualificationRecoveryUUIDValid(instanceID) {
		return EnvironmentQualificationPullSmoke{}, ErrInvalidArgument
	}
	var row pgQualificationPullSmoke
	query := `SELECT ` + pullSmokeColumns + ` FROM environment_qualification_pull_smokes WHERE request_id=$1::uuid AND attempt=$2 AND instance_id=$3::uuid AND binding=$4`
	if err := scanQualificationPullSmoke(s.pool.QueryRow(ctx, query, requestID, attempt, instanceID, binding), &row); err != nil {
		return EnvironmentQualificationPullSmoke{}, mapErr(err)
	}
	row.smoke.RequestID, row.smoke.Attempt, row.smoke.InstanceID = requestID, attempt, instanceID
	return row.smoke, nil
}

// qualificationPullCallerTx resolves the one current held candidate execution
// behind the observed node address, as the service route does, and rechecks
// the address after taking the execution locks.
func (s *PgStore) qualificationPullCallerTx(ctx context.Context, tx pgx.Tx, caller EnvironmentQualificationPullCaller) (EnvironmentWorkloadQualificationRequest, string, error) {
	var zero EnvironmentWorkloadQualificationRequest
	instanceID, held, err := qualificationNetworkInstance(ctx, tx, caller.NodeID, caller.HostIP)
	if err != nil {
		return zero, "", err
	}
	if instanceID == "" || !held {
		return zero, "", ErrConflict
	}
	execution, err := sqlc.New().EnvironmentQualificationExecution(ctx, tx, mustPgUUID(instanceID))
	if err != nil {
		return zero, "", mapErr(err)
	}
	row, err := s.qualificationCurrentTx(ctx, tx, pgUUIDString(execution.RequestID))
	if err != nil {
		return zero, "", err
	}
	request := qualificationRequestFromSQL(row)
	_, ins, err := s.qualificationServiceEndpointTx(ctx, tx, request, instanceID)
	if err != nil {
		return zero, "", err
	}
	if ins.NodeID != caller.NodeID || ins.HostIP != caller.HostIP {
		return zero, "", ErrConflict
	}
	current, held, err := qualificationNetworkInstance(ctx, tx, caller.NodeID, caller.HostIP)
	if err != nil {
		return zero, "", err
	}
	if current != instanceID || !held {
		return zero, "", ErrConflict
	}
	var slug string
	if err := tx.QueryRow(ctx, `SELECT slug FROM apps WHERE id=$1::uuid`, request.AppID).Scan(&slug); err != nil {
		return zero, "", mapErr(err)
	}
	if slug != caller.App || !qualificationPullQueueFrozen(request, caller.Queue) {
		return zero, "", ErrNotFound
	}
	return request, instanceID, nil
}

func (s *PgStore) qualificationPullRowsTx(ctx context.Context, tx pgx.Tx, request EnvironmentWorkloadQualificationRequest, instanceID, queue string) ([]pgQualificationPullSmoke, time.Time, error) {
	var now time.Time
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return nil, now, mapErr(err)
	}
	query := `SELECT ` + pullSmokeColumns + ` FROM environment_qualification_pull_smokes
WHERE request_id=$1::uuid AND attempt=$2 AND instance_id=$3::uuid AND queue_name=$4 ORDER BY binding FOR UPDATE`
	rows, err := tx.Query(ctx, query, request.ID, request.Attempt, instanceID, queue)
	if err != nil {
		return nil, now, mapErr(err)
	}
	defer rows.Close()
	var out []pgQualificationPullSmoke
	for rows.Next() {
		var row pgQualificationPullSmoke
		if err := scanQualificationPullSmoke(rows, &row); err != nil {
			return nil, now, mapErr(err)
		}
		row.smoke.RequestID, row.smoke.Attempt, row.smoke.InstanceID = request.ID, request.Attempt, instanceID
		out = append(out, row)
	}
	return out, now, mapErr(rows.Err())
}

func (s *PgStore) LeaseEnvironmentQualificationPullSmoke(ctx context.Context, caller EnvironmentQualificationPullCaller, visibility time.Duration) ([]EnvironmentQualificationPullLease, error) {
	if !qualificationPullCallerValid(caller) {
		return nil, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	request, instanceID, err := s.qualificationPullCallerTx(ctx, tx, caller)
	if err != nil {
		return nil, err
	}
	rows, now, err := s.qualificationPullRowsTx(ctx, tx, request, instanceID, caller.Queue)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row.smoke.Outcome != "" || !now.Before(row.smoke.Deadline) || row.leaseExpiresAt != nil && now.Before(*row.leaseExpiresAt) {
			continue
		}
		until, ok := qualificationPullVisibility(visibility, now, row.smoke.Deadline)
		if !ok {
			return nil, ErrInvalidArgument
		}
		receipt, sha, err := newQualificationPullReceipt()
		if err != nil {
			return nil, err
		}
		const lease = `UPDATE environment_qualification_pull_smokes SET receipt_sha256=$5, lease_expires_at=$6, deliveries=deliveries+1
WHERE request_id=$1::uuid AND attempt=$2 AND instance_id=$3::uuid AND binding=$4`
		if _, err := tx.Exec(ctx, lease, request.ID, request.Attempt, instanceID, row.smoke.Binding, sha, until); err != nil {
			return nil, mapErr(err)
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, mapErr(err)
		}
		return []EnvironmentQualificationPullLease{{MessageID: row.smoke.MessageID, Receipt: receipt, Payload: row.smoke.Payload,
			Attempt: row.smoke.Deliveries + 1, ArmedAt: row.smoke.ArmedAt, LeaseExpiresAt: until}}, nil
	}
	return nil, mapErr(tx.Commit(ctx))
}

func (s *PgStore) SettleEnvironmentQualificationPullSmoke(ctx context.Context, caller EnvironmentQualificationPullCaller, verb string, receipts []string, visibility time.Duration) (QueueLeaseOutcome, error) {
	var out QueueLeaseOutcome
	if !qualificationPullCallerValid(caller) || !qualificationPullSettleValid(verb, receipts) {
		return out, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	request, instanceID, err := s.qualificationPullCallerTx(ctx, tx, caller)
	if err != nil {
		return out, err
	}
	rows, now, err := s.qualificationPullRowsTx(ctx, tx, request, instanceID, caller.Queue)
	if err != nil {
		return out, err
	}
	for _, receipt := range receipts {
		var row *pgQualificationPullSmoke
		if qualificationPullReceiptValid(receipt) {
			sha := qualificationPullReceiptSHA256(receipt)
			for i := range rows {
				if rows[i].receiptSHA256 == sha {
					row = &rows[i]
				}
			}
		}
		if row == nil {
			out.Failed = append(out.Failed, QueueLeaseFailure{Receipt: receipt, Reason: QueueLeaseReasonInvalid})
			continue
		}
		if row.smoke.Outcome != "" || row.leaseExpiresAt == nil || !now.Before(*row.leaseExpiresAt) || !now.Before(row.smoke.Deadline) {
			out.Failed = append(out.Failed, QueueLeaseFailure{Receipt: receipt, Reason: QueueLeaseReasonExpired})
			continue
		}
		if verb == PullSmokeExtend {
			until, ok := qualificationPullVisibility(visibility, now, row.smoke.Deadline)
			if !ok {
				return QueueLeaseOutcome{}, ErrInvalidArgument
			}
			const extend = `UPDATE environment_qualification_pull_smokes SET lease_expires_at=$5 WHERE request_id=$1::uuid AND attempt=$2 AND instance_id=$3::uuid AND binding=$4`
			if _, err := tx.Exec(ctx, extend, request.ID, request.Attempt, instanceID, row.smoke.Binding, until); err != nil {
				return QueueLeaseOutcome{}, mapErr(err)
			}
			row.leaseExpiresAt = &until
		} else {
			outcome := PullSmokeAcked
			if verb == PullSmokeNack {
				outcome = PullSmokeNacked
			}
			const settle = `UPDATE environment_qualification_pull_smokes SET outcome=$5, settled_at=$6 WHERE request_id=$1::uuid AND attempt=$2 AND instance_id=$3::uuid AND binding=$4`
			if _, err := tx.Exec(ctx, settle, request.ID, request.Attempt, instanceID, row.smoke.Binding, outcome, now); err != nil {
				return QueueLeaseOutcome{}, mapErr(err)
			}
			row.smoke.Outcome = outcome
		}
		out.Succeeded = append(out.Succeeded, receipt)
	}
	return out, mapErr(tx.Commit(ctx))
}
