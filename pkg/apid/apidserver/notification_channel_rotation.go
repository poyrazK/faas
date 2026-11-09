package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/alertchannels"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// pagerDutyKeyChangeAllowed refuses replacing a PagerDuty routing key while
// an enabled rule using the channel is firing (ADR-20261109153000). The
// incident the fire opened is keyed to the old routing key's service; the
// resolve, sent to the new key, would leave it open for good. Slack and
// email keep no incident, so their destinations change freely.
func (s *server) pagerDutyKeyChangeAllowed(r *http.Request, acct state.Account, current state.NotificationChannel, p state.UpdateNotificationChannelParams) *api.Problem {
	if current.Kind != alertchannels.KindPagerDuty || p.TargetSealed == nil {
		return nil
	}
	firing, err := s.firingRulesOnChannel(r, acct, current.ID)
	if err != nil {
		return api.ErrCapacity("could not check the channel's open incidents")
	}
	if len(firing) > 0 {
		return api.ErrPagerDutyKeyHasOpenIncidents(firing)
	}
	return nil
}

// firingRulesOnChannel names the account's enabled, firing rules that
// deliver to channelID.
func (s *server) firingRulesOnChannel(r *http.Request, acct state.Account, channelID string) ([]string, error) {
	bindings, ok := s.store.(state.AlertRuleChannelStore)
	if !ok {
		return nil, nil
	}
	rules, err := s.store.ListAlertRulesForAccount(r.Context(), acct.ID)
	if err != nil {
		return nil, err
	}
	var firing []string
	for _, rule := range rules {
		if !rule.Enabled || rule.State != state.AlertStateFiring {
			continue
		}
		chans, err := bindings.ListAlertRuleChannels(r.Context(), rule.ID)
		if err != nil {
			return nil, err
		}
		for _, ch := range chans {
			if ch.ID == channelID {
				firing = append(firing, rule.Name+" ("+rule.ID+")")
				break
			}
		}
	}
	return firing, nil
}
