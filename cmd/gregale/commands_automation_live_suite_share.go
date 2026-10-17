package main

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// The journal fingerprint includes its creation time and ordered selection.
// Namespacing it gives every restored copy the same opaque shared-record ID.
func sharedAutomationLiveSuiteID(j automationLiveSuiteJournal) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("gregale-live-suite-record-v1:"+j.Fingerprint)).String()
}

func syncAutomationLiveSuite(ctx context.Context, c *api.Client, j automationLiveSuiteJournal) error {
	if !j.Plan.Shared {
		return nil
	}
	body := api.SaveAutomationLiveSuiteRequest{ExpectedVersion: j.Plan.Target.ExpectedVersion, ExpectedPublishedVersion: j.Plan.Target.ExpectedPublishedVersion, ExpectedDeploymentID: j.Plan.Target.ExpectedDeploymentID, Cases: []api.AutomationLiveSuiteMember{}}
	for _, entry := range j.Cases {
		body.Cases = append(body.Cases, api.AutomationLiveSuiteMember{Name: entry.Name, RunID: entry.RunID})
	}
	id := sharedAutomationLiveSuiteID(j)
	requestCtx, cancel := context.WithTimeout(ctx, api.AutomationLiveProgressRequestTimeout)
	defer cancel()
	view, err := c.SaveAutomationLiveSuite(requestCtx, j.Plan.App, j.Plan.Name, id, body)
	if err != nil {
		return err
	}
	if view.ID != id || view.Name != j.Plan.Name || view.Version != body.ExpectedVersion || view.PublishedVersion != body.ExpectedPublishedVersion || !sameLiveSuiteDeployment(view.DeploymentID, body.ExpectedDeploymentID) || len(view.Cases) != len(body.Cases) {
		return errors.New("invalid shared suite response")
	}
	for i, entry := range body.Cases {
		if view.Cases[i].Name != entry.Name || entry.RunID != "" && view.Cases[i].RunID != entry.RunID {
			return errors.New("shared suite run identity changed")
		}
	}
	return nil
}

func sameLiveSuiteDeployment(actual, expected string) bool {
	a, err := uuid.Parse(actual)
	if err != nil {
		return false
	}
	b, err := uuid.Parse(expected)
	return err == nil && a == b
}
