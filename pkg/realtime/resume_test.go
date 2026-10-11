package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/onebox-faas/faas/pkg/state"
)

type testResumeHistory struct {
	mu        sync.Mutex
	messages  []state.ManagedRealtimeChannelMessage
	oldest    int64
	reads     int
	failAfter int
}

func (h *testResumeHistory) ReadChannelHistory(_ context.Context, endpointID, channel string, after int64, limit int) (state.ManagedRealtimeChannelHistory, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.reads++
	if h.failAfter > 0 && h.reads >= h.failAfter {
		return state.ManagedRealtimeChannelHistory{}, errors.New("history store unavailable")
	}
	if endpointID != "resume-endpoint" || channel != "updates" || limit != resumeHistoryPageSize {
		return state.ManagedRealtimeChannelHistory{}, state.ErrManagedRealtimeHistoryInvalid
	}
	oldest := h.oldest
	if oldest == 0 {
		oldest = 1
	}
	result := state.ManagedRealtimeChannelHistory{OldestSequence: oldest}
	if n := len(h.messages); n > 0 {
		result.LatestSequence = h.messages[n-1].Sequence
	}
	if after < oldest-1 {
		result.HistoryUnavailable = true
		return result, nil
	}
	if after > result.LatestSequence {
		return state.ManagedRealtimeChannelHistory{}, state.ErrManagedRealtimeHistoryInvalid
	}
	for _, message := range h.messages {
		if message.Sequence > after && message.Sequence >= oldest && len(result.Messages) < limit {
			result.Messages = append(result.Messages, message)
		}
	}
	return result, nil
}

func (h *testResumeHistory) appendMessage(sequence int64, data string) {
	h.mu.Lock()
	h.messages = append(h.messages, state.ManagedRealtimeChannelMessage{Sequence: sequence, Data: []byte(data)})
	h.mu.Unlock()
}

type testChannelHooks struct {
	NopHooks
	mu      sync.Mutex
	allowed bool
	events  []Event
}

func (h *testChannelHooks) AuthorizeChannel(_ context.Context, event Event) (bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.events = append(h.events, event)
	return h.allowed, nil
}

type testChannelRouteReport struct {
	endpointID string
	channel    string
	subscribed bool
}

type testChannelRouteReporter struct {
	mu      sync.Mutex
	reports []testChannelRouteReport
	err     error
}

func (r *testChannelRouteReporter) ReportChannelRoute(_ context.Context, endpointID, channel string, subscribed bool) error {
	r.mu.Lock()
	r.reports = append(r.reports, testChannelRouteReport{endpointID: endpointID, channel: channel, subscribed: subscribed})
	err := r.err
	r.mu.Unlock()
	return err
}

func (r *testChannelRouteReporter) snapshot() []testChannelRouteReport {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]testChannelRouteReport(nil), r.reports...)
}

func newResumeTestManager(t *testing.T, history *testResumeHistory, hooks *testChannelHooks) (*Manager, *httptest.Server) {
	return newResumeTestManagerWithReporter(t, history, hooks, nil)
}

func newResumeTestManagerWithReporter(t *testing.T, history *testResumeHistory, hooks *testChannelHooks, reporter ManagedRealtimeChannelRouteReporter) (*Manager, *httptest.Server) {
	t.Helper()
	m := NewManager(Config{
		JWTAuthorizer: &recordingJWTAuthorizer{}, ResumePreview: true,
		HistoryReader: history, ResumePollInterval: 15 * time.Millisecond,
		ChannelRouteReporter: reporter,
	}, hooks)
	if err := m.RegisterEndpoint(Endpoint{
		ID: "resume-endpoint", AppID: "app-1", AccountID: "acct-1", CallbackURL: "https://app.example",
		AllowedOrigins: []string{"https://app.example"},
		ClientAuth: AuthPolicy{
			Mode: AuthModeOIDCJWT, Issuer: "https://issuer.example",
			JWKSURL: "https://issuer.example/.well-known/jwks.json", Algorithms: []string{"RS256"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(m.Handler())
	t.Cleanup(func() { server.Close(); _ = m.Close() })
	return m, server
}

func newSyntheticResumeTestManager(t *testing.T, history *testResumeHistory, hooks *testChannelHooks, reporter ManagedRealtimeChannelRouteReporter) *Manager {
	t.Helper()
	m := NewManager(Config{
		JWTAuthorizer: &recordingJWTAuthorizer{}, ResumePreview: true,
		HistoryReader: history, ResumePollInterval: 15 * time.Millisecond,
		ChannelRouteReporter: reporter,
	}, hooks)
	if err := m.RegisterEndpoint(Endpoint{
		ID: "resume-endpoint", AppID: "app-1", AccountID: "acct-1", CallbackURL: "https://app.example",
		AllowedOrigins: []string{"https://app.example"},
		ClientAuth: AuthPolicy{
			Mode: AuthModeOIDCJWT, Issuer: "https://issuer.example",
			JWKSURL: "https://issuer.example/.well-known/jwks.json", Algorithms: []string{"RS256"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		m.cancel()
		m.mu.RLock()
		connections := make([]*connection, 0, len(m.conns))
		for _, c := range m.conns {
			connections = append(connections, c)
		}
		m.mu.RUnlock()
		for _, c := range connections {
			c.closeOutboundQueue()
		}
	})
	return m
}

func addSyntheticResumeTestConnection(t *testing.T, m *Manager) *connection {
	t.Helper()
	value, ok := m.endpoints.Load("resume-endpoint")
	if !ok {
		t.Fatal("resume endpoint is not registered")
	}
	endpointState := value.(*endpointState)
	return m.addConnectionWithProtocol(endpointState, *endpointState.config.Load(), "user-123", nil, true)
}

func waitForResumeTestWorkers(t *testing.T, m *Manager) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if m.resumeCount.Load() == 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("resume workers did not stop; active reservations = %d", m.resumeCount.Load())
}

func TestResumeRouteReportsOnlyFirstAndLastLocalSubscriber(t *testing.T) {
	history := &testResumeHistory{}
	hooks := &testChannelHooks{allowed: true}
	reporter := &testChannelRouteReporter{}
	m := newSyntheticResumeTestManager(t, history, hooks, reporter)
	first, second := addSyntheticResumeTestConnection(t, m), addSyntheticResumeTestConnection(t, m)
	firstCtx, cancelFirst := context.WithCancel(context.Background())
	defer cancelFirst()
	secondCtx, cancelSecond := context.WithCancel(context.Background())
	defer cancelSecond()
	m.resumeSubscribe(firstCtx, first, resumeClientFrame{Type: "subscribe", Channel: "updates"})
	m.resumeSubscribe(secondCtx, second, resumeClientFrame{Type: "subscribe", Channel: "updates"})
	if got := reporter.snapshot(); len(got) != 1 || got[0] != (testChannelRouteReport{endpointID: "resume-endpoint", channel: "updates", subscribed: true}) {
		t.Fatalf("reports after two subscribes = %+v, want one route add", got)
	}

	m.resumeUnsubscribe(firstCtx, first, "updates")
	if got := reporter.snapshot(); len(got) != 1 {
		t.Fatalf("reports while second subscriber remains = %+v, want no removal", got)
	}

	m.resumeUnsubscribe(secondCtx, second, "updates")
	want := []testChannelRouteReport{
		{endpointID: "resume-endpoint", channel: "updates", subscribed: true},
		{endpointID: "resume-endpoint", channel: "updates", subscribed: false},
	}
	got := reporter.snapshot()
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("route reports = %+v, want %+v", got, want)
	}
	if routes := m.ChannelRouteSnapshot(); len(routes) != 0 {
		t.Fatalf("routes after last unsubscribe = %+v, want empty", routes)
	}
	waitForResumeTestWorkers(t, m)
}

func TestResumeRouteReportFailureKeepsSubscriptionAndRevision(t *testing.T) {
	history := &testResumeHistory{}
	hooks := &testChannelHooks{allowed: true}
	reporter := &testChannelRouteReporter{err: errors.New("route directory unavailable")}
	m := newSyntheticResumeTestManager(t, history, hooks, reporter)
	client := addSyntheticResumeTestConnection(t, m)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.resumeSubscribe(ctx, client, resumeClientFrame{Type: "subscribe", Channel: "updates"})
	if routes := m.ChannelRouteSnapshot(); len(routes) != 1 || routes[0] != (ChannelRoute{EndpointID: "resume-endpoint", Channel: "updates"}) {
		t.Fatalf("local route after report failure = %+v, want updates route", routes)
	}
	if revision := m.ChannelRouteRevision(); revision.InstanceID == "" || revision.Revision != 1 {
		t.Fatalf("route revision after report failure = %+v, want revision 1", revision)
	}
	if reports := reporter.snapshot(); len(reports) != 1 || !reports[0].subscribed {
		t.Fatalf("route reports = %+v, want one attempted add", reports)
	}

	// Restore reporting before cleanup so the test leaves no stale test route.
	reporter.mu.Lock()
	reporter.err = nil
	reporter.mu.Unlock()
	m.resumeUnsubscribe(ctx, client, "updates")
	waitForResumeTestWorkers(t, m)
}

func TestResumeBrowserBearerSubprotocol(t *testing.T) {
	history := &testResumeHistory{}
	hooks := &testChannelHooks{allowed: true}
	m, server := newResumeTestManager(t, history, hooks)
	value, _ := m.endpoints.Load("resume-endpoint")
	endpoint := *value.(*endpointState).config.Load()
	seenProtocols := make(chan string, 1)
	endpoint.Authorize = func(_ context.Context, request *http.Request) (string, error) {
		seenProtocols <- request.Header.Get("Sec-WebSocket-Protocol")
		return "user-123", nil
	}
	if err := m.RegisterEndpoint(endpoint); err != nil {
		t.Fatal(err)
	}
	url := "ws" + strings.TrimPrefix(server.URL, "http") + ManagedPathPrefix + "resume-endpoint"
	credential := ResumeBearerSubprotocolPrefix + "aaa.bbb.ccc"
	dialer := websocket.Dialer{Subprotocols: []string{ResumeSubprotocol, credential}}
	client, response, err := dialer.Dial(url, http.Header{"Origin": {"https://app.example"}})
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if got := client.Subprotocol(); got != ResumeSubprotocol {
		t.Fatalf("selected subprotocol = %q, want only v2", got)
	}
	if got := <-seenProtocols; got != ResumeSubprotocol {
		t.Fatalf("authorization hook protocols = %q, want credential stripped", got)
	}
	if got := m.cfg.JWTAuthorizer.(*recordingJWTAuthorizer).token; got != "aaa.bbb.ccc" {
		t.Fatalf("verified token = %q", got)
	}
	sendResumeTestFrame(t, client, resumeClientFrame{Type: "subscribe", Channel: "updates"})
	if frame := readResumeTestFrame(t, client); frame.Type != "subscribed" {
		t.Fatalf("browser subscription = %+v", frame)
	}
}

func TestResumeBrowserBearerRequiresEndpointOriginPolicy(t *testing.T) {
	history := &testResumeHistory{}
	hooks := &testChannelHooks{allowed: true}
	m, server := newResumeTestManager(t, history, hooks)
	value, _ := m.endpoints.Load("resume-endpoint")
	endpoint := *value.(*endpointState).config.Load()
	endpoint.AllowedOrigins = nil
	endpoint.CheckOrigin = nil
	if err := m.RegisterEndpoint(endpoint); err != nil {
		t.Fatal(err)
	}
	url := "ws" + strings.TrimPrefix(server.URL, "http") + ManagedPathPrefix + "resume-endpoint"
	dialer := websocket.Dialer{Subprotocols: []string{ResumeSubprotocol, ResumeBearerSubprotocolPrefix + "aaa.bbb.ccc"}}
	client, response, err := dialer.Dial(url, http.Header{"Origin": {"https://app.example"}})
	if client != nil {
		_ = client.Close()
	}
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err == nil || response == nil || response.StatusCode != http.StatusForbidden {
		t.Fatalf("dial = (%v, %v), want HTTP 403", err, response)
	}
}

func TestResumeBrowserBearerRequiresExplicitOriginAndUnambiguousCredential(t *testing.T) {
	history := &testResumeHistory{}
	hooks := &testChannelHooks{allowed: true}
	_, server := newResumeTestManager(t, history, hooks)
	url := "ws" + strings.TrimPrefix(server.URL, "http") + ManagedPathPrefix + "resume-endpoint"
	credential := ResumeBearerSubprotocolPrefix + "aaa.bbb.ccc"
	for _, tc := range []struct {
		name      string
		protocols []string
		header    http.Header
		status    int
	}{
		{"missing origin", []string{ResumeSubprotocol, credential}, nil, http.StatusForbidden},
		{"wrong origin", []string{ResumeSubprotocol, credential}, http.Header{"Origin": {"https://evil.example"}}, http.StatusForbidden},
		{"ambiguous headers", []string{ResumeSubprotocol, credential}, http.Header{"Origin": {"https://app.example"}, "Authorization": {"Bearer another"}}, http.StatusBadRequest},
		{"duplicate credentials", []string{ResumeSubprotocol, credential, credential}, http.Header{"Origin": {"https://app.example"}}, http.StatusBadRequest},
		{"malformed JWT", []string{ResumeSubprotocol, ResumeBearerSubprotocolPrefix + "not-a-jwt"}, http.Header{"Origin": {"https://app.example"}}, http.StatusBadRequest},
		{"credential without v2", []string{credential}, http.Header{"Origin": {"https://app.example"}}, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dialer := websocket.Dialer{Subprotocols: tc.protocols}
			client, response, err := dialer.Dial(url, tc.header)
			if client != nil {
				_ = client.Close()
			}
			if response != nil && response.Body != nil {
				_ = response.Body.Close()
			}
			if err == nil || response == nil || response.StatusCode != tc.status {
				t.Fatalf("dial = (%v, %v), want HTTP %d", err, response, tc.status)
			}
		})
	}
}

func dialResumeTest(t *testing.T, server *httptest.Server) *websocket.Conn {
	t.Helper()
	dialer := websocket.Dialer{Subprotocols: []string{ResumeSubprotocol}}
	url := "ws" + strings.TrimPrefix(server.URL, "http") + ManagedPathPrefix + "resume-endpoint"
	client, response, err := dialer.Dial(url, http.Header{"Authorization": {"Bearer test-jwt"}})
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	if client.Subprotocol() != ResumeSubprotocol {
		t.Fatalf("subprotocol = %q", client.Subprotocol())
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func sendResumeTestFrame(t *testing.T, client *websocket.Conn, frame resumeClientFrame) {
	t.Helper()
	if err := client.WriteJSON(frame); err != nil {
		t.Fatal(err)
	}
}

func readResumeTestFrame(t *testing.T, client *websocket.Conn) resumeServerFrame {
	t.Helper()
	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, data, err := client.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	var frame resumeServerFrame
	if err := json.Unmarshal(data, &frame); err != nil {
		t.Fatal(err)
	}
	return frame
}

func TestResumeReplayAckAndPollAcrossConnections(t *testing.T) {
	history := &testResumeHistory{}
	for i, data := range []string{"one", "two", "three"} {
		history.appendMessage(int64(i+1), data)
	}
	hooks := &testChannelHooks{allowed: true}
	_, server := newResumeTestManager(t, history, hooks)
	client := dialResumeTest(t, server)
	sendResumeTestFrame(t, client, resumeClientFrame{Type: "subscribe", Channel: "updates", After: 1})
	if frame := readResumeTestFrame(t, client); frame.Type != "subscribed" || frame.LatestSequence != 3 {
		t.Fatalf("subscribe = %+v", frame)
	}
	for _, want := range []struct {
		sequence int64
		data     string
	}{{2, "dHdv"}, {3, "dGhyZWU="}} {
		frame := readResumeTestFrame(t, client)
		if frame.Type != "message" || frame.Sequence != want.sequence || frame.DataBase64 != want.data || frame.MessageID == "" {
			t.Fatalf("replay message = %+v", frame)
		}
	}
	sendResumeTestFrame(t, client, resumeClientFrame{Type: "ack", Channel: "updates", Sequence: 3})
	if frame := readResumeTestFrame(t, client); frame.Type != "acknowledged" || frame.Sequence != 3 {
		t.Fatalf("ack = %+v", frame)
	}
	sendResumeTestFrame(t, client, resumeClientFrame{Type: "ack", Channel: "updates", Sequence: 4})
	if frame := readResumeTestFrame(t, client); frame.Code != "invalid_ack" {
		t.Fatalf("future ack = %+v", frame)
	}
	history.appendMessage(4, "four")
	live := readResumeTestFrame(t, client)
	if live.Type != "message" || live.Sequence != 4 {
		t.Fatalf("polled message = %+v", live)
	}
	_ = client.Close()
	// A second realtime owner reads the shared log with the last
	// client-held acknowledged cursor.
	_, secondServer := newResumeTestManager(t, history, hooks)
	next := dialResumeTest(t, secondServer)
	sendResumeTestFrame(t, next, resumeClientFrame{Type: "subscribe", Channel: "updates", After: 3})
	if frame := readResumeTestFrame(t, next); frame.Type != "subscribed" {
		t.Fatalf("resubscribe = %+v", frame)
	}
	if frame := readResumeTestFrame(t, next); frame.Type != "message" || frame.Sequence != 4 || frame.MessageID != live.MessageID {
		t.Fatalf("reconnect replay = %+v", frame)
	}
	hooks.mu.Lock()
	events := append([]Event(nil), hooks.events...)
	hooks.mu.Unlock()
	if len(events) != 2 || events[0].Principal != "user-123" || events[0].Channel != "updates" || events[0].Permission != "read" {
		t.Fatalf("channel decisions = %+v", events)
	}
}

func TestResumeDeniesBeforeHistoryAndReportsExpiredCursor(t *testing.T) {
	history := &testResumeHistory{oldest: 3}
	for i, data := range []string{"one", "two", "three"} {
		history.appendMessage(int64(i+1), data)
	}
	hooks := &testChannelHooks{}
	_, server := newResumeTestManager(t, history, hooks)
	client := dialResumeTest(t, server)
	sendResumeTestFrame(t, client, resumeClientFrame{Type: "subscribe", Channel: "updates", After: 1})
	if frame := readResumeTestFrame(t, client); frame.Code != "not_authorized" {
		t.Fatalf("denied subscribe = %+v", frame)
	}
	history.mu.Lock()
	reads := history.reads
	history.mu.Unlock()
	if reads != 0 {
		t.Fatalf("history read before channel grant: %d", reads)
	}
	hooks.mu.Lock()
	hooks.allowed = true
	hooks.mu.Unlock()
	sendResumeTestFrame(t, client, resumeClientFrame{Type: "subscribe", Channel: "updates", After: 1})
	if frame := readResumeTestFrame(t, client); frame.Type != "resync_required" || frame.OldestSequence != 3 {
		t.Fatalf("expired cursor = %+v", frame)
	}
}

func TestResumeHandshakeRequiresPreviewAndOIDC(t *testing.T) {
	history := &testResumeHistory{}
	hooks := &testChannelHooks{allowed: true}
	for _, tc := range []struct {
		name     string
		preview  bool
		endpoint Endpoint
		status   int
	}{
		{
			name: "preview disabled", endpoint: Endpoint{ID: "resume-endpoint", ClientAuth: AuthPolicy{
				Mode: AuthModeOIDCJWT, Issuer: "https://issuer.example",
				JWKSURL: "https://issuer.example/jwks", Algorithms: []string{"RS256"},
			}}, status: http.StatusServiceUnavailable,
		},
		{
			name: "shared bearer", preview: true, endpoint: Endpoint{
				ID: "resume-endpoint", AuthToken: "shared-token", ClientAuth: AuthPolicy{Mode: AuthModeStaticBearer},
			}, status: http.StatusForbidden,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := NewManager(Config{ResumePreview: tc.preview, HistoryReader: history, JWTAuthorizer: &recordingJWTAuthorizer{}}, hooks)
			defer m.Close()
			if err := m.RegisterEndpoint(tc.endpoint); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(m.Handler())
			defer server.Close()
			dialer := websocket.Dialer{Subprotocols: []string{ResumeSubprotocol}}
			url := "ws" + strings.TrimPrefix(server.URL, "http") + ManagedPathPrefix + "resume-endpoint"
			client, response, err := dialer.Dial(url, http.Header{"Authorization": {"Bearer test-jwt"}})
			if client != nil {
				_ = client.Close()
			}
			if response != nil && response.Body != nil {
				_ = response.Body.Close()
			}
			if err == nil || response == nil || response.StatusCode != tc.status {
				t.Fatalf("v2 handshake = (%v, %v), want HTTP %d", err, response, tc.status)
			}
		})
	}
}

func TestResumeHistoryFailureClosesWithRetryableCode(t *testing.T) {
	history := &testResumeHistory{failAfter: 2}
	hooks := &testChannelHooks{allowed: true}
	_, server := newResumeTestManager(t, history, hooks)
	client := dialResumeTest(t, server)
	sendResumeTestFrame(t, client, resumeClientFrame{Type: "subscribe", Channel: "updates"})
	if frame := readResumeTestFrame(t, client); frame.Type != "subscribed" {
		t.Fatalf("subscribe = %+v", frame)
	}
	for range 2 {
		_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, _, err := client.ReadMessage()
		var closeErr *websocket.CloseError
		if errors.As(err, &closeErr) && closeErr.Code == websocket.CloseTryAgainLater {
			return
		}
		if err != nil {
			t.Fatalf("history failure close = %v", err)
		}
	}
	t.Fatal("history failure did not close v2 connection")
}
