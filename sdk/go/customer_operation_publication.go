package faas

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

func (r *CustomerOperationRuntime) publishCustomerOperationMilestones(ctx context.Context, db *sql.DB, input customerOperationInput) error {
	digest := customerOperationRequestDigest(input)
	rows, err := db.QueryContext(ctx, `SELECT m.id::text,m.name,m.payload,m.occurred_at
		FROM public.gregale_customer_operation_milestones m
		JOIN public.gregale_customer_operation_inbox r ON r.operation_id=m.operation_id
		WHERE r.operation_id=$1::uuid AND r.account_id=$2::uuid AND r.app_id=$3::uuid AND r.platform_tenant_id=$4::uuid
		AND r.request_digest=$5 AND m.acknowledged_at IS NULL ORDER BY m.occurred_at,m.id LIMIT $6`,
		input.operationID, input.accountID, input.appID, input.platformTenantID, digest[:], operationMilestones+1)
	if err != nil {
		return fmt.Errorf("faas: read pending milestone outbox rows: %w", err)
	}
	type pending struct {
		id, name, payload string
		occurredAt        time.Time
	}
	pendingRows := make([]pending, 0, operationMilestones)
	for rows.Next() {
		var row pending
		if err := rows.Scan(&row.id, &row.name, &row.payload, &row.occurredAt); err != nil {
			_ = rows.Close()
			return fmt.Errorf("faas: read pending milestone outbox row: %w", err)
		}
		pendingRows = append(pendingRows, row)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("faas: iterate pending milestone outbox rows: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("faas: close pending milestone outbox rows: %w", err)
	}
	if len(pendingRows) > operationMilestones {
		return errors.New("faas: saved milestone count exceeds its bound")
	}
	for _, row := range pendingRows {
		if !customerOperationUUIDPattern.MatchString(row.id) || !customerOperationMilestoneName.MatchString(row.name) ||
			len(row.payload) > operationMilestonePayloadBytes || !json.Valid([]byte(row.payload)) {
			return errors.New("faas: invalid saved customer Operation milestone")
		}
		request := OperationMilestoneRequest{ID: row.id, Name: row.name, Payload: json.RawMessage(row.payload), OccurredAt: row.occurredAt.UTC()}
		requestCtx, cancel := r.operationContext(ctx)
		client, err := r.operationClient(requestCtx)
		if err != nil {
			cancel()
			return err
		}
		receipt, err := client.ReportOperationMilestone(requestCtx, input.operationID, input.proof, request)
		cancel()
		if err != nil {
			return fmt.Errorf("faas: publish Customer Operation milestone: %w", err)
		}
		if receipt.ID != request.ID || receipt.OperationID != input.operationID || receipt.Name != request.Name {
			return errors.New("faas: Customer Operation milestone publication identity was not confirmed")
		}
		if _, err := db.ExecContext(ctx, `UPDATE public.gregale_customer_operation_milestones m SET acknowledged_at=clock_timestamp()
			FROM public.gregale_customer_operation_inbox r WHERE m.operation_id=r.operation_id AND r.operation_id=$1::uuid
			AND r.account_id=$2::uuid AND r.app_id=$3::uuid AND r.platform_tenant_id=$4::uuid AND r.request_digest=$5 AND m.id=$6::uuid`,
			input.operationID, input.accountID, input.appID, input.platformTenantID, digest[:], row.id); err != nil {
			return fmt.Errorf("faas: acknowledge Customer Operation milestone: %w", err)
		}
	}
	return nil
}

func (r *CustomerOperationRuntime) publishCustomerOperationWorkflowStates(ctx context.Context, db *sql.DB, input customerOperationInput) error {
	digest := customerOperationRequestDigest(input)
	rows, err := db.QueryContext(ctx, `SELECT s.id::text,s.workflow,s.instance_id,s.from_state,s.state,s.revision,s.evidence_milestones,s.occurred_at,s.blockers,s.blockers_only,s.blocker_resolutions,s.deadline_at,s.deadline_only,s.outcome_code,s.outcome_description,s.outcome_only,s.depends_on,s.dependencies_only
		FROM public.gregale_customer_operation_workflow_states s
		JOIN public.gregale_customer_operation_inbox r ON r.operation_id=s.operation_id
		WHERE r.operation_id=$1::uuid AND r.account_id=$2::uuid AND r.app_id=$3::uuid AND r.platform_tenant_id=$4::uuid
		AND r.request_digest=$5 AND s.acknowledged_at IS NULL ORDER BY s.revision,s.id LIMIT $6`,
		input.operationID, input.accountID, input.appID, input.platformTenantID, digest[:], operationWorkflowStateReports+1)
	if err != nil {
		return fmt.Errorf("faas: read pending workflow state outbox rows: %w", err)
	}
	type pending struct {
		id, workflow, instanceID, fromState, state string
		revision                                   int64
		blockers                                   []byte
		blockersOnly                               bool
		evidence                                   []byte
		occurredAt                                 time.Time
		resolutions                                []byte
		deadlineAt                                 string
		deadlineOnly                               bool
		outcomeCode, outcomeDescription            string
		outcomeOnly                                bool
		dependenciesJSON                           []byte
		dependenciesOnly                           bool
	}
	pendingRows := make([]pending, 0, operationWorkflowStateReports)
	for rows.Next() {
		var row pending
		if err := rows.Scan(&row.id, &row.workflow, &row.instanceID, &row.fromState, &row.state, &row.revision, &row.evidence, &row.occurredAt, &row.blockers, &row.blockersOnly, &row.resolutions, &row.deadlineAt, &row.deadlineOnly, &row.outcomeCode, &row.outcomeDescription, &row.outcomeOnly, &row.dependenciesJSON, &row.dependenciesOnly); err != nil {
			_ = rows.Close()
			return fmt.Errorf("faas: read pending workflow state outbox row: %w", err)
		}
		pendingRows = append(pendingRows, row)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("faas: iterate pending workflow state outbox rows: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("faas: close pending workflow state outbox rows: %w", err)
	}
	if len(pendingRows) > operationWorkflowStateReports {
		return errors.New("faas: saved workflow state count exceeds its bound")
	}
	for _, row := range pendingRows {
		if !customerOperationUUIDPattern.MatchString(row.id) || !customerOperationWorkflowName.MatchString(row.workflow) ||
			!customerOperationStateName.MatchString(row.state) || row.fromState != "" && !customerOperationStateName.MatchString(row.fromState) ||
			row.revision < 1 || row.revision > 9007199254740991 {
			return errors.New("faas: invalid saved Customer Operation workflow state")
		}
		var evidence []OperationWorkflowEvidenceMilestone
		if len(row.evidence) > operationWorkflowStateBatchBytes || json.Unmarshal(row.evidence, &evidence) != nil || len(evidence) > 16 {
			return errors.New("faas: invalid saved workflow state evidence")
		}
		for _, item := range evidence {
			if !customerOperationUUIDPattern.MatchString(item.ID) || !customerOperationMilestoneName.MatchString(item.Name) {
				return errors.New("faas: invalid saved workflow state evidence")
			}
		}
		var blockers []OperationWorkflowBlocker
		if len(row.blockers) > operationWorkflowStateBatchBytes || json.Unmarshal(row.blockers, &blockers) != nil {
			return errors.New("faas: invalid saved workflow blockers")
		}
		blockers, err := canonicalCustomerWorkflowBlockers(blockers)
		if err != nil {
			return err
		}
		var resolutions []OperationWorkflowBlockerResolution
		if len(row.resolutions) > operationWorkflowStateBatchBytes || json.Unmarshal(row.resolutions, &resolutions) != nil {
			return errors.New("faas: invalid saved blocker resolutions")
		}
		resolutions, err = canonicalCustomerWorkflowResolutions(resolutions, blockers)
		if err != nil {
			return err
		}
		deadline, err := canonicalCustomerWorkflowDeadline(row.deadlineAt)
		if err != nil {
			return err
		}
		if row.outcomeCode != "" || row.outcomeDescription != "" {
			if err := validateCustomerWorkflowOutcome(row.outcomeCode, row.outcomeDescription); err != nil {
				return err
			}
		}
		var dependencies []OperationWorkflowDependency
		if json.Unmarshal(row.dependenciesJSON, &dependencies) != nil {
			return errors.New("faas: invalid saved dependencies")
		}
		dependencies, err = canonicalCustomerWorkflowDependencies(dependencies)
		if err != nil {
			return err
		}
		report := OperationWorkflowStateReport{DependsOn: dependencies, DependenciesOnly: row.dependenciesOnly, OutcomeCode: row.outcomeCode, OutcomeDescription: row.outcomeDescription, OutcomeOnly: row.outcomeOnly, DeadlineAt: deadline, DeadlineOnly: row.deadlineOnly, BlockerResolutions: resolutions, Blockers: blockers, BlockersOnly: row.blockersOnly, ID: row.id, Workflow: row.workflow, InstanceID: row.instanceID, State: row.state,
			Revision: row.revision, OccurredAt: row.occurredAt.UTC(), EvidenceMilestones: evidence}
		if row.fromState != "" {
			report.FromState = row.fromState
		}
		requestCtx, cancel := r.operationContext(ctx)
		client, err := r.operationClient(requestCtx)
		if err != nil {
			cancel()
			return err
		}
		receipt, err := client.ReportOperationWorkflowState(requestCtx, input.operationID, input.proof, report)
		cancel()
		if err != nil {
			return fmt.Errorf("faas: publish Customer Operation workflow state: %w", err)
		}
		if receipt.ID != report.ID || receipt.OperationID != input.operationID || receipt.Workflow != report.Workflow ||
			receipt.InstanceID != report.InstanceID || receipt.FromState != report.FromState || receipt.State != report.State ||
			!sameCustomerWorkflowDependencies(receipt.DependsOn, report.DependsOn) || receipt.DependenciesOnly != report.DependenciesOnly || receipt.OutcomeCode != report.OutcomeCode || receipt.OutcomeDescription != report.OutcomeDescription || receipt.OutcomeOnly != report.OutcomeOnly || receipt.DeadlineAt != report.DeadlineAt || receipt.DeadlineOnly != report.DeadlineOnly || receipt.Revision != report.Revision || receipt.ContractVersion < 1 || !sameCustomerOperationEvidence(receipt.EvidenceMilestones, report.EvidenceMilestones) || receipt.BlockersOnly != report.BlockersOnly || !sameCustomerWorkflowBlockers(receipt.Blockers, report.Blockers) || !sameCustomerWorkflowResolutions(receipt.BlockerResolutions, report.BlockerResolutions) {
			return errors.New("faas: Customer Operation workflow state publication identity was not confirmed")
		}
		if _, err := db.ExecContext(ctx, `UPDATE public.gregale_customer_operation_workflow_states s SET acknowledged_at=clock_timestamp()
			FROM public.gregale_customer_operation_inbox r WHERE s.operation_id=r.operation_id AND r.operation_id=$1::uuid
			AND r.account_id=$2::uuid AND r.app_id=$3::uuid AND r.platform_tenant_id=$4::uuid AND r.request_digest=$5 AND s.id=$6::uuid`,
			input.operationID, input.accountID, input.appID, input.platformTenantID, digest[:], row.id); err != nil {
			return fmt.Errorf("faas: acknowledge Customer Operation workflow state: %w", err)
		}
	}
	return nil
}

func sameCustomerOperationEvidence(left, right []OperationWorkflowEvidenceMilestone) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func sameCustomerWorkflowBlockers(left, right []OperationWorkflowBlocker) bool {
	a, err := canonicalCustomerWorkflowBlockers(left)
	if err != nil {
		return false
	}
	b, err := canonicalCustomerWorkflowBlockers(right)
	if err != nil || len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sameCustomerWorkflowResolutions(left, right []OperationWorkflowBlockerResolution) bool {
	a, err := canonicalCustomerWorkflowResolutions(left, nil)
	if err != nil {
		return false
	}
	b, err := canonicalCustomerWorkflowResolutions(right, nil)
	if err != nil || len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sameCustomerWorkflowDependencies(left, right []OperationWorkflowDependency) bool {
	a, err := canonicalCustomerWorkflowDependencies(left)
	if err != nil {
		return false
	}
	b, err := canonicalCustomerWorkflowDependencies(right)
	if err != nil || len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
