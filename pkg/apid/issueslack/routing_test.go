package issueslack

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestSlackRoutingPriorityAndValidation(t *testing.T) {
	specific := state.IssueSlackRoutingRule{Key: "specific", Enabled: true, Event: "frequency_threshold_reached", Environment: "production", FrequencyRuleKey: "burst", ChannelID: "C0123456789"}
	generic := state.IssueSlackRoutingRule{Key: "all-frequency", Enabled: true, Event: "frequency_threshold_reached", ChannelID: "C1111111111"}
	for _, tc := range []struct {
		name, event, env, freq, team, key string
		rules                             []state.IssueSlackRoutingRule
	}{{"specific", "frequency_threshold_reached", "production", "burst", "", "specific", []state.IssueSlackRoutingRule{specific, generic}}, {"other_key", "frequency_threshold_reached", "production", "other", "", "all-frequency", []state.IssueSlackRoutingRule{specific, generic}}, {"other_environment", "frequency_threshold_reached", "staging", "burst", "", "all-frequency", []state.IssueSlackRoutingRule{specific, generic}}, {"priority", "frequency_threshold_reached", "production", "burst", "", "all-frequency", []state.IssueSlackRoutingRule{generic, specific}}, {"other_event", "regressed", "production", "", "", "", []state.IssueSlackRoutingRule{specific, generic}}} {
		t.Run(tc.name, func(t *testing.T) {
			key, _ := state.MatchIssueSlackRouting(tc.rules, tc.event, tc.env, tc.freq, tc.team)
			if key != tc.key {
				t.Fatal(key)
			}
		})
	}
	team := specific
	team.TeamID = "11111111-1111-4111-8111-111111111111"
	if key, _ := state.MatchIssueSlackRouting([]state.IssueSlackRoutingRule{team}, team.Event, team.Environment, "burst", ""); key != "" {
		t.Fatal("team filter bypassed")
	}
	team.Enabled = false
	if key, _ := state.MatchIssueSlackRouting([]state.IssueSlackRoutingRule{team}, team.Event, team.Environment, "burst", team.TeamID); key != "" {
		t.Fatal("disabled rule matched")
	}
	for _, tc := range []struct {
		name   string
		change func(*state.IssueSlackRoutingRule)
	}{{"key", func(r *state.IssueSlackRoutingRule) { r.Key = "<script>" }}, {"event", func(r *state.IssueSlackRoutingRule) { r.Event = "application_reminder" }}, {"env", func(r *state.IssueSlackRoutingRule) { r.Environment = "production*" }}, {"channel", func(r *state.IssueSlackRoutingRule) { r.ChannelID = "https://evil.example" }}, {"frequency", func(r *state.IssueSlackRoutingRule) { r.Event = "created" }}, {"team", func(r *state.IssueSlackRoutingRule) { r.TeamID = "bad" }}} {
		t.Run(tc.name, func(t *testing.T) {
			r := specific
			tc.change(&r)
			if _, e := state.NormalizeIssueSlackRoutingRules([]state.IssueSlackRoutingRule{r}); e == nil {
				t.Fatal("invalid rule accepted")
			}
		})
	}
	if _, e := state.NormalizeIssueSlackRoutingRules([]state.IssueSlackRoutingRule{specific, specific}); e == nil {
		t.Fatal("duplicate keys")
	}
	rules := []state.IssueSlackRoutingRule{}
	for i := 0; i <= api.IssueSlackRoutingMaxRules; i++ {
		r := specific
		r.Key = "rule-" + strconv.Itoa(i)
		rules = append(rules, r)
	}
	if _, e := state.NormalizeIssueSlackRoutingRules(rules); e == nil {
		t.Fatal("rule cap bypassed")
	}
}
func routingTestForm() url.Values {
	return url.Values{"expected_revision": {"3"}, "installation_revision": {"1"}, "rule_key": {"first", "second", ""}, "rule_enabled": {"1", "0", "1"}, "rule_event": {"created", "regressed", "regressed"}, "rule_environment": {"production", "", ""}, "rule_frequency_key": {"", "", ""}, "rule_team_id": {"", "", ""}, "rule_channel_id": {"C0123456789", "C1111111111", ""}, "sample_event": {"created"}, "sample_environment": {"production"}}
}
func TestSlackRoutingFormsKeepPriorityAndDrafts(t *testing.T) {
	f := routingTestForm()
	in, sample, e := RoutingForm(f)
	if e != nil || len(in.Rules) != 2 || in.Rules[0].Key != "first" || in.Rules[1].Enabled || sample.Environment != "production" {
		t.Fatal(in, sample, e)
	}
	f.Set("rule_move", "up:second")
	in, _, e = RoutingForm(f)
	if e != nil || in.Rules[0].Key != "second" {
		t.Fatal(in, e)
	}
	f = routingTestForm()
	f["rule_enabled"][0] = "remove"
	in, _, e = RoutingForm(f)
	if e != nil || len(in.Rules) != 1 || in.Rules[0].Key != "second" {
		t.Fatal(in, e)
	}
	f = routingTestForm()
	f.Del("rule_event")
	if _, _, e = RoutingForm(f); e == nil {
		t.Fatal("misaligned fields accepted")
	}
	f = routingTestForm()
	f["rule_key"][2] = "partial"
	if _, _, e = RoutingForm(f); e == nil {
		t.Fatal("incomplete new rule accepted")
	}
	f = routingTestForm()
	f.Set("expected_revision", "-1")
	if _, _, e = RoutingForm(f); e == nil {
		t.Fatal("negative revision")
	}
}

type routingServiceStore struct {
	state.IssueSlackOAuthStore
	state.IssueSlackRoutingStore
	installation state.IssueSlackInstallation
	policy       state.IssueSlackRoutingPolicy
	saved        int
}

func (s *routingServiceStore) IssueSlackInstallationCredential(context.Context, string, string) (state.IssueSlackInstallation, error) {
	return s.installation, nil
}
func (s *routingServiceStore) GetIssueSlackRoutingPolicy(context.Context, string, string) (state.IssueSlackRoutingPolicy, error) {
	return s.policy, nil
}
func (s *routingServiceStore) SetIssueSlackRoutingPolicy(_ context.Context, _, _ string, in state.PutIssueSlackRoutingPolicy) (state.IssueSlackRoutingPolicy, error) {
	s.saved++
	return state.IssueSlackRoutingPolicy{Rules: in.Rules, Revision: in.Revision + 1}, nil
}
func TestSlackRoutingServiceVerifiesDistinctEnabledChannels(t *testing.T) {
	id, e := age.GenerateX25519Identity()
	if e != nil {
		t.Fatal(e)
	}
	sealed, e := secretbox.SealBytes(id.Recipient(), state.IssueSlackTokenNamespace, []byte("xoxb-safe-token"), api.IssueSlackTokenMaxBytes)
	if e != nil {
		t.Fatal(e)
	}
	st := &routingServiceStore{installation: state.IssueSlackInstallation{Status: "connected", Revision: 1, TokenSealed: sealed}, policy: state.IssueSlackRoutingPolicy{Revision: 3}}
	calls := 0
	allow := true
	client := NewClient(&http.Client{Transport: oauthTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != "https://slack.com/api/conversations.info" || r.Header.Get("Authorization") != "Bearer xoxb-safe-token" {
			t.Fatal("unsafe verification")
		}
		_ = r.ParseForm()
		body := `{"ok":false,"error":"private-response"}`
		if allow {
			body = `{"ok":true,"channel":{"id":"` + r.PostForm.Get("channel") + `","is_member":true}}`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})})
	service := OAuthService{Store: st, Client: client, Identities: []*age.X25519Identity{id}}
	in, _, e := RoutingForm(routingTestForm())
	if e != nil {
		t.Fatal(e)
	}
	in.Rules[1].Enabled = true
	duplicate := in.Rules[0]
	duplicate.Key = "third"
	in.Rules = append(in.Rules, duplicate)
	if _, e = service.SetRouting(context.Background(), "app", "owner", in); e != nil || calls != 2 || st.saved != 1 {
		t.Fatal(calls, st.saved, e)
	}
	in.Revision = 2
	if _, e = service.SetRouting(context.Background(), "app", "owner", in); e == nil || calls != 2 {
		t.Fatal("stale verification", e)
	}
	in.Revision = 3
	allow = false
	if _, e = service.SetRouting(context.Background(), "app", "owner", in); e == nil || strings.Contains(e.Error(), "private-response") || st.saved != 1 {
		t.Fatal("unjoined channel saved", e)
	}
}
