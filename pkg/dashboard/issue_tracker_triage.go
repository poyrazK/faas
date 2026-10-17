package dashboard

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"time"
)

type IssueTrackerTriageView struct {
	AppSlug, CSRF                      string
	Triage                             state.IssueTrackerTriage
	CanRelease                         bool
	SnoozeDefaultUntil, SnoozeMaxUntil string
	SnoozeReasonMaxBytes               int
}

func NewIssueTrackerTriageView(slug, csrf string, triage state.IssueTrackerTriage, release bool) IssueTrackerTriageView {
	now := time.Now().UTC()
	until := now.Add(time.Hour)
	if triage.Snoozed && triage.SnoozedUntil != nil {
		until = *triage.SnoozedUntil
	}
	return IssueTrackerTriageView{AppSlug: slug, CSRF: csrf, Triage: triage, CanRelease: release, SnoozeDefaultUntil: until.UTC().Format("2006-01-02T15:04"), SnoozeMaxUntil: now.Add(api.IssueTrackerSyncSnoozeMax).Format("2006-01-02T15:04"), SnoozeReasonMaxBytes: api.IssueTrackerSyncSnoozeReasonMaxBytes}
}
