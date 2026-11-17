package state

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/storedworkflowlive"
)

type StoredWorkflowLiveSuiteStore interface {
	SaveStoredWorkflowLiveSuite(context.Context, string, string, string, api.SaveStoredWorkflowLiveSuiteRequest) (api.StoredWorkflowLiveSuite, error)
	GetStoredWorkflowLiveSuite(context.Context, string, string, string) (api.StoredWorkflowLiveSuite, error)
	ListStoredWorkflowLiveSuites(context.Context, string, string) (api.ListStoredWorkflowLiveSuitesResponse, error)
}

type storedWorkflowLiveSuiteRecord struct {
	ID, AppID, Name      string
	Plan                 api.SaveStoredWorkflowLiveSuiteRequest
	Managed              bool
	CreatedAt, UpdatedAt time.Time
}

func validateLiveSuite(id string, request *api.SaveStoredWorkflowLiveSuiteRequest) (string, error) {
	suiteID, err := uuid.Parse(id)
	if err != nil || suiteID == uuid.Nil {
		return "", ErrNotFound
	}
	deployment, err := uuid.Parse(request.ExpectedDeploymentID)
	if err != nil || deployment == uuid.Nil || request.ExpectedVersion <= 0 || request.ExpectedPublishedVersion <= 0 || request.ExpectedPublishedVersion > request.ExpectedVersion {
		return "", ErrStoredWorkflowVersionConflict
	}
	request.ExpectedDeploymentID = deployment.String()
	if len(request.Cases) == 0 || len(request.Cases) > api.StoredWorkflowLiveSuiteMaxCases {
		return "", api.ErrValidation("Select 1..32 distinct saved live cases.")
	}
	seen := map[string]bool{}
	for i := range request.Cases {
		member := &request.Cases[i]
		if member.Name == "" || strings.TrimSpace(member.Name) != member.Name || len(member.Name) > api.StoredWorkflowTestCaseNameMaxBytes || !utf8.ValidString(member.Name) || strings.IndexFunc(member.Name, unicode.IsControl) >= 0 || seen[member.Name] {
			return "", api.ErrValidation("Suite cases require distinct, bounded saved names.")
		}
		seen[member.Name] = true
		if member.RunID != "" {
			run, err := uuid.Parse(member.RunID)
			if err != nil || run == uuid.Nil {
				return "", api.ErrValidation("A suite member must reference a valid verification run UUID.")
			}
			member.RunID = run.String()
		}
	}
	return suiteID.String(), nil
}

func validateLiveSuiteTarget(plan api.SaveStoredWorkflowLiveSuiteRequest, current StoredWorkflow, deployment string) error {
	if current.Version != plan.ExpectedVersion || current.PublishedVersion != plan.ExpectedPublishedVersion || !sameMemUUID(deployment, plan.ExpectedDeploymentID) {
		return ErrStoredWorkflowVersionConflict
	}
	for _, member := range plan.Cases {
		def, saved, err := liveCase(current, member.Name)
		if err != nil || saved.Live == nil || storedworkflowlive.Validate(def, *saved.Live) != nil {
			return ErrStoredWorkflowInvalid
		}
	}
	return nil
}

// Merge only attaches identities. Frozen selection and existing run links cannot
// be replaced, and an older writer cannot clear a newer writer's progress.
func mergeLiveSuitePlan(prior, incoming api.SaveStoredWorkflowLiveSuiteRequest) (api.SaveStoredWorkflowLiveSuiteRequest, error) {
	if prior.ExpectedVersion != incoming.ExpectedVersion || prior.ExpectedPublishedVersion != incoming.ExpectedPublishedVersion || prior.ExpectedDeploymentID != incoming.ExpectedDeploymentID || len(prior.Cases) != len(incoming.Cases) {
		return prior, ErrWorkflowRunIdempotencyConflict
	}
	merged := prior
	merged.Cases = append([]api.StoredWorkflowLiveSuiteMember(nil), prior.Cases...)
	for i, member := range incoming.Cases {
		if member.Name != prior.Cases[i].Name || member.RunID != "" && prior.Cases[i].RunID != "" && member.RunID != prior.Cases[i].RunID {
			return prior, ErrWorkflowRunIdempotencyConflict
		}
		if member.RunID != "" {
			merged.Cases[i].RunID = member.RunID
		}
	}
	return merged, nil
}

func validateLiveSuiteLink(plan api.SaveStoredWorkflowLiveSuiteRequest, member api.StoredWorkflowLiveSuiteMember, view api.StoredWorkflowLiveVerification) error {
	if view.RunID != member.RunID || view.CaseName != member.Name || view.Version != plan.ExpectedVersion || view.PublishedVersion != plan.ExpectedPublishedVersion || !sameMemUUID(view.DeploymentID, plan.ExpectedDeploymentID) {
		return ErrWorkflowRunIdempotencyConflict
	}
	return nil
}

func liveSuiteSummary(record storedWorkflowLiveSuiteRecord) api.StoredWorkflowLiveSuiteSummary {
	result := api.StoredWorkflowLiveSuiteSummary{ID: record.ID, Name: record.Name, Version: record.Plan.ExpectedVersion, PublishedVersion: record.Plan.ExpectedPublishedVersion, DeploymentID: record.Plan.ExpectedDeploymentID,
		CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt, CaseCount: len(record.Plan.Cases), Managed: record.Managed}
	for _, member := range record.Plan.Cases {
		if member.RunID != "" {
			result.LinkedRuns++
		}
	}
	return result
}

func liveSuiteView(record storedWorkflowLiveSuiteRecord, current StoredWorkflow, deployment string, lookup func(string) (api.StoredWorkflowLiveVerification, error)) (api.StoredWorkflowLiveSuite, error) {
	result := api.StoredWorkflowLiveSuite{StoredWorkflowLiveSuiteSummary: liveSuiteSummary(record), ObservedAt: time.Now().UTC(), Cases: []api.StoredWorkflowLiveSuiteCase{}, Summary: api.StoredWorkflowLiveSuiteCounts{Selected: len(record.Plan.Cases)}}
	result.Outdated = current.Version != record.Plan.ExpectedVersion || current.PublishedVersion != record.Plan.ExpectedPublishedVersion || !sameMemUUID(deployment, record.Plan.ExpectedDeploymentID)
	for _, member := range record.Plan.Cases {
		entry := api.StoredWorkflowLiveSuiteCase{Name: member.Name, RunID: member.RunID, Outcome: "not_started"}
		if member.RunID == "" {
			result.Summary.NotStarted++
		} else {
			view, err := lookup(member.RunID)
			if err != nil && !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrWorkflowRunNotFound) {
				return result, err
			}
			entry.Outcome = "unavailable"
			if err == nil {
				if err := validateLiveSuiteLink(record.Plan, member, view); err != nil {
					return result, err
				}
				entry.Verification = &view
				switch {
				case view.Outdated:
					entry.Outcome = "outdated"
				case !liveRunTerminal(view.RunStatus):
					entry.Outcome = "pending"
				case view.ReportCurrent && view.Report != nil:
					entry.Outcome = "failed"
					if view.Report.Passed {
						entry.Outcome = "passed"
					}
				}
			}
			switch entry.Outcome {
			case "passed":
				result.Summary.Passed++
			case "failed":
				result.Summary.Failed++
			case "pending":
				result.Summary.Pending++
			case "outdated":
				result.Summary.Outdated++
			default:
				result.Summary.Unavailable++
			}
		}
		result.Cases = append(result.Cases, entry)
	}
	result.Outcome = "passed"
	switch {
	case result.Outdated || result.Summary.Outdated > 0:
		result.Outcome = "outdated"
	case result.Summary.Failed > 0:
		result.Outcome = "failed"
	case result.Summary.Unavailable > 0:
		result.Outcome = "unavailable"
	case result.Summary.Pending > 0:
		result.Outcome = "pending"
	case result.Summary.NotStarted > 0:
		result.Outcome = "incomplete"
	}
	return result, nil
}

func cloneLiveSuiteRequest(request api.SaveStoredWorkflowLiveSuiteRequest) api.SaveStoredWorkflowLiveSuiteRequest {
	request.Cases = append([]api.StoredWorkflowLiveSuiteMember(nil), request.Cases...)
	return request
}

func liveSuitePlanEqual(a, b api.SaveStoredWorkflowLiveSuiteRequest) bool {
	aJSON, _ := json.Marshal(a)
	bJSON, _ := json.Marshal(b)
	return string(aJSON) == string(bJSON)
}
