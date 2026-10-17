package dashboard

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/issueslack"
	"github.com/onebox-faas/faas/pkg/state"
)

type IssueSlackRoutingView struct {
	AppSlug, CSRF                              string
	Policy                                     state.IssueSlackRoutingPolicy
	Installation                               state.IssueSlackInstallation
	Teams                                      []api.IssueTeam
	TeamNames                                  map[string]string
	Channels                                   []issueslack.OAuthChannel
	Sample                                     state.IssueSlackRoutingSample
	Preview                                    *state.IssueSlackRoutingPreview
	MaxRules, KeyMaxBytes, EnvironmentMaxBytes int
	Draft, Saved, ChannelLookupFailed          bool
}
