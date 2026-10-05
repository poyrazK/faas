package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/outbound/routepolicy"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"time"
)

type WorkflowOutboundAttempt struct {
	AccountID        string
	AppID            string
	PlatformTenantID string
	Token            string `json:"-"`
}
type WorkflowOutboundStore interface {
	GetWorkflowOutboundAttempt(context.Context, string, string, int) (WorkflowOutboundAttempt, error)
	ValidateWorkflowOutboundBindings(context.Context, string, api.WorkflowSpec) error
}

func (s *PgStore) GetWorkflowOutboundAttempt(ctx context.Context, runID, step string, attempt int) (WorkflowOutboundAttempt, error) {
	row, err := sqlc.New().WorkflowOutboundAttempt(ctx, s.pool, sqlc.WorkflowOutboundAttemptParams{RunID: mustPgUUID(runID), StepName: step, Attempt: int32(attempt)})
	if err != nil {
		return WorkflowOutboundAttempt{}, err
	}
	return WorkflowOutboundAttempt{AccountID: pgUUIDString(row.AccountID), AppID: pgUUIDString(row.AppID), PlatformTenantID: pgUUIDString(row.PlatformTenantID), Token: pgUUIDString(row.OutboundAttemptToken)}, nil
}
func (m *MemStore) GetWorkflowOutboundAttempt(_ context.Context, runID, stepName string, attempt int) (WorkflowOutboundAttempt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	run, ok := m.workflowRuns[runID]
	if !ok {
		return WorkflowOutboundAttempt{}, ErrNotFound
	}
	step, ok := m.workflowSteps[runID][stepName]
	if !ok {
		return WorkflowOutboundAttempt{}, ErrNotFound
	}
	app := m.apps[run.AppID]
	account := m.accounts[app.AccountID]
	if run.Status != WorkflowRunStatusRunning || step.Status != WorkflowStepStatusRunning || step.Attempt != attempt || step.outboundAttemptToken == "" || !m.workflowRunLeases[runID].After(time.Now()) || !account.Active() || !account.Plan.WorkflowsAllowed() || app.Status == AppDeleted || app.MaintenanceMode || app.PlatformTenantRequired && run.PlatformTenantID == "" {
		return WorkflowOutboundAttempt{}, ErrWorkflowNotRunning
	}
	if run.PlatformTenantID != "" && !m.workflowOutboundTenantLinkActiveLocked(app.AccountID, run.PlatformTenantID, app.ID) {
		return WorkflowOutboundAttempt{}, ErrWorkflowNotRunning
	}
	return WorkflowOutboundAttempt{AccountID: app.AccountID, AppID: app.ID, PlatformTenantID: run.PlatformTenantID, Token: step.outboundAttemptToken}, nil
}

// workflowOutboundTenantLinkActiveLocked mirrors ValidatePlatformTenantAppBinding
// while GetWorkflowOutboundAttempt holds MemStore.mu. It keeps the live-link
// check atomic with the run and outbound-attempt check without re-locking.
func (m *MemStore) workflowOutboundTenantLinkActiveLocked(accountID, tenantID, appID string) bool {
	tenant, ok := m.platformTenants[tenantID]
	if !ok || tenant.AccountID != accountID || tenant.Status != PlatformTenantActive {
		return false
	}
	for consumerID, linkedTenantID := range m.platformTenantByConsumer {
		consumer, ok := m.apiConsumers[consumerID]
		if ok && linkedTenantID == tenantID && consumer.PlatformTenantID == tenantID && consumer.AccountID == accountID && consumer.AppID == appID && consumer.Active() {
			return true
		}
	}
	for surfaceID, linkedTenantID := range m.platformTenantBySurface {
		surface, ok := m.tenantSurfaces[surfaceID]
		if ok && linkedTenantID == tenantID && surface.AccountID == accountID && surface.AppID == appID && surface.Active() {
			return true
		}
	}
	return false
}

func outboundBindingAllows(step api.WorkflowOutboundSpec, methods, paths, bindingMethods, bindingPaths []string) bool {
	if !api.WorkflowOutboundPathAllowedByPolicy(step.Method, step.Path, routepolicy.Policy{AllowedMethods: methods, AllowedPathPrefixes: paths}) {
		return false
	}
	return bindingMethods == nil && bindingPaths == nil || api.WorkflowOutboundPathAllowedByPolicy(step.Method, step.Path, routepolicy.Policy{AllowedMethods: bindingMethods, AllowedPathPrefixes: bindingPaths})
}
func invalidOutboundBinding(name string) error {
	return fmt.Errorf("%w: step %q requires an enabled customer managed integration with a credential and app binding allowing its route", ErrAutomationInvalid, name)
}
func validateWorkflowOutboundTx(ctx context.Context, db sqlc.DBTX, appID, accountID string, spec api.WorkflowSpec) error {
	q := sqlc.New()
	for _, step := range workflowOutboundActions(spec) {
		if step.Outbound == nil {
			continue
		}
		if err := api.ValidateWorkflowOutboundStep(step); err != nil {
			return invalidOutboundBinding(step.Name)
		}
		row, err := q.WorkflowOutboundBinding(ctx, db, sqlc.WorkflowOutboundBindingParams{IntegrationID: mustPgUUID(step.Outbound.IntegrationID), AppID: mustPgUUID(appID), AccountID: mustPgUUID(accountID)})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("read workflow outbound binding: %w", err)
		}
		if err != nil || !outboundBindingAllows(*step.Outbound, row.AllowedMethods, row.AllowedPathPrefixes, row.BindingMethods, row.BindingPaths) {
			return invalidOutboundBinding(step.Name)
		}
	}
	return nil
}
func (s *PgStore) ValidateWorkflowOutboundBindings(ctx context.Context, appID string, spec api.WorkflowSpec) error {
	app, err := s.AppByID(ctx, appID)
	if err != nil {
		return err
	}
	return validateWorkflowOutboundTx(ctx, s.pool, appID, app.AccountID, spec)
}
func (m *MemStore) validateWorkflowOutboundLocked(appID, accountID string, spec api.WorkflowSpec) error {
	for _, step := range workflowOutboundActions(spec) {
		if step.Outbound == nil {
			continue
		}
		offer, ok := m.outboundIntegrationOffers[step.Outbound.IntegrationID]
		found := false
		for _, binding := range m.outboundAppBindings {
			if binding.ID == step.Outbound.IntegrationID && binding.AccountID == accountID && binding.AppID == appID && outboundBindingAllows(*step.Outbound, offer.AllowedMethods, offer.AllowedPathPrefixes, binding.RouteMethods, binding.RoutePathPrefixes) {
				found = true
			}
		}
		if !ok || offer.AccountID != accountID || !offer.Enabled || offer.OwnerKind != "customer" || offer.CredentialSource != "customer_sealed" || len(m.outboundCredentials[offer.ID]) == 0 || !found {
			return invalidOutboundBinding(step.Name)
		}
	}
	return nil
}
func (m *MemStore) ValidateWorkflowOutboundBindings(_ context.Context, appID string, spec api.WorkflowSpec) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[appID]
	if !ok {
		return ErrNotFound
	}
	return m.validateWorkflowOutboundLocked(appID, app.AccountID, spec)
}

func workflowOutboundSpec(snapshot json.RawMessage, name string) *api.WorkflowOutboundSpec {
	step := api.WorkflowRuntimeStep(snapshot, name)
	if step == nil {
		return nil
	}
	return step.Outbound
}
func recoverWorkflowStepsTx(ctx context.Context, tx sqlc.DBTX, runID string) error {
	q := sqlc.New()
	id := mustPgUUID(runID)
	if err := q.MarkWorkflowOutboundUnknown(ctx, tx, id); err != nil {
		return err
	}
	if err := q.CloseWorkflowOutboundUnknownAttempts(ctx, tx, id); err != nil {
		return err
	}
	return q.ResetWorkflowRunningSteps(ctx, tx, id)
}

func checkWorkflowOutboundCompletionTx(ctx context.Context, tx sqlc.DBTX, runID, stepName string, attempt int) error {
	current, err := sqlc.New().WorkflowOutboundCompletionCurrent(ctx, tx, sqlc.WorkflowOutboundCompletionCurrentParams{RunID: mustPgUUID(runID), StepName: stepName, Attempt: int32(attempt)})
	if err != nil {
		return err
	}
	if !current {
		return ErrWorkflowOutboundAttemptExpired
	}
	return nil
}

func (m *MemStore) recoverWorkflowStepsLocked(run WorkflowRun, now time.Time) {
	for name, step := range m.workflowSteps[run.ID] {
		if step.Status != WorkflowStepStatusRunning {
			continue
		}
		outbound := workflowOutboundSpec(run.DefinitionSnapshot, name)
		if outbound != nil {
			message := "outbound result unknown after recovery"
			if !outbound.SafeToRepeat() {
				message = "outbound result unknown; unsafe to repeat"
				step.Status = WorkflowStepStatusDead
				step.Error = &message
				step.FinishedAt = &now
			}
			key := workflowStepAttemptKey{runID: run.ID, stepName: name, attempt: step.Attempt}
			if record, ok := m.workflowStepAttempts[key]; ok && record.Status == WorkflowAttemptStatusRunning {
				record.Status = WorkflowAttemptStatusFailed
				record.Error = &message
				record.FinishedAt = &now
				m.workflowStepAttempts[key] = record
			}
		}
		if outbound == nil && (step.ForEachParent != nil || run.ResumeCount > 0) {
			key := workflowStepAttemptKey{runID: run.ID, stepName: name, attempt: step.Attempt}
			if record, ok := m.workflowStepAttempts[key]; ok && record.Status == WorkflowAttemptStatusRunning {
				message := "for_each item result unknown after recovery"
				record.Status, record.Error, record.FinishedAt = WorkflowAttemptStatusFailed, &message, &now
				m.workflowStepAttempts[key] = record
			}
		}
		step.outboundAttemptToken = ""
		if step.Status == WorkflowStepStatusRunning {
			step.Status = WorkflowStepStatusPending
			if step.Attempt > 0 && outbound == nil && step.ForEachParent == nil && run.ResumeCount == 0 {
				step.Attempt--
			}
			step.FinishedAt = nil
			step.Error = nil
			step.NextRetryAt = nil
		}
		m.workflowSteps[run.ID][name] = step
	}
}

var ErrWorkflowOutboundAttemptExpired = errors.New("workflow outbound attempt expired")

func workflowOutboundActions(spec api.WorkflowSpec) []api.WorkflowStepSpec {
	steps := make([]api.WorkflowStepSpec, 0, len(spec.Steps))
	for _, step := range spec.Steps {
		if step.ForEach != nil {
			step = step.ForEach.Action.Step(step.Name)
		}
		steps = append(steps, step)
	}
	return steps
}
