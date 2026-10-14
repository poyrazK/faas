package main

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/issuetracker"
)

type nativeLinearStatusProvider struct {
	client           *Client
	app, issue, link string
	snapshot         *api.NativeLinearIssueTrackerStatus
}

func (p *nativeLinearStatusProvider) ReadStatus(ctx context.Context, url, _ string) (issuetracker.Status, error) {
	out, err := p.client.GetNativeLinearIssueTrackerStatus(ctx, p.app, p.issue, p.link)
	if err != nil {
		return issuetracker.Status{}, err
	}
	if out.IssueID != p.issue || out.LinkID != p.link || out.Status.URL != url || out.Status.Provider != "linear" || out.TeamID != out.Status.TeamID || out.ConnectionRevision <= 0 || out.GroupFingerprint == "" {
		return issuetracker.Status{}, errors.New("native Linear status returned inconsistent evidence")
	}
	if p.snapshot != nil && (p.snapshot.ConnectionRevision != out.ConnectionRevision || p.snapshot.GroupFingerprint != out.GroupFingerprint || p.snapshot.WorkspaceID != out.WorkspaceID || p.snapshot.TeamID != out.TeamID || p.snapshot.ViewerID != out.ViewerID) {
		return issuetracker.Status{}, issuetracker.ErrStatusChanged
	}
	p.snapshot = &out
	return nativeRemote(out.Status), nil
}
func (p *nativeLinearStatusProvider) SetStatus(ctx context.Context, observed issuetracker.Status, _ string, wanted, linearState string) (issuetracker.Status, error) {
	if p.snapshot == nil || nativeRemote(p.snapshot.Status) != observed {
		return issuetracker.Status{}, issuetracker.ErrStatusChanged
	}
	before := *p.snapshot
	out, err := p.client.UpdateNativeLinearIssueTrackerStatus(ctx, p.app, p.issue, p.link, api.UpdateNativeLinearIssueTrackerStatusRequest{DesiredState: wanted, Expected: before.Status, ExpectedConnectionRevision: before.ConnectionRevision, ExpectedGroupFingerprint: before.GroupFingerprint, LinearStateID: linearState})
	if err != nil {
		return issuetracker.Status{}, err
	}
	if out.IssueID != p.issue || out.LinkID != p.link || out.Status.URL != observed.URL || out.Status.Provider != "linear" || out.ConnectionRevision != before.ConnectionRevision || out.GroupFingerprint != before.GroupFingerprint || out.WorkspaceID != before.WorkspaceID || out.TeamID != before.TeamID || out.ViewerID != before.ViewerID {
		return issuetracker.Status{}, errors.New("native Linear update returned inconsistent evidence")
	}
	return nativeRemote(out.Status), nil
}
