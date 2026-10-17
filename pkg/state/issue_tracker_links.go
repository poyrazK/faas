package state

import (
	"context"
	"github.com/onebox-faas/faas/pkg/api"
	"time"
)

type IssueTrackerLinkStore interface {
	ListIssueTrackerLinks(context.Context, string, string, string, IssueCursor) (api.ListIssueTrackerLinksResponse, error)
	PutIssueTrackerLink(context.Context, string, string, string, string, api.PutIssueTrackerLinkRequest, time.Time) (api.IssueTrackerLink, error)
	DeleteIssueTrackerLink(context.Context, string, string, string, string, string, time.Time) error
}
