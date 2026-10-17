package issueslack

import (
	"net/url"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func ApplicationPolicyForm(form url.Values) (state.PutIssueApplicationSlackPolicy, error) {
	revision, err := strconv.ParseInt(form.Get("expected_revision"), 10, 64)
	if err != nil || revision < 0 {
		return state.PutIssueApplicationSlackPolicy{}, api.ErrValidation("reload application Slack settings before saving")
	}
	return state.PutIssueApplicationSlackPolicy{Revision: revision, Enabled: form.Get("enabled") == "1", FallbackTeamID: form.Get("fallback_team_id"), Events: append([]string{}, form["events"]...)}, nil
}
