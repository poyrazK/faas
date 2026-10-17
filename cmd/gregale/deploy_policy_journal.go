package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

const (
	deployPoliciesPrepared          = "prepared"
	deployPoliciesSubmissionStarted = "submission_started"
)

type deployPolicyJournal struct {
	Phase   string                       `json:"phase"`
	Intent  api.DeploymentStagingRequest `json:"intent"`
	Cleaned bool                         `json:"cleaned,omitempty"`
}

type deployPolicyClient interface {
	GetDeploymentStagingSnapshot(context.Context, string) (api.DeploymentStagingSnapshot, error)
	ApplyDeploymentStaging(context.Context, string, api.DeploymentStagingRequest) (api.DeploymentStagingReceipt, error)
	deployCronStagingClient
}

func desiredDeployPolicies(dir string) (map[string]json.RawMessage, error) {
	desired := make(map[string]json.RawMessage)
	if dir == "" {
		return desired, nil
	}
	m, found, err := gregalemanifest.Load(dir)
	if err != nil || !found || m == nil {
		return desired, err
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	if m.Lifecycle != nil {
		if m.Lifecycle.Profiling != nil {
			desired["profiling"], err = json.Marshal(m.Lifecycle.Profiling)
			if err != nil {
				return nil, err
			}
		}
		if m.Lifecycle.Tracing != nil {
			desired["tracing"], err = json.Marshal(m.Lifecycle.Tracing)
			if err != nil {
				return nil, err
			}
		}
	}
	return desired, nil
}

func (s *savedDeploySession) preparePolicyJournal(ctx context.Context, c deployPolicyClient, dir string) error {
	intent := api.DeploymentStagingRequest{SessionID: s.record.ID, Operation: "stage", ExpectedAppID: s.record.AppID, Before: map[string]json.RawMessage{}, Desired: map[string]json.RawMessage{}}
	s.record.PolicyJournal = &deployPolicyJournal{Phase: deployPoliciesPrepared, Intent: intent}
	if err := s.save(); err != nil {
		return err
	}
	desired, err := desiredDeployPolicies(dir)
	if err != nil {
		return err
	}
	if len(desired) > 0 {
		snapshot, err := c.GetDeploymentStagingSnapshot(ctx, s.record.AppSlug)
		if err != nil {
			return fmt.Errorf("snapshot deployment policies: %w", err)
		}
		if snapshot.AppID == "" || (s.record.AppID != "" && snapshot.AppID != s.record.AppID) || snapshot.Revision < 0 {
			return fmt.Errorf("deployment policy snapshot has a different app or invalid revision")
		}
		if err := api.ValidatePolicySettings(snapshot.Settings); err != nil {
			return err
		}
		s.record.AppID = snapshot.AppID
		intent.ExpectedAppID, intent.ExpectedRevision, intent.ExpectedDeploymentID = snapshot.AppID, snapshot.Revision, snapshot.LatestDeploymentID
		for field, value := range desired {
			before, found := snapshot.Settings[field]
			if !found {
				return fmt.Errorf("policy snapshot is missing %s", field)
			}
			intent.Before[field], intent.Desired[field] = before, value
		}
	}
	s.record.PolicyJournal.Intent = intent
	return s.save()
}

func (s *savedDeploySession) stagePolicies(ctx context.Context, c deployPolicyClient) error {
	j := s.record.PolicyJournal
	if j == nil || len(j.Intent.Desired) == 0 {
		return nil
	}
	receipt, err := c.ApplyDeploymentStaging(ctx, s.record.AppSlug, j.Intent)
	if err != nil {
		return fmt.Errorf("stage deployment policies: %w", err)
	}
	if receipt.SessionID != s.record.ID || receipt.AppID != s.record.AppID || receipt.Status != "staged" || receipt.Revision != j.Intent.ExpectedRevision+1 {
		return fmt.Errorf("staging receipt does not match this saved deploy")
	}
	return nil
}

func (s *savedDeploySession) markSubmissionStarted() error {
	j := s.record.PolicyJournal
	if j == nil || j.Phase == deployPoliciesSubmissionStarted {
		return nil
	}
	if j.Cleaned {
		return fmt.Errorf("deployment policy cleanup already completed; start a new deploy")
	}
	j.Phase = deployPoliciesSubmissionStarted
	if err := s.save(); err != nil {
		return fmt.Errorf("persist deployment submission boundary: %w", err)
	}
	return nil
}

func (s *savedDeploySession) shouldCleanPolicies() bool {
	j := s.record.PolicyJournal
	if s.record.UploadAcceptanceUncertain && s.record.Outcome != deploySubmissionRejected {
		return false
	}
	return j != nil && s.record.DeploymentID == "" && s.record.Outcome != deploySubmissionAccepted &&
		(j.Phase == deployPoliciesPrepared || s.record.Outcome == deploySubmissionRejected)
}

func (s *savedDeploySession) cleanPolicies(ctx context.Context, c deployPolicyClient) error {
	if !s.shouldCleanPolicies() {
		return nil
	}
	cronErr := s.cleanCrons(ctx, c)
	j := s.record.PolicyJournal
	if j.Cleaned {
		return cronErr
	}
	if len(j.Intent.Desired) > 0 {
		request := j.Intent
		request.Operation = "restore"
		receipt, err := c.ApplyDeploymentStaging(ctx, s.record.AppSlug, request)
		if err != nil {
			return errors.Join(cronErr, fmt.Errorf("restore deployment policies: %w", err))
		}
		if receipt.SessionID != s.record.ID || receipt.AppID != s.record.AppID || receipt.Status != "cleaned" || receipt.Revision <= j.Intent.ExpectedRevision {
			return errors.Join(cronErr, fmt.Errorf("cleanup receipt does not match this saved deploy"))
		}
	}
	j.Cleaned = true
	s.record.Outcome = deploySubmissionRejected
	if err := s.save(); err != nil {
		j.Cleaned = false
		return errors.Join(cronErr, fmt.Errorf("save completed policy cleanup: %w", err))
	}
	return cronErr
}

func validatePolicyJournal(record deploySessionRecord) error {
	j := record.PolicyJournal
	if j == nil {
		return nil
	}
	if (j.Phase != deployPoliciesPrepared && j.Phase != deployPoliciesSubmissionStarted) || j.Intent.SessionID != record.ID || j.Intent.ExpectedAppID != record.AppID || j.Intent.Operation != "stage" || j.Intent.ExpectedRevision < 0 {
		return fmt.Errorf("saved deploy policy journal has an invalid identity or phase")
	}
	if len(j.Intent.Desired) == 0 {
		if len(j.Intent.Before) != 0 {
			return fmt.Errorf("saved deploy policy journal is incomplete")
		}
		return nil
	}
	return j.Intent.Validate()
}

func (s *savedDeploySession) renderPolicyCleanup() int {
	outcome := "rejected"
	if s.record.PolicyJournal.Phase == deployPoliciesPrepared {
		outcome = "not_submitted"
	}
	if jsonOutput {
		return jsonOut(writeJSON(map[string]any{"session_id": s.record.ID, "app_id": s.record.AppID, "app_slug": s.record.AppSlug,
			"submission_outcome": outcome, "policy_cleanup_complete": true, "cron_cleanup_complete": s.record.CronJournal == nil || !s.record.CronJournal.Managed || s.record.CronJournal.Cleaned, "deployment_created": false, "resume_command": s.resumeCommand()}))
	}
	PrintOK(osStdout, "Recovered deployment settings for saved deploy %s; no deployment created. Rerun the original deploy to submit.", s.record.ID)
	return 0
}
