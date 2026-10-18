package issuetriage

import (
	"net/url"

	"github.com/onebox-faas/faas/pkg/state"
)

func ParseInboxFilter(values url.Values) (state.IssueApplicationResponseFilter, error) {
	f := state.IssueApplicationResponseFilter{Status: values.Get("status"), AfterID: values.Get("after"), MyTeams: values.Get("team") == "mine"}
	if team := values.Get("team"); team != "" && team != "all" && team != "mine" {
		return f, state.ErrInvalidArgument
	}
	err := f.Validate()
	return f, err
}
func InboxURL(slug string, filter state.IssueApplicationResponseFilter) string {
	values := url.Values{"status": {filter.Status}}
	if filter.MyTeams {
		values.Set("team", "mine")
	}
	if filter.AfterID != "" {
		values.Set("after", filter.AfterID)
	}
	return "/dashboard/apps/" + url.PathEscape(slug) + "/issues/responses?" + values.Encode()
}
