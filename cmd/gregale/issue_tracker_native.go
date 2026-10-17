package main

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/issuetracker"
)

type nativeIssueStatusProvider struct {
	client           *Client
	app, issue, link string
}

func nativeRemote(s api.IssueTrackerProviderStatus) issuetracker.Status {
	return issuetracker.Status{Provider: s.Provider, URL: s.URL, ID: s.ID, State: s.State, TeamID: s.TeamID, StateID: s.StateID}
}
func (p nativeIssueStatusProvider) ReadStatus(ctx context.Context, url, _ string) (issuetracker.Status, error) {
	out, err := p.client.GetNativeIssueTrackerStatus(ctx, p.app, p.issue, p.link)
	if err != nil {
		return issuetracker.Status{}, err
	}
	if out.IssueID != p.issue || out.LinkID != p.link || out.Status.URL != url || out.Status.Provider != "github" {
		return issuetracker.Status{}, errors.New("native status returned an inconsistent linked issue")
	}
	return nativeRemote(out.Status), nil
}
func (p nativeIssueStatusProvider) SetStatus(ctx context.Context, observed issuetracker.Status, _ string, wanted, linearState string) (issuetracker.Status, error) {
	if linearState != "" {
		return issuetracker.Status{}, errors.New("native GitHub sync does not accept Linear state selection")
	}
	out, err := p.client.UpdateNativeIssueTrackerStatus(ctx, p.app, p.issue, p.link, api.UpdateNativeIssueTrackerStatusRequest{DesiredState: wanted, Expected: api.IssueTrackerProviderStatus{Provider: observed.Provider, URL: observed.URL, ID: observed.ID, State: observed.State, TeamID: observed.TeamID, StateID: observed.StateID}})
	if err != nil {
		return issuetracker.Status{}, err
	}
	if out.IssueID != p.issue || out.LinkID != p.link || out.Status.URL != observed.URL || out.Status.Provider != "github" {
		return issuetracker.Status{}, errors.New("native update returned an inconsistent linked issue")
	}
	return nativeRemote(out.Status), nil
}
