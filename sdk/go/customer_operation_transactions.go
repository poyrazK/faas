package faas

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// CustomerOperationReceiptSchema is application-owned PostgreSQL DDL. Install
// it as the database owner before enabling transactional milestones or states.
//
//go:embed customer_operation_schema.sql
var CustomerOperationReceiptSchema string

var (
	ErrInvalidCustomerOperationRequest  = errors.New("invalid customer Operation transaction request")
	ErrCustomerOperationReceiptConflict = errors.New("customer Operation receipt scope or input differs")
	ErrCustomerOperationNotNegotiated   = errors.New("customer Operation workflow and milestone support is not negotiated")
	customerOperationWorkflowName       = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	customerOperationStateName          = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
	customerOperationMilestoneName      = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
)

// CustomerOperationTransaction exposes the caller-owned SQL transaction and
// queues public milestones and app-reported workflow updates for that write.
type CustomerOperationTransaction struct {
	OperationSQLTransaction
	input          customerOperationInput
	milestones     []OperationMilestoneRequest
	workflowStates []OperationWorkflowStateReport
	guardErr       error
	open           bool
}

// Milestone queues a declared public fact. It is saved before commit and
// published after commit; payload must contain JSON values within the limit.
func (tx *CustomerOperationTransaction) Milestone(name string, payload any) error {
	if tx == nil || !tx.open {
		return errors.New("faas: milestones must be recorded inside the transaction callback")
	}
	if !tx.input.milestonesEnabled {
		return ErrCustomerOperationNotNegotiated
	}
	if !customerOperationMilestoneName.MatchString(name) {
		return errors.New("faas: milestone name must be a bounded lowercase slug")
	}
	if len(tx.milestones) >= operationMilestones {
		return errors.New("faas: too many customer Operation milestones")
	}
	payloadJSON, err := marshalCustomerOperationJSON(payload)
	if err != nil {
		return fmt.Errorf("faas: milestone payload: %w", err)
	}
	if len(payloadJSON) > operationMilestonePayloadBytes {
		return errors.New("faas: milestone payload exceeds its byte limit")
	}
	identity, err := newCustomerOperationUUID()
	if err != nil {
		return fmt.Errorf("faas: generate milestone identity: %w", err)
	}
	report := OperationMilestoneRequest{ID: identity, Name: name, Payload: json.RawMessage(payloadJSON), OccurredAt: time.Now().UTC().Truncate(time.Millisecond)}
	batch, err := marshalCustomerOperationJSON(struct {
		Milestones []OperationMilestoneRequest `json:"milestones"`
	}{Milestones: append(append([]OperationMilestoneRequest(nil), tx.milestones...), report)})
	if err != nil || len(batch) > operationMilestoneBatchBytes {
		return errors.New("faas: customer Operation milestone batch exceeds its byte limit")
	}
	tx.milestones = append(tx.milestones, report)
	return nil
}

// WorkflowState queues an explicit app-owned snapshot in the same transaction
// as the business write.
func (tx *CustomerOperationTransaction) WorkflowState(workflow, instanceID, state string) error {
	return tx.queueWorkflowState(workflow, instanceID, state, "")
}

// WorkflowTransition queues a declared edge. Check fromState against the
// locked business row before calling; the helper also checks its saved report
// head to serialize competing reports across Operations.
func (tx *CustomerOperationTransaction) WorkflowTransition(workflow, instanceID, fromState, toState string) error {
	if !customerOperationStateName.MatchString(fromState) {
		return errors.New("faas: workflow transition source must be a bounded lowercase slug")
	}
	return tx.queueWorkflowState(workflow, instanceID, toState, fromState)
}

func (tx *CustomerOperationTransaction) queueWorkflowState(workflow, instanceID, state, fromState string) error {
	if tx == nil || !tx.open {
		return errors.New("faas: workflow state must be reported inside the transaction callback")
	}
	if !tx.input.milestonesEnabled {
		return ErrCustomerOperationNotNegotiated
	}
	if !customerOperationWorkflowName.MatchString(workflow) || !customerOperationStateName.MatchString(state) ||
		fromState != "" && !customerOperationStateName.MatchString(fromState) || instanceID == "" ||
		!utf8.ValidString(instanceID) || len(instanceID) > operationSubjectIdBytes || strings.ContainsFunc(instanceID, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return errors.New("faas: workflow state requires valid workflow, instance ID, and state names")
	}
	if len(tx.workflowStates) >= operationWorkflowStateReports {
		return errors.New("faas: too many customer Operation workflow state reports")
	}
	identity, err := newCustomerOperationUUID()
	if err != nil {
		return fmt.Errorf("faas: generate workflow state identity: %w", err)
	}
	report := OperationWorkflowStateReport{ID: identity, Workflow: workflow, InstanceID: instanceID, State: state,
		OccurredAt: time.Now().UTC().Truncate(time.Millisecond)}
	if fromState != "" {
		report.FromState = fromState
	}
	candidate := append(append([]OperationWorkflowStateReport(nil), tx.workflowStates...), report)
	batch, err := marshalCustomerOperationJSON(struct {
		WorkflowStates []OperationWorkflowStateReport `json:"workflow_states"`
	}{WorkflowStates: candidate})
	if err != nil || len(batch) > operationWorkflowStateBatchBytes {
		return errors.New("faas: customer Operation workflow state batch exceeds its byte limit")
	}
	tx.workflowStates = append(tx.workflowStates, report)
	return nil
}

// CustomerOperationPublicationError indicates that the business transaction
// committed but one or more public facts still need publication. Retry the same
// Operation request; the callback will be skipped and pending outbox rows replayed.
type CustomerOperationPublicationError struct {
	OperationID string
	Kind        string
	Committed   bool
	Cause       error
}

func (e *CustomerOperationPublicationError) Error() string {
	return fmt.Sprintf("business transaction committed; customer Operation %s publication incomplete; retry the same Operation identity: %v", e.Kind, e.Cause)
}

func (e *CustomerOperationPublicationError) Unwrap() error { return e.Cause }

// Transaction runs a trusted Customer Operation HTTP request inside one
// READ COMMITTED transaction. The handler must use tx for business writes and
// must not perform external side effects. Receipt replay skips the handler,
// then retries any unacknowledged milestone or workflow-state publications.
func (r *CustomerOperationRuntime) Transaction(
	ctx context.Context,
	db *sql.DB,
	request *http.Request,
	originalBody []byte,
	handler func(*CustomerOperationTransaction) (any, error),
) (OperationTransactionResult, error) {
	if r == nil || db == nil || request == nil || handler == nil || ctx == nil {
		return OperationTransactionResult{}, ErrInvalidCustomerOperationRequest
	}
	input, err := customerOperationInputFromHTTP(request, originalBody)
	if err != nil {
		return OperationTransactionResult{}, err
	}
	digest := customerOperationRequestDigest(input)
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return OperationTransactionResult{}, fmt.Errorf("faas: Customer Operation transaction begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // rollback after commit or errors is best effort
	if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1 || $2::uuid::text, 0))", "gregale.customer-operation-inbox.v1:", input.operationID); err != nil {
		return OperationTransactionResult{}, fmt.Errorf("faas: Customer Operation transaction lock: %w", err)
	}
	var accountID, appID, platformTenantID, responseBody string
	var storedDigest []byte
	err = tx.QueryRowContext(ctx, `SELECT account_id::text,app_id::text,coalesce(platform_tenant_id::text,'') AS platform_tenant_id,request_digest,response_body
		FROM public.gregale_customer_operation_inbox WHERE operation_id=$1::uuid`, input.operationID).Scan(&accountID, &appID, &platformTenantID, &storedDigest, &responseBody)
	replayed := err == nil
	if replayed {
		if accountID != input.accountID || appID != input.appID || platformTenantID != input.platformTenantID || !bytes.Equal(storedDigest, digest[:]) {
			return OperationTransactionResult{}, ErrCustomerOperationReceiptConflict
		}
		if err := validateCustomerOperationResult([]byte(responseBody), input.resultMaxBytes); err != nil {
			return OperationTransactionResult{}, err
		}
	} else if errors.Is(err, sql.ErrNoRows) {
		if input.milestonesEnabled {
			rows, queryErr := tx.QueryContext(ctx, "SELECT 1 FROM public.gregale_customer_operation_milestones LIMIT 0")
			if queryErr != nil {
				return OperationTransactionResult{}, fmt.Errorf("faas: Customer Operation milestone schema is unavailable: %w", queryErr)
			}
			if closeErr := rows.Close(); closeErr != nil {
				return OperationTransactionResult{}, fmt.Errorf("faas: close Customer Operation schema check: %w", closeErr)
			}
		}
		transaction := &CustomerOperationTransaction{OperationSQLTransaction: tx, input: input, open: true}
		var result any
		var handlerErr error
		func() {
			defer func() { transaction.open = false }()
			result, handlerErr = handler(transaction)
		}()
		if transaction.guardErr != nil {
			return OperationTransactionResult{}, transaction.guardErr
		}
		if handlerErr != nil {
			return OperationTransactionResult{}, handlerErr
		}
		encoded, err := marshalCustomerOperationJSON(result)
		if err != nil {
			return OperationTransactionResult{}, fmt.Errorf("faas: encode Customer Operation result: %w", err)
		}
		if err := validateCustomerOperationResult(encoded, input.resultMaxBytes); err != nil {
			return OperationTransactionResult{}, err
		}
		responseBody = string(encoded)

		if len(transaction.milestones) > 0 {
			if err := r.validateCustomerOperationMilestones(ctx, input, transaction.milestones); err != nil {
				return OperationTransactionResult{}, err
			}
			if err := saveCustomerOperationMilestones(ctx, tx, input.operationID, transaction.milestones); err != nil {
				return OperationTransactionResult{}, fmt.Errorf("faas: save Customer Operation milestones: %w", err)
			}
		}
		if len(transaction.workflowStates) > 0 {
			saved, err := saveCustomerOperationWorkflowStates(ctx, tx, input, transaction.workflowStates, transaction.milestones)
			if err != nil {
				return OperationTransactionResult{}, err
			}
			batch, err := marshalCustomerOperationJSON(OperationWorkflowStateValidationRequest{WorkflowStates: saved, Milestones: transaction.milestones})
			if err != nil || len(batch) > operationWorkflowStateBatchBytes {
				return OperationTransactionResult{}, errors.New("faas: Customer Operation workflow validation batch exceeds its byte limit")
			}
			if err := r.validateCustomerOperationWorkflowStates(ctx, input, saved, transaction.milestones); err != nil {
				return OperationTransactionResult{}, err
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO public.gregale_customer_operation_inbox
			(operation_id,account_id,app_id,platform_tenant_id,request_digest,response_body)
			VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6)`, input.operationID, input.accountID, input.appID, input.platformTenantID, digest[:], responseBody); err != nil {
			return OperationTransactionResult{}, fmt.Errorf("faas: insert Customer Operation receipt: %w", err)
		}
	} else if err != nil {
		return OperationTransactionResult{}, fmt.Errorf("faas: read Customer Operation receipt: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return OperationTransactionResult{}, fmt.Errorf("%w: %w", ErrOperationCommitUnknown, err)
	}
	result := OperationTransactionResult{Body: json.RawMessage(responseBody), Replayed: replayed}
	if input.milestonesEnabled {
		if err := r.publishCustomerOperationMilestones(ctx, db, input); err != nil {
			return result, &CustomerOperationPublicationError{OperationID: input.operationID, Kind: "milestone", Committed: true, Cause: err}
		}
		if err := r.publishCustomerOperationWorkflowStates(ctx, db, input); err != nil {
			return result, &CustomerOperationPublicationError{OperationID: input.operationID, Kind: "workflow-state", Committed: true, Cause: err}
		}
	}
	return result, nil
}

func customerOperationRequestDigest(input customerOperationInput) [32]byte {
	frame := make([]byte, 0, len(input.method)+len(input.path)+len(input.body)+64)
	frame = append(frame, "gregale-customer-operation-request-v1\n"...)
	frame = append(frame, input.method...)
	frame = append(frame, '\n')
	frame = append(frame, input.path...)
	frame = append(frame, '\n')
	frame = append(frame, input.body...)
	return sha256.Sum256(frame)
}

func marshalCustomerOperationJSON(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte("\n")), nil
}

func validateCustomerOperationResult(body []byte, maximum int64) error {
	if len(body) == 0 || int64(len(body)) > maximum || !utf8.Valid(body) || !json.Valid(body) {
		return errors.New("faas: Customer Operation result is invalid or exceeds its byte limit")
	}
	return nil
}

func newCustomerOperationUUID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(value[:])
	return encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:], nil
}

func (r *CustomerOperationRuntime) validateCustomerOperationMilestones(ctx context.Context, input customerOperationInput, reports []OperationMilestoneRequest) error {
	requestCtx, cancel := r.operationContext(ctx)
	defer cancel()
	client, err := r.operationClient(requestCtx)
	if err != nil {
		return err
	}
	response, err := client.ValidateOperationMilestones(requestCtx, input.operationID, input.proof, OperationMilestoneValidationRequest{Milestones: reports})
	if err != nil {
		return fmt.Errorf("faas: validate Customer Operation milestones: %w", err)
	}
	if !response.Valid {
		return errors.New("faas: Customer Operation milestone validation was not confirmed")
	}
	return nil
}

func (r *CustomerOperationRuntime) validateCustomerOperationWorkflowStates(ctx context.Context, input customerOperationInput, reports []OperationWorkflowStateReport, milestones []OperationMilestoneRequest) error {
	requestCtx, cancel := r.operationContext(ctx)
	defer cancel()
	client, err := r.operationClient(requestCtx)
	if err != nil {
		return err
	}
	response, err := client.ValidateOperationWorkflowStates(requestCtx, input.operationID, input.proof,
		OperationWorkflowStateValidationRequest{WorkflowStates: reports, Milestones: milestones})
	if err != nil {
		return fmt.Errorf("faas: validate Customer Operation workflow states: %w", err)
	}
	if !response.Valid {
		return errors.New("faas: Customer Operation workflow state validation was not confirmed")
	}
	return nil
}

func saveCustomerOperationMilestones(ctx context.Context, tx OperationSQLTransaction, operationID string, reports []OperationMilestoneRequest) error {
	for _, report := range reports {
		if _, err := tx.ExecContext(ctx, `INSERT INTO public.gregale_customer_operation_milestones
			(operation_id,id,name,payload,occurred_at) VALUES ($1::uuid,$2::uuid,$3,$4,$5::timestamptz)`,
			operationID, report.ID, report.Name, string(report.Payload), report.OccurredAt); err != nil {
			return err
		}
	}
	return nil
}

func saveCustomerOperationWorkflowStates(ctx context.Context, tx OperationSQLTransaction, input customerOperationInput, reports []OperationWorkflowStateReport, milestones []OperationMilestoneRequest) ([]OperationWorkflowStateReport, error) {
	evidenceByName := make(map[string]OperationWorkflowEvidenceMilestone, len(milestones))
	for _, milestone := range milestones {
		if _, exists := evidenceByName[milestone.Name]; !exists {
			evidenceByName[milestone.Name] = OperationWorkflowEvidenceMilestone{ID: milestone.ID, Name: milestone.Name}
		}
	}
	evidence := make([]OperationWorkflowEvidenceMilestone, 0, len(evidenceByName))
	for _, milestone := range evidenceByName {
		evidence = append(evidence, milestone)
	}
	sort.Slice(evidence, func(i, j int) bool {
		if evidence[i].Name != evidence[j].Name {
			return evidence[i].Name < evidence[j].Name
		}
		return evidence[i].ID < evidence[j].ID
	})
	if len(evidence) > 16 {
		return nil, errors.New("faas: workflow transition evidence exceeds its limit")
	}

	saved := make([]OperationWorkflowStateReport, 0, len(reports))
	for _, report := range reports {
		if !report.BlockersOnly && !report.DeadlineOnly && !report.OutcomeOnly && !report.DependenciesOnly && report.FromState != "" && len(evidence) > 0 {
			report.EvidenceMilestones = append([]OperationWorkflowEvidenceMilestone(nil), evidence...)
		}
		var revision int64
		err := tx.QueryRowContext(ctx, `INSERT INTO public.gregale_customer_operation_workflow_state_counters
			(platform_tenant_id,workflow,instance_id,revision) VALUES ($1::uuid,$2,$3,1)
			ON CONFLICT (platform_tenant_id,workflow,instance_id) DO UPDATE
			SET revision=public.gregale_customer_operation_workflow_state_counters.revision+1
			WHERE public.gregale_customer_operation_workflow_state_counters.revision<9007199254740991
			RETURNING revision`, input.platformTenantID, report.Workflow, report.InstanceID).Scan(&revision)
		if err != nil || revision < 1 || revision > 9007199254740991 {
			if err == nil {
				err = errors.New("revision is outside the supported range")
			}
			return nil, fmt.Errorf("faas: allocate workflow state revision: %w", err)
		}
		report.Revision = revision
		var lastState sql.NullString
		var previousJSON []byte
		var previousRevision int64
		var lastDeadline string
		var lastDeadlineRevision int64
		var lastOutcomeCode, lastOutcomeDescription string
		var lastOutcomeRevision int64
		var lastDependenciesJSON []byte
		var lastDependenciesRevision int64
		if err := tx.QueryRowContext(ctx, `SELECT last_state,last_blockers,last_blockers_revision,last_deadline_at,last_deadline_revision,last_outcome_code,last_outcome_description,last_outcome_revision,last_dependencies,last_dependencies_revision FROM public.gregale_customer_operation_workflow_state_counters
			WHERE platform_tenant_id=$1::uuid AND workflow=$2 AND instance_id=$3 FOR UPDATE`,
			input.platformTenantID, report.Workflow, report.InstanceID).Scan(&lastState, &previousJSON, &previousRevision, &lastDeadline, &lastDeadlineRevision, &lastOutcomeCode, &lastOutcomeDescription, &lastOutcomeRevision, &lastDependenciesJSON, &lastDependenciesRevision); err != nil {
			return nil, fmt.Errorf("faas: read workflow state counter: %w", err)
		}
		if lastState.Valid && !customerOperationStateName.MatchString(lastState.String) {
			return nil, errors.New("faas: saved workflow state is invalid")
		}
		if report.FromState != "" && lastState.Valid && lastState.String != report.FromState {
			return nil, errors.New("faas: workflow transition source does not match the latest app-reported state")
		}
		evidenceMilestones := report.EvidenceMilestones
		if evidenceMilestones == nil {
			evidenceMilestones = []OperationWorkflowEvidenceMilestone{}
			report.EvidenceMilestones = evidenceMilestones
		}
		evidenceJSON, err := marshalCustomerOperationJSON(evidenceMilestones)
		if err != nil {
			return nil, fmt.Errorf("faas: encode workflow transition evidence: %w", err)
		}
		var previous []OperationWorkflowBlocker
		if err := json.Unmarshal(previousJSON, &previous); err != nil {
			return nil, fmt.Errorf("faas: decode blocker counter: %w", err)
		}
		if report.DeadlineOnly || report.OutcomeOnly || report.DependenciesOnly {
			if previousRevision != revision-1 {
				return nil, errors.New("faas: metadata update requires current blocker counter metadata; upgrade all writers")
			}
			report.Blockers = previous
		}
		if !report.DeadlineOnly && report.DeadlineAt == "" && lastDeadlineRevision == revision-1 {
			report.DeadlineAt = lastDeadline
		}
		if !report.OutcomeOnly && report.OutcomeCode == "" && lastState.Valid && lastState.String == report.State && lastOutcomeRevision == revision-1 {
			report.OutcomeCode = lastOutcomeCode
			report.OutcomeDescription = lastOutcomeDescription
		}
		if report.OutcomeCode != "" || report.OutcomeDescription != "" {
			if err := validateCustomerWorkflowOutcome(report.OutcomeCode, report.OutcomeDescription); err != nil {
				return nil, err
			}
		}
		if !report.DependenciesOnly && report.DependsOn == nil && lastDependenciesRevision == revision-1 {
			if err := json.Unmarshal(lastDependenciesJSON, &report.DependsOn); err != nil {
				return nil, fmt.Errorf("faas: decode workflow dependencies: %w", err)
			}
		}
		dependencies, err := canonicalCustomerWorkflowDependencies(report.DependsOn)
		if err != nil {
			return nil, err
		}
		report.DependsOn = dependencies
		dependenciesJSON, err := marshalCustomerOperationJSON(dependencies)
		if err != nil {
			return nil, err
		}
		canonicalDeadline, err := canonicalCustomerWorkflowDeadline(report.DeadlineAt)
		if err != nil {
			return nil, err
		}
		report.DeadlineAt = canonicalDeadline
		report.Blockers = append([]OperationWorkflowBlocker(nil), report.Blockers...)
		for i := range report.Blockers {
			b := &report.Blockers[i]
			found := false
			if previousRevision == revision-1 {
				for _, prior := range previous {
					if b.Code == prior.Code && b.Operation == prior.Operation {
						found = true
						if prior.FirstObservedAt != "" {
							b.FirstObservedAt = prior.FirstObservedAt
						}
						break
					}
				}
				if !found && b.FirstObservedAt == "" {
					b.FirstObservedAt = report.OccurredAt.UTC().Truncate(time.Microsecond).Format(time.RFC3339Nano)
				}
			}
			if b.FirstObservedAt != "" {
				first, err := time.Parse(time.RFC3339Nano, b.FirstObservedAt)
				if err != nil || first.After(report.OccurredAt) {
					return nil, errors.New("faas: blocker first observation exceeds report time")
				}
			}
		}
		blockers := report.Blockers
		if blockers == nil {
			blockers = []OperationWorkflowBlocker{}
		}
		blockersJSON, err := marshalCustomerOperationJSON(blockers)
		if err != nil {
			return nil, err
		}
		resolutions := report.BlockerResolutions
		if resolutions == nil {
			resolutions = []OperationWorkflowBlockerResolution{}
		}
		resolutionsJSON, err := marshalCustomerOperationJSON(resolutions)
		if err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO public.gregale_customer_operation_workflow_states
			(operation_id,id,platform_tenant_id,workflow,instance_id,from_state,state,revision,evidence_milestones,occurred_at,blockers,blockers_only,blocker_resolutions,deadline_at,deadline_only,outcome_code,outcome_description,outcome_only,depends_on,dependencies_only)
			VALUES ($1::uuid,$2::uuid,$3::uuid,$4,$5,$6,$7,$8,$9::jsonb,$10::timestamptz,$11::jsonb,$12::boolean,$13::jsonb,$14::text,$15::boolean,$16::text,$17::text,$18::boolean,$19::jsonb,$20::boolean)`,
			input.operationID, report.ID, input.platformTenantID, report.Workflow, report.InstanceID, report.FromState, report.State,
			report.Revision, string(evidenceJSON), report.OccurredAt, string(blockersJSON), report.BlockersOnly, string(resolutionsJSON), report.DeadlineAt, report.DeadlineOnly, report.OutcomeCode, report.OutcomeDescription, report.OutcomeOnly, string(dependenciesJSON), report.DependenciesOnly); err != nil {
			return nil, fmt.Errorf("faas: save workflow state outbox row: %w", err)
		}
		var updatedRevision int64
		if err := tx.QueryRowContext(ctx, `UPDATE public.gregale_customer_operation_workflow_state_counters SET last_state=$4,last_blockers=$6::jsonb,last_blockers_revision=$5,last_deadline_at=$7,last_deadline_revision=$5,last_outcome_code=$8,last_outcome_description=$9,last_outcome_revision=$5,last_dependencies=$10::jsonb,last_dependencies_revision=$5
			WHERE platform_tenant_id=$1::uuid AND workflow=$2 AND instance_id=$3 AND revision=$5 RETURNING revision`,
			input.platformTenantID, report.Workflow, report.InstanceID, report.State, report.Revision, string(blockersJSON), report.DeadlineAt, report.OutcomeCode, report.OutcomeDescription, string(dependenciesJSON)).Scan(&updatedRevision); err != nil || updatedRevision != report.Revision {
			if err == nil {
				err = errors.New("counter revision changed unexpectedly")
			}
			return nil, fmt.Errorf("faas: update workflow state counter: %w", err)
		}
		saved = append(saved, report)
	}
	return saved, nil
}

// WorkflowBlockers replaces the public blockers without changing business state.
// Check state against the locked business row; [] clears the previous blockers.
func (tx *CustomerOperationTransaction) WorkflowBlockers(workflow, instanceID, state string, blockers []OperationWorkflowBlocker, resolutions ...OperationWorkflowBlockerResolution) error {
	if tx == nil || !tx.open {
		return errors.New("faas: workflow blockers require an open transaction")
	}
	canonical, err := canonicalCustomerWorkflowBlockers(blockers)
	if err != nil {
		return err
	}
	canonicalResolutions, err := canonicalCustomerWorkflowResolutions(resolutions, canonical)
	if err != nil {
		return err
	}
	if err := tx.queueWorkflowState(workflow, instanceID, state, state); err != nil {
		return err
	}
	index := len(tx.workflowStates) - 1
	tx.workflowStates[index].Blockers = canonical
	tx.workflowStates[index].BlockersOnly = true
	tx.workflowStates[index].BlockerResolutions = canonicalResolutions
	batch, err := marshalCustomerOperationJSON(OperationWorkflowStateValidationRequest{WorkflowStates: tx.workflowStates})
	if err != nil || len(batch) > operationWorkflowStateBatchBytes {
		tx.workflowStates = tx.workflowStates[:index]
		return errors.New("faas: workflow blocker batch exceeds its byte limit")
	}
	return nil
}

func canonicalCustomerWorkflowBlockers(blockers []OperationWorkflowBlocker) ([]OperationWorkflowBlocker, error) {
	if len(blockers) > 16 {
		return nil, errors.New("faas: too many workflow blockers")
	}
	result := append(make([]OperationWorkflowBlocker, 0, len(blockers)), blockers...)
	seen := make(map[string]bool, len(result))
	for i, b := range result {
		if b.FirstObservedAt != "" {
			first, err := time.Parse(time.RFC3339Nano, b.FirstObservedAt)
			if err != nil || first.UTC().Year() < 1 || first.UTC().Year() > 9999 {
				return nil, errors.New("faas: invalid blocker observation time")
			}
			result[i].FirstObservedAt = first.UTC().Truncate(time.Microsecond).Format(time.RFC3339Nano)
		}
		key := b.Operation + ":" + b.Code
		if !customerOperationStateName.MatchString(b.Code) || !customerOperationStateName.MatchString(b.Operation) || b.Description == "" || len(b.Description) > 512 || !utf8.ValidString(b.Description) || strings.ContainsFunc(b.Description, func(r rune) bool { return r < 0x20 || r == 0x7f }) || seen[key] {
			return nil, errors.New("faas: invalid workflow blocker public fields or duplicate target/code")
		}
		seen[key] = true
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Operation != result[j].Operation {
			return result[i].Operation < result[j].Operation
		}
		return result[i].Code < result[j].Code
	})
	return result, nil
}

func canonicalCustomerWorkflowResolutions(resolutions []OperationWorkflowBlockerResolution, blockers []OperationWorkflowBlocker) ([]OperationWorkflowBlockerResolution, error) {
	if len(resolutions) > 16 {
		return nil, errors.New("faas: too many blocker resolutions")
	}
	fields := make([]OperationWorkflowBlocker, 0, len(resolutions))
	active := make(map[string]bool, len(blockers))
	for _, b := range blockers {
		active[b.Operation+":"+b.Code] = true
	}
	result := append(make([]OperationWorkflowBlockerResolution, 0, len(resolutions)), resolutions...)
	for _, v := range result {
		if !customerOperationUUIDPattern.MatchString(v.BlockerOperationID) || !customerOperationUUIDPattern.MatchString(v.BlockerReportID) || v.BlockerOperationID == "00000000-0000-0000-0000-000000000000" || v.BlockerReportID == "00000000-0000-0000-0000-000000000000" || v.BlockerRevision < 1 || v.BlockerRevision > 9007199254740991 || active[v.Operation+":"+v.Code] {
			return nil, errors.New("faas: resolution requires prior report identity/revision and a cleared target/code")
		}
		fields = append(fields, OperationWorkflowBlocker{Operation: v.Operation, Code: v.Code, Description: v.Description})
	}
	if _, err := canonicalCustomerWorkflowBlockers(fields); err != nil {
		return nil, err
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Operation != result[j].Operation {
			return result[i].Operation < result[j].Operation
		}
		return result[i].Code < result[j].Code
	})
	return result, nil
}

func canonicalCustomerWorkflowDeadline(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	due, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || due.UTC().Year() < 1 || due.UTC().Year() > 9999 {
		return "", errors.New("faas: workflow deadline requires a finite RFC3339 timestamp")
	}
	return due.UTC().Truncate(time.Microsecond).Format(time.RFC3339Nano), nil
}

// WorkflowDeadline sets or updates the due time; an empty string clears it.
// State must come from the locked business row. Blockers are preserved by the counter.
func (tx *CustomerOperationTransaction) WorkflowDeadline(workflow, instanceID, state, dueAt string) error {
	deadline, err := canonicalCustomerWorkflowDeadline(dueAt)
	if err != nil {
		return err
	}
	if err = tx.queueWorkflowState(workflow, instanceID, state, state); err != nil {
		return err
	}
	i := len(tx.workflowStates) - 1
	tx.workflowStates[i].DeadlineAt = deadline
	tx.workflowStates[i].DeadlineOnly = true
	batch, err := marshalCustomerOperationJSON(OperationWorkflowStateValidationRequest{WorkflowStates: tx.workflowStates})
	if err != nil || len(batch) > operationWorkflowStateBatchBytes {
		tx.workflowStates = tx.workflowStates[:i]
		return errors.New("faas: workflow deadline batch exceeds its byte limit")
	}
	return nil
}

func validateCustomerWorkflowOutcome(code, description string) error {
	if !customerOperationStateName.MatchString(code) || description == "" || len(description) > 512 || !utf8.ValidString(description) || strings.ContainsFunc(description, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return errors.New("faas: outcome requires a bounded public code and description")
	}
	return nil
}

// WorkflowOutcome reports one explicit outcome for the declared terminal state.
// Queue its business transition first when completion happens in this transaction.
func (tx *CustomerOperationTransaction) WorkflowOutcome(workflow, instanceID, state, code, description string) error {
	if err := validateCustomerWorkflowOutcome(code, description); err != nil {
		return err
	}
	if err := tx.queueWorkflowState(workflow, instanceID, state, state); err != nil {
		return err
	}
	i := len(tx.workflowStates) - 1
	tx.workflowStates[i].OutcomeCode = code
	tx.workflowStates[i].OutcomeDescription = description
	tx.workflowStates[i].OutcomeOnly = true
	batch, err := marshalCustomerOperationJSON(OperationWorkflowStateValidationRequest{WorkflowStates: tx.workflowStates})
	if err != nil || len(batch) > operationWorkflowStateBatchBytes {
		tx.workflowStates = tx.workflowStates[:i]
		return errors.New("faas: outcome report batch exceeds its byte limit")
	}
	return nil
}

func canonicalCustomerWorkflowDependencies(dependencies []OperationWorkflowDependency) ([]OperationWorkflowDependency, error) {
	if len(dependencies) > 16 {
		return nil, errors.New("faas: workflow dependencies exceed their limit")
	}
	result := append(make([]OperationWorkflowDependency, 0, len(dependencies)), dependencies...)
	seen := map[[4]string]bool{}
	for _, d := range result {
		key := [4]string{d.SubjectType, d.SubjectID, d.Workflow, d.InstanceID}
		validID := func(value string) bool {
			return value != "" && len(value) <= 256 && utf8.ValidString(value) && !strings.ContainsFunc(value, func(r rune) bool { return r < 0x20 || r == 0x7f })
		}
		if !customerOperationStateName.MatchString(d.SubjectType) || !customerOperationWorkflowName.MatchString(d.Workflow) || !validID(d.SubjectID) || !validID(d.InstanceID) || d.RequiredOutcomeCode != "" && !customerOperationStateName.MatchString(d.RequiredOutcomeCode) || seen[key] {
			return nil, errors.New("faas: invalid or duplicate workflow dependency")
		}
		seen[key] = true
	}
	sort.Slice(result, func(i, j int) bool {
		a, b := result[i], result[j]
		if a.SubjectType != b.SubjectType {
			return a.SubjectType < b.SubjectType
		}
		if a.SubjectID != b.SubjectID {
			return a.SubjectID < b.SubjectID
		}
		if a.Workflow != b.Workflow {
			return a.Workflow < b.Workflow
		}
		return a.InstanceID < b.InstanceID
	})
	return result, nil
}

// WorkflowDependencies replaces direct prerequisites; [] removes all links.
// The state comes from the locked business row; current blockers are preserved.
func (tx *CustomerOperationTransaction) WorkflowDependencies(workflow, instanceID, state string, dependencies []OperationWorkflowDependency) error {
	canonical, err := canonicalCustomerWorkflowDependencies(dependencies)
	if err != nil {
		return err
	}
	if err = tx.queueWorkflowState(workflow, instanceID, state, state); err != nil {
		return err
	}
	i := len(tx.workflowStates) - 1
	tx.workflowStates[i].DependsOn = canonical
	tx.workflowStates[i].DependenciesOnly = true
	batch, err := marshalCustomerOperationJSON(OperationWorkflowStateValidationRequest{WorkflowStates: tx.workflowStates})
	if err != nil || len(batch) > operationWorkflowStateBatchBytes {
		tx.workflowStates = tx.workflowStates[:i]
		return errors.New("faas: dependency report batch exceeds its byte limit")
	}
	return nil
}
