package main

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

type deployCronJournal struct {
	Intent   api.DeploymentCronStagingRequest `json:"intent"`
	Status   string                           `json:"status,omitempty"`
	Deferred bool                             `json:"deferred,omitempty"`
	Managed  bool                             `json:"managed"`
	Cleaned  bool                             `json:"cleaned,omitempty"`
}
type deployCronStagingClient interface {
	GetDeploymentCronStagingSnapshot(context.Context, string) (api.DeploymentCronStagingSnapshot, error)
	ApplyDeploymentCronStaging(context.Context, string, api.DeploymentCronStagingRequest) (api.DeploymentStagingReceipt, error)
}

func (s *savedDeploySession) prepareCronJournal(ctx context.Context, c deployCronStagingClient, dir string, noTriggers bool) error {
	if noTriggers || dir == "" || s.record.DarkDeploy {
		return nil
	}
	m, found, err := gregalemanifest.Load(dir)
	if err != nil || !found || m == nil {
		return err
	}
	if err = m.Validate(); err != nil {
		return err
	}
	manage := m.Triggers != nil && len(m.Triggers) == 0
	for _, t := range m.Triggers {
		if t.Kind == gregalemanifest.TriggerKindCron && t.App == s.record.AppSlug {
			manage = true
		}
	}
	if !manage {
		return nil
	}
	s.record.CronJournal = &deployCronJournal{}
	if err = s.save(); err != nil {
		return err
	}
	snapshot, err := c.GetDeploymentCronStagingSnapshot(ctx, s.record.AppSlug)
	if err != nil {
		return fmt.Errorf("snapshot deployment crons: %w", err)
	}
	if snapshot.AppID != s.record.AppID || snapshot.Revision < 0 {
		return fmt.Errorf("cron snapshot has a different app or invalid revision")
	}
	if err = api.ValidateDeploymentCrons(snapshot.Crons); err != nil {
		return err
	}
	request := api.DeploymentCronStagingRequest{SessionID: s.record.ID, Operation: "stage", ExpectedAppID: s.record.AppID, ExpectedDeploymentID: snapshot.LatestDeploymentID, ExpectedRevision: snapshot.Revision, Before: snapshot.Crons, Desired: []api.DeploymentCronConfig{}}
	existing := map[string]string{}
	for _, cron := range snapshot.Crons {
		existing[manifestCronKey(cron.Schedule, cron.Path)] = cron.ID
	}
	for _, t := range m.Triggers {
		if t.Kind != gregalemanifest.TriggerKindCron || t.App != s.record.AppSlug {
			continue
		}
		req := manifestCronRequest(s.record.AppSlug, t)
		id := existing[manifestCronKey(req.Schedule, req.Path)]
		if id == "" {
			generated, err := uuid.NewRandom()
			if err != nil {
				return err
			}
			id = generated.String()
		}
		request.Desired = append(request.Desired, api.DeploymentCronConfig{ID: id, Schedule: req.Schedule, Path: req.Path, Enabled: *req.Enabled, Timezone: req.Timezone, SkipIfRunning: *req.SkipIfRunning})
	}
	if err = request.Validate(); err != nil {
		return err
	}
	s.record.CronJournal = &deployCronJournal{Intent: request, Managed: true, Deferred: true}
	return s.save()
}
func (s *savedDeploySession) stageCrons(ctx context.Context, c deployCronStagingClient) error {
	j := s.record.CronJournal
	if j == nil || !j.Managed {
		return nil
	}
	request := j.Intent
	status := "staged"
	if j.Deferred {
		request.Operation, status = "prepare", "prepared"
	}
	receipt, err := c.ApplyDeploymentCronStaging(ctx, s.record.AppSlug, request)
	if err != nil {
		return fmt.Errorf("stage deployment crons: %w", err)
	}
	if receipt.SessionID != s.record.ID || receipt.AppID != s.record.AppID || receipt.Status != status || receipt.Revision != j.Intent.ExpectedRevision+1 {
		return fmt.Errorf("cron staging receipt does not match this saved deploy")
	}
	if j.Deferred {
		j.Status = receipt.Status
		return s.save()
	}
	return nil
}
func (s *savedDeploySession) cleanCrons(ctx context.Context, c deployCronStagingClient) error {
	j := s.record.CronJournal
	if j == nil || !j.Managed || j.Cleaned {
		return nil
	}
	request := j.Intent
	request.Operation = "restore"
	if j.Deferred {
		request.Operation = "cancel"
	}
	receipt, err := c.ApplyDeploymentCronStaging(ctx, s.record.AppSlug, request)
	if err != nil {
		return fmt.Errorf("restore deployment crons: %w", err)
	}
	if receipt.SessionID != s.record.ID || receipt.AppID != s.record.AppID || receipt.Status != "cleaned" || receipt.Revision <= j.Intent.ExpectedRevision {
		return fmt.Errorf("cron cleanup receipt does not match this saved deploy")
	}
	j.Cleaned = true
	if err = s.save(); err != nil {
		j.Cleaned = false
		return fmt.Errorf("save completed cron cleanup: %w", err)
	}
	return nil
}
func validateCronJournal(record deploySessionRecord) error {
	j := record.CronJournal
	if j == nil || !j.Managed {
		return nil
	}
	if record.PolicyJournal == nil || j.Intent.SessionID != record.ID || j.Intent.ExpectedAppID != record.AppID || j.Intent.Operation != "stage" {
		return fmt.Errorf("saved deploy cron journal has an invalid identity")
	}
	return j.Intent.Validate()
}

func (s *savedDeploySession) refreshCronActivation(ctx context.Context, c deployCronStagingClient) error {
	j := s.record.CronJournal
	if j == nil || !j.Managed || !j.Deferred {
		return nil
	}
	if j.Status == "activated" {
		return nil // Activation is historical completion, even after another deploy.
	}
	if reader, ok := c.(deploymentConfigurationClient); ok {
		if handled, err := s.readCronConfiguration(ctx, reader); handled {
			return err
		}
	}
	request := j.Intent
	request.Operation = "prepare"
	receipt, err := c.ApplyDeploymentCronStaging(ctx, s.record.AppSlug, request)
	if err != nil {
		return fmt.Errorf("check pending cron activation: %w", err)
	}
	if receipt.SessionID != s.record.ID || receipt.AppID != s.record.AppID || receipt.Revision != j.Intent.ExpectedRevision+1 {
		return fmt.Errorf("cron activation receipt does not match saved deploy")
	}
	switch receipt.Status {
	case "prepared", "activated", "conflict", "discarded":
	default:
		return fmt.Errorf("unexpected cron activation status")
	}
	j.Status = receipt.Status
	if err = s.save(); err != nil {
		return err
	}
	if receipt.Status == "conflict" || receipt.Status == "discarded" {
		return fmt.Errorf("cron activation %s; active schedules were preserved; inspect current crons and rerun deploy", receipt.Status)
	}
	return nil
}

func (s *savedDeploySession) bindCommittedCronUpload(ctx context.Context, c *Client, uploadID, deploymentID string) error {
	j := s.record.CronJournal
	if j == nil || !j.Managed || !j.Deferred {
		return nil
	}
	ctx = api.ContextWithDeploymentAttempt(ctx, s.record.AttemptID)
	dep, err := c.CommitUpload(ctx, uploadID)
	if err != nil {
		if committedDeploymentID(err) == deploymentID {
			return nil
		}
		return fmt.Errorf("retain committed upload cron activation receipt: %w", err)
	}
	if dep.ID != deploymentID {
		return fmt.Errorf("committed upload returned a different deployment")
	}
	return nil
}

func renderCronActivation(ctx context.Context, deployments ...api.DeploymentResponse) {
	var deployment api.DeploymentResponse
	if len(deployments) > 0 {
		deployment = deployments[0]
	}
	if s := sessionFromContext(ctx); s != nil && s.record.CronConfiguration != nil {
		deployment.Configuration = s.record.CronConfiguration
	}
	if deployment.Configuration != nil {
		if !deployment.Configuration.Complete {
			renderDeploymentConfiguration(deployment)
		}
		return
	}
	if s := sessionFromContext(ctx); s != nil {
		if j := s.record.CronJournal; j != nil && j.Managed && j.Deferred && j.Status == "prepared" {
			if s.record.CronConfiguration != nil {
				PrintProgress(osStdout, "Configuration incomplete: %s", s.record.CronConfiguration.Crons.Message)
				return
			}
			PrintProgress(osStdout, "Cron changes pending: the server will activate them when this release owns all production traffic.")
		}
	}
}
