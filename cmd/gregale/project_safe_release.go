package main

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func savedSafeProjectRelease(safe *api.ProjectSafeRelease) *api.ProjectSafeRelease {
	if safe == nil {
		return nil
	}
	copy := *safe
	copy.Additions = append([]api.ProjectReleaseAddition(nil), safe.Additions...)
	if safe.Worker != nil {
		worker := *safe.Worker
		copy.Worker = &worker
	}
	if safe.ReleaseCommand != nil {
		command := *safe.ReleaseCommand
		if command.ExitCode != nil {
			code := *command.ExitCode
			command.ExitCode = &code
		}
		if command.BaselineVerification != nil {
			proof := *command.BaselineVerification
			proof.Evidence = append([]api.ProjectReleaseProbeEvidence(nil), proof.Evidence...)
			command.BaselineVerification = &proof
		}
		if command.Contract != nil {
			contract := *command.Contract
			if contract.ExitCode != nil {
				exit := *contract.ExitCode
				contract.ExitCode = &exit
			}
			if contract.Recovery != nil {
				recovery := *contract.Recovery
				if recovery.ResolvedAt != nil {
					at := *recovery.ResolvedAt
					recovery.ResolvedAt = &at
				}
				contract.Recovery = &recovery
			}
			command.Contract = &contract
		}
		copy.ReleaseCommand = &command
	}
	if safe.Configuration != nil {
		config := *safe.Configuration
		if config.Secrets != nil {
			secretCopy := *config.Secrets
			secretCopy.Versions = map[string]map[string]string{}
			for name, values := range config.Secrets.Versions {
				copyValues := map[string]string{}
				for key, id := range values {
					copyValues[key] = id
				}
				secretCopy.Versions[name] = copyValues
			}
			secretCopy.PreviousVersions = map[string]map[string]string{}
			for name, values := range config.Secrets.PreviousVersions {
				copyValues := map[string]string{}
				for key, id := range values {
					copyValues[key] = id
				}
				secretCopy.PreviousVersions[name] = copyValues
			}
			config.Secrets = &secretCopy
		}
		copy.Configuration = &config
	}
	if safe.Verification != nil {
		v := *safe.Verification
		if v.WorkerHealth != nil {
			health := *v.WorkerHealth
			v.WorkerHealth = &health
		}
		v.Probes = append([]api.ProjectReleaseProbe(nil), v.Probes...)
		v.Evidence = append([]api.ProjectReleaseProbeEvidence(nil), v.Evidence...)
		copy.Verification = &v
	}
	copy.Message = "" // Journals retain identity and state, not arbitrary server text.
	return &copy
}
func waitForSafeProjectRelease(ctx context.Context, c *Client, s *savedDeploySession, deadline time.Duration) int {
	waitCtx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	last := s.projectApplyReceipt()
	for {
		callCtx, callCancel := context.WithTimeout(waitCtx, 10*time.Second)
		receipt, err := c.GetProjectSubmission(callCtx, s.record.Project.AttemptID)
		callCancel()
		if err == nil {
			p := s.record.Project
			if receipt.AttemptID != p.AttemptID || receipt.ProjectSlug != p.Slug || receipt.Environment != p.Environment || receipt.Apply.SafeRelease == nil || receipt.Apply.SafeRelease.PreviousReleaseID != p.SafeRelease.PreviousReleaseID {
				return projectSubmissionFailure(s, errors.New("safe project release receipt does not match the saved attempt"))
			}
			if err := s.rememberProjectApply(receipt.Apply); err != nil {
				return projectSubmissionFailure(s, err)
			}
			last = receipt.Apply
			switch last.SafeRelease.Status {
			case "activated", "succeeded":
				if last.SafeRelease.Verification != nil && last.SafeRelease.Status == "activated" {
					return projectSubmissionFailure(s, errors.New("verified release has no verification outcome"))
				}
				for i := range last.Builds {
					if last.Builds[i].DeploymentStatus == "" {
						last.Builds[i].DeploymentStatus = statusLive
					}
					last.Builds[i].BuildStatus = buildStatusSucceeded
				}
				return renderProjectDeployReceipt(api.PlanResponse{}, last, s, false, nil)
			case "failed", "conflicted", "rolled_back", "rollback_failed":
				return renderProjectDeployReceipt(api.PlanResponse{}, last, s, false, nil)
			case "building", "verifying", "rolling_back":
			default:
				return projectSubmissionFailure(s, errors.New("unrecognized safe release status"))
			}
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-waitCtx.Done():
			timer.Stop()
			if errors.Is(waitCtx.Err(), context.Canceled) {
				return projectSubmissionFailure(s, waitCtx.Err())
			}
			return renderProjectDeployReceipt(api.PlanResponse{}, last, s, true, nil)
		case <-timer.C:
		}
	}
}
