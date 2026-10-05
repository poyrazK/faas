package gateway

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/logsanitize"
)

// WorkflowAdmissionFunc is the gateway-side replay check for a synthetic
// workflow delivery. The callback must verify that the referenced run and
// step are still running at the supplied attempt. It is intentionally a
// narrow callback so gateway does not own workflow persistence.
type WorkflowAdmissionFunc func(ctx context.Context, appID, runID, platformTenantID, stepName string, attempt int) error

// ManagedWorkflowOperationIdentityFunc confirms opt-in in the immutable run
// definition and returns the app owner's account ID for a managed step.
type ManagedWorkflowOperationIdentityFunc func(ctx context.Context, appID, runID, stepName string) (accountID string, enabled bool, err error)

// WithWorkflowAdmission wires the durable workflow delivery gate. A nil
// callback keeps non-workflow synth traffic unchanged, but workflow traffic
// fails closed when the callback is absent.
func (s *SynthServer) WithWorkflowAdmission(admit WorkflowAdmissionFunc) *SynthServer {
	s.workflowAdmission = admit
	return s
}

func (s *SynthServer) WithManagedWorkflowOperationIdentity(resolve ManagedWorkflowOperationIdentityFunc) *SynthServer {
	s.managedWorkflowOperationIdentity = resolve
	return s
}

// applyWorkflowAdmission authenticates and admits one workflow invocation.
// Workflow requests use the same short-lived internal-service JWT as other
// daemon-to-gateway calls, but are gated regardless of the app's public auth
// mode. The run/step/attempt metadata is checked before the invocation can
// reach the customer instance, which makes a replay after a terminal state a
// harmless conflict rather than a second side effect.
func (s *SynthServer) applyWorkflowAdmission(w http.ResponseWriter, r *http.Request, appID, platformTenantID string, headers map[string]string) bool {
	if s.internalSvcVerifier == nil {
		http.Error(w, "workflow admission verifier is not configured", http.StatusInternalServerError)
		return true
	}
	if s.workflowAdmission == nil {
		http.Error(w, "workflow admission store is not configured", http.StatusInternalServerError)
		return true
	}
	tok, ok := bearerFromHeader(r)
	if !ok {
		http.Error(w, "workflow invocation requires Authorization: Bearer", http.StatusForbidden)
		return true
	}
	svcName, err := s.internalSvcVerifier.Verify(r.Context(), tok)
	if err != nil || svcName != "schedd" {
		http.Error(w, "workflow invocation token is invalid", http.StatusForbidden)
		return true
	}
	if headers["X-Faas-Internal-Wake"] != "workflow" {
		http.Error(w, "workflow invocation marker is missing", http.StatusBadRequest)
		return true
	}
	runID := strings.TrimSpace(headers["X-Faas-Workflow-Run-Id"])
	stepName := strings.TrimSpace(headers["X-Faas-Workflow-Step"])
	attempt, err := strconv.Atoi(strings.TrimSpace(headers["X-Faas-Workflow-Attempt"]))
	if runID == "" || stepName == "" || err != nil || attempt < 1 {
		http.Error(w, "workflow invocation metadata is invalid", http.StatusBadRequest)
		return true
	}
	if err := s.workflowAdmission(r.Context(), appID, runID, platformTenantID, stepName, attempt); err != nil {
		if s.log != nil {
			s.log.Info("gateway synth: workflow delivery rejected", "app_id", logsanitize.Field(appID), "run_id", logsanitize.Field(runID), "step", logsanitize.Field(stepName), "attempt", attempt, "err", logsanitize.FieldAny(err))
		}
		http.Error(w, "workflow delivery rejected", http.StatusConflict)
		return true
	}
	return false
}

func (s *SynthServer) managedWorkflowOperationAccount(ctx context.Context, appID string, headers map[string]string, operationID string, generation int64) (string, error) {
	if s.managedWorkflowOperationIdentity == nil {
		return "", fmt.Errorf("managed workflow operation identity is not configured")
	}
	runID := strings.TrimSpace(headers["X-Faas-Workflow-Run-Id"])
	stepName := strings.TrimSpace(headers["X-Faas-Workflow-Step"])
	attempt, err := strconv.Atoi(strings.TrimSpace(headers["X-Faas-Workflow-Attempt"]))
	if err != nil || attempt < 1 || int64(attempt) != generation {
		return "", fmt.Errorf("managed workflow operation generation does not match the active attempt")
	}
	expected, err := api.ManagedWorkflowStepOperationID(runID, stepName)
	if err != nil || operationID != expected {
		return "", fmt.Errorf("managed workflow operation ID does not match the active run and step")
	}
	accountID, enabled, err := s.managedWorkflowOperationIdentity(ctx, appID, runID, stepName)
	if err != nil {
		return "", err
	}
	if !enabled || accountID == "" {
		return "", fmt.Errorf("managed workflow operations are not enabled for this step")
	}
	return accountID, nil
}
