package realtime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

type sseFakeJWT struct {
	principal string
	err       error
	calls     int
}

func (f *sseFakeJWT) Authorize(context.Context, string, AuthPolicy) (string, error) {
	f.calls++
	return f.principal, f.err
}

type sseFakeChannels struct {
	allow bool
	err   error
	calls int
}

func (f *sseFakeChannels) AuthorizeChannel(context.Context, Event) (bool, error) {
	f.calls++
	return f.allow, f.err
}

type sseFakeAccess struct {
	access state.ManagedRealtimeAccess
	err    error
	calls  int
}

func (f *sseFakeAccess) ReadRealtimeAccess(context.Context, string, string) (state.ManagedRealtimeAccess, error) {
	f.calls++
	return f.access, f.err
}

type sseFakeTickets struct {
	ticket state.RealtimeSubscriptionTicket
	err    error
	calls  int
}

func (f *sseFakeTickets) ReadSubscriptionTicket(context.Context, string, string) (state.RealtimeSubscriptionTicket, error) {
	f.calls++
	return f.ticket, f.err
}

type sseAuthFixture struct {
	jwt      *sseFakeJWT
	channels *sseFakeChannels
	access   *sseFakeAccess
	tickets  *sseFakeTickets
	auth     *sseAuthorization
}

func newSSEAuthFixture(usesTicket bool) *sseAuthFixture {
	f := &sseAuthFixture{
		jwt:      &sseFakeJWT{principal: "user-1"},
		channels: &sseFakeChannels{allow: true},
		access:   &sseFakeAccess{},
		tickets: &sseFakeTickets{ticket: state.RealtimeSubscriptionTicket{
			Principal: "user-1", Channels: []string{"orders"}, ExpiresAt: time.Now().Add(time.Minute)}},
	}
	m := &Manager{cfg: Config{CallbackTimeout: time.Second, JWTAuthorizer: f.jwt, AccessPreview: true,
		AccessReader: f.access, TicketReader: f.tickets}}
	node := &endpointState{}
	node.config.Store(&Endpoint{Preview: true, ClientAuth: AuthPolicy{Mode: AuthModeOIDCJWT}})
	f.auth = &sseAuthorization{m: m, node: node, authorizer: f.channels, endpointID: "ep", channel: "orders",
		principal: "user-1", token: "token", connectionID: "sse_test", usesTicket: usesTicket,
		issued: time.Now().Add(-time.Minute), ticketExpiry: time.Now().Add(time.Minute)}
	return f
}

func TestSSEAuthorizationVerdicts(t *testing.T) {
	revokedAt := time.Now()
	cases := []struct {
		name   string
		ticket bool
		setup  func(*sseAuthFixture)
		want   sseVerdict
	}{
		{name: "oidc allowed", want: sseAllowed},
		{name: "jwt verification error reconnects", setup: func(f *sseAuthFixture) { f.jwt.err = errors.New("jwks timeout") }, want: sseUnavailable},
		{name: "principal changed", setup: func(f *sseAuthFixture) { f.jwt.principal = "user-2" }, want: sseDenied},
		{name: "channel callback error reconnects", setup: func(f *sseAuthFixture) { f.channels.err = errors.New("callback timeout") }, want: sseUnavailable},
		{name: "channel callback deny", setup: func(f *sseAuthFixture) { f.channels.allow = false }, want: sseDenied},
		{name: "access read error reconnects", setup: func(f *sseAuthFixture) { f.access.err = errors.New("rpc unavailable") }, want: sseUnavailable},
		{name: "channel denied", setup: func(f *sseAuthFixture) { f.access.access.DeniedChannels = []string{"orders"} }, want: sseDenied},
		{name: "session revoked", setup: func(f *sseAuthFixture) { f.access.access.SessionsRevokedBefore = &revokedAt }, want: sseDenied},
		{name: "endpoint removed", setup: func(f *sseAuthFixture) { f.auth.node.revoked.Store(true) }, want: sseUnavailable},
		{name: "ticket allowed", ticket: true, want: sseAllowed},
		{name: "ticket read error reconnects", ticket: true, setup: func(f *sseAuthFixture) { f.tickets.err = errors.New("rpc unavailable") }, want: sseUnavailable},
		{name: "ticket channel removed", ticket: true, setup: func(f *sseAuthFixture) { f.tickets.ticket.Channels = []string{"other"} }, want: sseDenied},
		{name: "ticket principal changed", ticket: true, setup: func(f *sseAuthFixture) { f.tickets.ticket.Principal = "user-2" }, want: sseDenied},
		{name: "ticket expired", ticket: true, setup: func(f *sseAuthFixture) { f.auth.ticketExpiry = time.Now().Add(-time.Second) }, want: sseUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newSSEAuthFixture(tc.ticket)
			if tc.setup != nil {
				tc.setup(f)
			}
			if got := f.auth.check(context.Background()); got != tc.want {
				t.Fatalf("check() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestSSEAuthorizationCanceledStreamReconnects(t *testing.T) {
	f := newSSEAuthFixture(false)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := f.auth.check(ctx); got != sseUnavailable {
		t.Fatalf("check() = %d, want unavailable", got)
	}
}

func TestSSEAuthorizationCachesCallbacksAndAccess(t *testing.T) {
	f := newSSEAuthFixture(false)
	for i := 0; i < 5; i++ {
		if got := f.auth.check(context.Background()); got != sseAllowed {
			t.Fatalf("check %d = %d, want allowed", i, got)
		}
	}
	if f.jwt.calls != 1 || f.channels.calls != 1 || f.access.calls != 1 {
		t.Fatalf("calls jwt=%d channel=%d access=%d, want 1 each", f.jwt.calls, f.channels.calls, f.access.calls)
	}
	// A stale access snapshot is refreshed without repeating the callback.
	f.auth.refreshedAt = time.Now().Add(-realtimeAccessRefreshInterval)
	f.auth.check(context.Background())
	if f.jwt.calls != 1 || f.channels.calls != 1 || f.access.calls != 2 {
		t.Fatalf("after access refresh jwt=%d channel=%d access=%d", f.jwt.calls, f.channels.calls, f.access.calls)
	}
	f.auth.authorizedAt = time.Now().Add(-channelReauthorizeInterval)
	f.auth.check(context.Background())
	if f.jwt.calls != 2 || f.channels.calls != 2 {
		t.Fatalf("after reauthorization jwt=%d channel=%d", f.jwt.calls, f.channels.calls)
	}
}

func TestSSEAuthorizationCachesTickets(t *testing.T) {
	f := newSSEAuthFixture(true)
	for i := 0; i < 5; i++ {
		f.auth.check(context.Background())
	}
	if f.tickets.calls != 1 {
		t.Fatalf("ticket reads = %d, want 1", f.tickets.calls)
	}
}

func TestSSEAdmitReportsOnlyDefinitiveDenies(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*sseAuthFixture)
		want  []string
	}{
		{name: "allowed", want: nil},
		{name: "denied", setup: func(f *sseAuthFixture) { f.channels.allow = false }, want: []string{"error"}},
		{name: "transient", setup: func(f *sseAuthFixture) { f.channels.err = errors.New("timeout") }, want: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newSSEAuthFixture(false)
			if tc.setup != nil {
				tc.setup(f)
			}
			var sent []string
			var code string
			ok := f.auth.admit(context.Background(), func(event, _ string, data any) bool {
				sent = append(sent, event)
				code = data.(map[string]string)["code"]
				return true
			})
			if ok != (tc.name == "allowed") || len(sent) != len(tc.want) {
				t.Fatalf("admit = %v, sent %v, want %v", ok, sent, tc.want)
			}
			if len(sent) == 1 && code != "access_revoked" {
				t.Fatalf("code = %q, want access_revoked", code)
			}
		})
	}
}
