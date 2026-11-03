package realtime

import (
	"context"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// emptyChannelHistory serves every channel as empty.
type emptyChannelHistory struct{}

func (emptyChannelHistory) ReadChannelHistory(context.Context, string, string, int64, int) (state.ManagedRealtimeChannelHistory, error) {
	return state.ManagedRealtimeChannelHistory{OldestSequence: 1}, nil
}

// claimsJWTAuthorizer returns a verified identity with an org claim and
// records the claims the policy asked for.
type claimsJWTAuthorizer struct {
	mu      sync.Mutex
	extract []string
}

func (a *claimsJWTAuthorizer) Authorize(context.Context, string, AuthPolicy) (string, error) {
	return "user-123", nil
}

func (a *claimsJWTAuthorizer) AuthorizeIdentity(_ context.Context, _ string, policy AuthPolicy) (JWTIdentity, error) {
	a.mu.Lock()
	a.extract = append([]string(nil), policy.ExtractClaims...)
	a.mu.Unlock()
	return JWTIdentity{Principal: "user-123", SessionID: "session-1", ExpiresAt: time.Now().Add(time.Hour),
		Claims: map[string]string{"org_id": "org-7"}}, nil
}

// adr: 20261019102657 — a channel a rule grants subscribes without the
// authorize callback; any other channel still asks it.
func TestChannelRulesAuthorizeWithoutCallback(t *testing.T) {
	hooks := &testChannelHooks{allowed: false}
	jwt := &claimsJWTAuthorizer{}
	m := NewManager(Config{JWTAuthorizer: jwt, ResumePreview: true, HistoryReader: emptyChannelHistory{},
		ResumePollInterval: 15 * time.Millisecond}, hooks)
	if err := m.RegisterEndpoint(Endpoint{Preview: true, ID: "resume-endpoint", AppID: "app-1", AccountID: "acct-1",
		CallbackURL: "https://app.example", AllowedOrigins: []string{"https://app.example"},
		ClientAuth: AuthPolicy{Mode: AuthModeOIDCJWT, Issuer: "https://issuer.example",
			JWKSURL: "https://issuer.example/.well-known/jwks.json", Algorithms: []string{"RS256"}},
		ChannelRules: []api.RealtimeChannelRule{{Pattern: "org:{org_id}:room:*", Read: true}, {Pattern: "user:{sub}", Read: true}},
	}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(m.Handler())
	t.Cleanup(func() { server.Close(); _ = m.Close() })
	client := dialResumeTest(t, server)
	// verdict reads past presence and other frames to the channel's subscribe answer.
	verdict := func(channel string) resumeServerFrame {
		t.Helper()
		for {
			frame := readResumeTestFrame(t, client)
			if frame.Channel == channel && (frame.Type == "subscribed" || frame.Type == "error") {
				return frame
			}
		}
	}

	for _, channel := range []string{"org:org-7:room:lobby", "user:user-123"} {
		sendResumeTestFrame(t, client, resumeClientFrame{Type: "subscribe", Channel: channel})
		if frame := verdict(channel); frame.Type != "subscribed" {
			t.Fatalf("subscribe %s = %+v, want subscribed by rule", channel, frame)
		}
	}
	hooks.mu.Lock()
	asked := len(hooks.events)
	hooks.mu.Unlock()
	if asked != 0 {
		t.Fatalf("authorize callback ran %d times for rule-granted channels", asked)
	}
	jwt.mu.Lock()
	extract := jwt.extract
	jwt.mu.Unlock()
	if !slices.Equal(extract, []string{"org_id"}) {
		t.Fatalf("extracted claims = %v, want the rules' org_id", extract)
	}

	sendResumeTestFrame(t, client, resumeClientFrame{Type: "subscribe", Channel: "org:org-8:room:lobby"})
	if frame := verdict("org:org-8:room:lobby"); frame.Type != "error" || frame.Code != "not_authorized" {
		t.Fatalf("another org's channel = %+v, want the callback's denial", frame)
	}
	hooks.mu.Lock()
	asked = len(hooks.events)
	hooks.mu.Unlock()
	if asked != 1 {
		t.Fatalf("authorize callback ran %d times, want once for the unmatched channel", asked)
	}
}

func TestChannelRulesAllowEdgeCases(t *testing.T) {
	endpoint := &Endpoint{ChannelRules: []api.RealtimeChannelRule{{Pattern: "org:{org_id}:*", Read: true}, {Pattern: "act*", Read: true}}}
	activity, err := api.RealtimeActivityScopeChannel("org:o1:room", "typing")
	if err != nil {
		t.Fatal(err)
	}
	identity := JWTIdentity{Principal: "user-1", Claims: map[string]string{"org_id": "o1"}}
	for _, tc := range []struct {
		name     string
		endpoint *Endpoint
		identity JWTIdentity
		channel  string
		want     bool
	}{
		{"match", endpoint, identity, "org:o1:room", true},
		{"other org", endpoint, identity, "org:o2:room", false},
		{"no rules", &Endpoint{}, identity, "org:o1:room", false},
		{"no principal", endpoint, JWTIdentity{Claims: identity.Claims}, "org:o1:room", false},
		{"activity scope uses the callback", endpoint, identity, activity, false},
	} {
		if got := channelRulesAllow(tc.endpoint, tc.identity, tc.channel, false); got != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
	// adr: 369 — a read rule does not grant presence state or signals.
	presence := &Endpoint{ChannelRules: []api.RealtimeChannelRule{{Pattern: "org:{org_id}:room:*", Read: true, Presence: true}, {Pattern: "org:{org_id}:feed", Read: true}}}
	for _, tc := range []struct {
		channel        string
		read, presence bool
	}{
		{"org:o1:room:lobby", true, true},
		{"org:o1:feed", true, false},
		{"org:o2:room:lobby", false, false},
	} {
		if got := channelRulesAllow(presence, identity, tc.channel, false); got != tc.read {
			t.Errorf("%s read: %v, want %v", tc.channel, got, tc.read)
		}
		if got := channelRulesAllow(presence, identity, tc.channel, true); got != tc.presence {
			t.Errorf("%s presence: %v, want %v", tc.channel, got, tc.presence)
		}
	}
	if got := channelRuleClaims([]api.RealtimeChannelRule{{Pattern: "org:{org_id}:{sub}"}, {Pattern: "t:{org_id}:{team}"}}); !slices.Equal(got, []string{"org_id", "team"}) {
		t.Fatalf("claims = %v", got)
	}
}
