package dashboard

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/issuetriage"
	"github.com/onebox-faas/faas/pkg/state"
)

type IssueApplicationResponseInboxView struct {
	AppSlug, CSRF, NextURL, SnoozeDefaultUntil, SnoozeMaxUntil string
	Filter                                                     state.IssueApplicationResponseFilter
	Inbox                                                      state.IssueApplicationResponseInbox
}

func NewIssueApplicationResponseInboxView(slug, csrf string, filter state.IssueApplicationResponseFilter, inbox state.IssueApplicationResponseInbox) IssueApplicationResponseInboxView {
	now := time.Now().UTC()
	v := IssueApplicationResponseInboxView{AppSlug: slug, CSRF: csrf, Filter: filter, Inbox: inbox, SnoozeDefaultUntil: now.Add(time.Hour).Format("2006-01-02T15:04"), SnoozeMaxUntil: now.Add(api.IssueApplicationSnoozeMax).Format("2006-01-02T15:04")}
	if inbox.NextID != "" {
		filter.AfterID = inbox.NextID
		v.NextURL = issuetriage.InboxURL(slug, filter)
	}
	return v
}
