package dashboard

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"time"
)

type IssueApplicationTriageView struct {
	SlackLink                          state.IssueSlackAccountLink
	AppSlug, CSRF                      string
	Triage                             state.IssueApplicationTriage
	IsOwner                            bool
	SnoozeDefaultUntil, SnoozeMaxUntil string
	SnoozeReasonMaxBytes               int
}

func NewIssueApplicationTriageView(slug, csrf string, triage state.IssueApplicationTriage, owner bool) IssueApplicationTriageView {
	now := time.Now().UTC()
	until := now.Add(time.Hour)
	if triage.Snoozed && triage.SnoozedUntil != nil {
		until = *triage.SnoozedUntil
	}
	return IssueApplicationTriageView{AppSlug: slug, CSRF: csrf, Triage: triage, IsOwner: owner, SnoozeDefaultUntil: until.UTC().Format("2006-01-02T15:04"), SnoozeMaxUntil: now.Add(api.IssueApplicationSnoozeMax).Format("2006-01-02T15:04"), SnoozeReasonMaxBytes: api.IssueApplicationSnoozeReasonMaxBytes}
}
