package issueslack

import (
	"context"
	"net/url"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func RoutingForm(form url.Values) (state.PutIssueSlackRoutingPolicy, state.IssueSlackRoutingSample, error) {
	in := state.PutIssueSlackRoutingPolicy{Rules: []state.IssueSlackRoutingRule{}}
	sample := state.IssueSlackRoutingSample{Event: form.Get("sample_event"), Environment: form.Get("sample_environment"), FrequencyRuleKey: form.Get("sample_frequency_key"), AssignedTeamID: form.Get("sample_team_id")}
	var err error
	in.Revision, err = strconv.ParseInt(form.Get("expected_revision"), 10, 64)
	if err != nil || in.Revision < 0 {
		return in, sample, api.ErrValidation("reload Slack channel rules")
	}
	in.InstallationRevision, err = strconv.ParseInt(form.Get("installation_revision"), 10, 64)
	if err != nil || in.InstallationRevision <= 0 {
		return in, sample, api.ErrValidation("connect Slack and reload channel rules")
	}
	keys := form["rule_key"]
	if len(keys) > api.IssueSlackRoutingMaxRules+1 {
		return in, sample, api.ErrValidation("too many Slack channel rules")
	}
	for _, field := range []string{"rule_enabled", "rule_event", "rule_environment", "rule_frequency_key", "rule_team_id", "rule_channel_id"} {
		if len(form[field]) != len(keys) {
			return in, sample, api.ErrValidation("incomplete Slack channel rule form")
		}
	}
	for i, key := range keys {
		enabled := form["rule_enabled"][i]
		if enabled == "remove" {
			continue
		}
		if enabled != "0" && enabled != "1" {
			return in, sample, api.ErrValidation("invalid rule status")
		}
		r := state.IssueSlackRoutingRule{Key: key, Enabled: enabled == "1", Event: form["rule_event"][i], Environment: form["rule_environment"][i], FrequencyRuleKey: form["rule_frequency_key"][i], TeamID: form["rule_team_id"][i], ChannelID: form["rule_channel_id"][i]}
		if r.Key == "" && r.Environment == "" && r.FrequencyRuleKey == "" && r.TeamID == "" && r.ChannelID == "" {
			continue
		}
		in.Rules = append(in.Rules, r)
	}
	in.Rules, err = state.NormalizeIssueSlackRoutingRules(in.Rules)
	if err != nil {
		return in, sample, api.ErrValidation(err.Error())
	}
	if move := form.Get("rule_move"); move != "" {
		in.Rules, err = moveSlackRule(in.Rules, move)
	}
	return in, sample, err
}
func moveSlackRule(in []state.IssueSlackRoutingRule, move string) ([]state.IssueSlackRoutingRule, error) {
	direction, key, ok := strings.Cut(move, ":")
	if !ok || (direction != "up" && direction != "down") {
		return nil, api.ErrValidation("invalid rule order")
	}
	for i, r := range in {
		if r.Key == key {
			j := i - 1
			if direction == "down" {
				j = i + 1
			}
			if j >= 0 && j < len(in) {
				in[i], in[j] = in[j], in[i]
			}
			return in, nil
		}
	}
	return nil, api.ErrValidation("reload rule order")
}
func (s OAuthService) routingCredential(ctx context.Context, app, owner string, in state.PutIssueSlackRoutingPolicy) (string, state.IssueSlackRoutingStore, error) {
	st, ok := s.Store.(state.IssueSlackRoutingStore)
	if !ok {
		return "", nil, api.ErrCapacity("Slack channel rules unavailable")
	}
	install, err := s.Store.IssueSlackInstallationCredential(ctx, app, owner)
	if err != nil {
		return "", st, err
	}
	policy, err := st.GetIssueSlackRoutingPolicy(ctx, app, owner)
	if err != nil {
		return "", st, err
	}
	if install.Status != "connected" || install.Revision != in.InstallationRevision || policy.Revision != in.Revision {
		return "", st, api.NewProblem(409, "issue_slack_routing_changed", "Slack channel rules changed", "reload channel rules and review the current workspace")
	}
	token, err := OpenToken(s.Identities, install.TokenSealed)
	return token, st, err
}
func (s OAuthService) SetRouting(ctx context.Context, app, owner string, in state.PutIssueSlackRoutingPolicy) (state.IssueSlackRoutingPolicy, error) {
	empty := state.IssueSlackRoutingPolicy{}
	rules, err := state.NormalizeIssueSlackRoutingRules(in.Rules)
	if err != nil {
		return empty, api.ErrValidation(err.Error())
	}
	in.Rules = rules
	token, st, err := s.routingCredential(ctx, app, owner, in)
	if err != nil {
		return empty, err
	}
	checked := map[string]bool{}
	for _, r := range rules {
		if r.Enabled && !checked[r.ChannelID] {
			if err = s.Client.VerifyOAuthChannel(ctx, token, r.ChannelID); err != nil {
				return empty, api.ErrValidation("choose active channels the connected Slack bot has joined")
			}
			checked[r.ChannelID] = true
		}
	}
	return st.SetIssueSlackRoutingPolicy(ctx, app, owner, in)
}
func (s OAuthService) PreviewRouting(ctx context.Context, app, owner string, in state.PutIssueSlackRoutingPolicy, sample state.IssueSlackRoutingSample) (state.IssueSlackRoutingPreview, error) {
	out := state.IssueSlackRoutingPreview{}
	token, st, err := s.routingCredential(ctx, app, owner, in)
	if err != nil {
		return out, err
	}
	out, err = st.PreviewIssueSlackRouting(ctx, app, owner, in, sample)
	if err != nil {
		return out, err
	}
	if out.ChannelID != "" {
		if err = s.Client.VerifyOAuthChannel(ctx, token, out.ChannelID); err != nil {
			return out, api.ErrValidation("the preview channel is unavailable or the Slack bot has not joined it")
		}
	}
	return out, nil
}
