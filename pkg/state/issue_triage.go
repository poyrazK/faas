package state

import (
	"context"
	"github.com/onebox-faas/faas/pkg/api"
	"time"
)

type IssueTriageStore interface {
	CompareIssueReleases(context.Context, string, string, string, string, string, time.Time) (api.IssueReleaseComparison, error)
	BulkActOnIssues(context.Context, string, string, api.BulkIssueActionRequest, time.Time) (api.BulkIssueActionResponse, error)
}
