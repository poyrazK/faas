package main

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

type deploymentConfigurationClient interface {
	GetDeployment(context.Context, string) (api.DeploymentResponse, error)
}

func (s *savedDeploySession) readCronConfiguration(ctx context.Context, c deploymentConfigurationClient) (bool, error) {
	if s.record.DeploymentID == "" {
		return false, nil
	}
	d, err := c.GetDeployment(ctx, s.record.DeploymentID)
	if err != nil {
		return true, fmt.Errorf("read deployment configuration status: %w", err)
	}
	if d.Configuration == nil {
		return false, nil // APID predates ADR-20261019120054; preserve existing recovery.
	}
	if d.ID != s.record.DeploymentID || d.AppID != s.record.AppID {
		return true, fmt.Errorf("configuration status belongs to a different deployment or app")
	}
	if d.Configuration.Crons.SessionID != "" && d.Configuration.Crons.SessionID != s.record.ID {
		prior := s.record.CronConfiguration
		if prior != nil && prior.Crons.SessionID == s.record.ID && (prior.Crons.Status == "conflicted" || prior.Crons.Status == "discarded") {
			d.Configuration = prior
		} else {
			configuration := api.NewDeploymentConfigurationStatus("discarded", "intent_superseded")
			configuration.Crons.SessionID = s.record.ID
			d.Configuration = &configuration
		}
	}
	status := d.Configuration.Crons.Status
	legacyStatus := map[string]string{"pending": "prepared", "activated": "activated", "conflicted": "conflict", "discarded": "discarded"}[status]
	if legacyStatus == "" {
		return true, fmt.Errorf("saved deploy has no retained cron activation outcome; inspect current crons and deployment history")
	}
	if d.Configuration.Complete != (status == "activated") {
		return true, fmt.Errorf("deployment configuration status has an inconsistent completion flag")
	}
	configuration := *d.Configuration
	s.record.CronConfiguration = &configuration
	s.record.CronJournal.Status = legacyStatus
	if err := s.save(); err != nil {
		return true, err
	}
	if !d.Configuration.Complete && status != "pending" {
		return true, fmt.Errorf("%s %s", d.Configuration.Crons.Message, d.Configuration.Crons.Hint)
	}
	return true, nil
}

func renderDeploymentConfiguration(d api.DeploymentResponse) {
	if d.Configuration == nil {
		return
	}
	c := d.Configuration
	label := "complete"
	if !c.Complete {
		label = "incomplete"
		if d.Status == statusLive {
			label = "release live, configuration incomplete"
		}
	}
	_, _ = fmt.Fprintf(osStdout, "%-14s %s\n", "configuration:", label)
	_, _ = fmt.Fprintf(osStdout, "%-14s %s\n", "crons:", c.Crons.Status)
	_, _ = fmt.Fprintln(osStdout, c.Crons.Message)
	if c.Crons.Hint != "" {
		_, _ = fmt.Fprintln(osStdout, c.Crons.Hint)
	}
}
