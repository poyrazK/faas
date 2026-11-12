package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// Execution requests are explicit writes. GET and metadata PUT never drive a
// managed suite. Admission and its immutable link share the same store lock/tx.
type StoredWorkflowLiveSuiteExecutionStore interface {
	StartStoredWorkflowLiveSuite(context.Context, string, string, string, api.StartStoredWorkflowLiveSuiteRequest, bool) (api.StoredWorkflowLiveSuite, error)
	AdvanceStoredWorkflowLiveSuite(context.Context, string, string, string, api.AdvanceStoredWorkflowLiveSuiteRequest, bool) (api.StoredWorkflowLiveSuite, error)
}

func managedSuitePlan(id string, request api.StartStoredWorkflowLiveSuiteRequest) (string, api.SaveStoredWorkflowLiveSuiteRequest, error) {
	plan := api.SaveStoredWorkflowLiveSuiteRequest{ExpectedVersion: request.ExpectedVersion, ExpectedPublishedVersion: request.ExpectedPublishedVersion, ExpectedDeploymentID: request.ExpectedDeploymentID}
	if !request.ConfirmRealActions {
		return "", plan, api.ErrValidation("Confirm that this suite may perform real actions before starting it.")
	}
	parsed, err := uuid.Parse(id)
	if err != nil || parsed.Version() != 7 {
		return "", plan, api.ErrValidation("Start a suite with a fresh UUIDv7 and reuse that same ID after a lost response.")
	}
	for _, name := range request.Cases {
		plan.Cases = append(plan.Cases, api.StoredWorkflowLiveSuiteMember{Name: name})
	}
	id, err = validateLiveSuite(id, &plan)
	return id, plan, err
}

func validateManagedSuiteCreation(id string) error {
	parsed := uuid.MustParse(id) // Already validated by managedSuitePlan.
	sec, nsec := parsed.Time().UnixTime()
	age := time.Since(time.Unix(sec, nsec))
	if age < -api.StoredWorkflowLiveSuiteFutureClockSkew || age > api.StoredWorkflowLiveSuiteStartWindow {
		return api.NewProblem(409, api.CodeConflict, "Suite start expired", "This suite ID is too old or has a future timestamp. Inspect retained history before explicitly starting a new suite; an expired or cleared suite must not be recreated by retrying its old request.")
	}
	return nil
}

func managedSuiteReplay(record storedWorkflowLiveSuiteRecord, plan api.SaveStoredWorkflowLiveSuiteRequest) error {
	if !record.Managed {
		return ErrWorkflowRunIdempotencyConflict
	}
	_, err := mergeLiveSuitePlan(record.Plan, plan)
	return err
}

func managedSuiteCaseRequest(record storedWorkflowLiveSuiteRecord, name string) (string, api.StartStoredWorkflowLiveVerificationRequest) {
	sum := sha256.Sum256([]byte("gregale-managed-live-suite-v1:" + record.ID + ":" + name))
	return hex.EncodeToString(sum[:]), api.StartStoredWorkflowLiveVerificationRequest{ExpectedVersion: record.Plan.ExpectedVersion, ExpectedPublishedVersion: record.Plan.ExpectedPublishedVersion, ExpectedDeploymentID: record.Plan.ExpectedDeploymentID, CaseName: name, ConfirmRealActions: true}
}

// Check earlier identities before admitting at most one next case. Missing
// records, failed assertions and resumed/nonterminal runs all stop advancement.
func advanceManagedSuite(record storedWorkflowLiveSuiteRecord, current StoredWorkflow, deployment string, check func(string) (api.StoredWorkflowLiveVerification, error), start func(string, api.StartStoredWorkflowLiveVerificationRequest) (api.StoredWorkflowLiveVerification, error)) (storedWorkflowLiveSuiteRecord, error) {
	if current.Version != record.Plan.ExpectedVersion || current.PublishedVersion != record.Plan.ExpectedPublishedVersion || !sameMemUUID(deployment, record.Plan.ExpectedDeploymentID) {
		return record, nil
	}
	record.Plan = cloneLiveSuiteRequest(record.Plan)
	for i, member := range record.Plan.Cases {
		if member.RunID != "" {
			view, err := check(member.RunID)
			if errors.Is(err, ErrNotFound) || errors.Is(err, ErrWorkflowRunNotFound) {
				return record, nil
			}
			if err != nil {
				return record, err
			}
			if err := validateLiveSuiteLink(record.Plan, member, view); err != nil {
				return record, err
			}
			if view.Outdated || !view.ReportCurrent || view.Report == nil || !view.Report.Passed {
				return record, nil
			}
			continue
		}
		key, request := managedSuiteCaseRequest(record, member.Name)
		view, err := start(key, request)
		if err != nil {
			return record, err
		}
		record.Plan.Cases[i].RunID = view.RunID
		record.UpdatedAt = time.Now().UTC()
		return record, nil
	}
	return record, nil
}
