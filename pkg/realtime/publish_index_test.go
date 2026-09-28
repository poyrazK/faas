package realtime

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func addSyntheticConnection(tb testing.TB, m *Manager, endpointID string) *connection {
	tb.Helper()
	value, ok := m.endpoints.Load(endpointID)
	if !ok {
		tb.Fatalf("endpoint %q is not registered", endpointID)
	}
	state := value.(*endpointState)
	return m.addConnection(state, *state.config.Load(), "", nil)
}

func TestManagerPublishSelectsEndpointAndChannel(t *testing.T) {
	m := NewManager(Config{}, nil)
	defer m.cancel()
	first := Endpoint{ID: "first", MaxMessageBytes: 1024}
	second := Endpoint{ID: "second", MaxMessageBytes: 1024}
	for _, endpoint := range []Endpoint{first, second} {
		if err := m.RegisterEndpoint(endpoint); err != nil {
			t.Fatal(err)
		}
	}
	alerts := addSyntheticConnection(t, m, first.ID)
	updates := addSyntheticConnection(t, m, first.ID)
	otherEndpoint := addSyntheticConnection(t, m, second.ID)
	for _, subscription := range []struct {
		id      string
		channel string
	}{
		{alerts.info.ID, "alerts"},
		{alerts.info.ID, "alerts"}, // A repeat subscription must not duplicate delivery.
		{updates.info.ID, "updates"},
		{otherEndpoint.info.ID, "alerts"},
	} {
		if err := m.Subscribe(subscription.id, subscription.channel); err != nil {
			t.Fatal(err)
		}
	}
	msg := Message{Data: []byte("hello")}
	if queued, err := m.Publish(context.Background(), first.ID, "alerts", msg); err != nil || queued != 1 {
		t.Fatalf("first alerts publish = (%d, %v), want (1, nil)", queued, err)
	}
	if got := string((<-alerts.outbound).Data); got != "hello" {
		t.Fatalf("first alerts payload = %q", got)
	}
	if len(updates.outbound) != 0 || len(otherEndpoint.outbound) != 0 {
		t.Fatal("publish crossed a channel or endpoint boundary")
	}
	if err := m.Unsubscribe(alerts.info.ID, "alerts"); err != nil {
		t.Fatal(err)
	}
	if queued, err := m.Publish(context.Background(), first.ID, "alerts", msg); err != nil || queued != 0 {
		t.Fatalf("unsubscribed publish = (%d, %v), want (0, nil)", queued, err)
	}
	if queued, err := m.Publish(context.Background(), second.ID, "alerts", msg); err != nil || queued != 1 {
		t.Fatalf("second endpoint publish = (%d, %v), want (1, nil)", queued, err)
	}
	<-otherEndpoint.outbound
}

func TestManagerChannelRouteSnapshotReturnsUniqueEndpointChannelPairs(t *testing.T) {
	m := NewManager(Config{}, nil)
	defer m.cancel()
	for _, endpointID := range []string{"first", "second"} {
		if err := m.RegisterEndpoint(Endpoint{ID: endpointID, MaxMessageBytes: 1024}); err != nil {
			t.Fatal(err)
		}
	}
	first := addSyntheticConnection(t, m, "first")
	second := addSyntheticConnection(t, m, "first")
	otherEndpoint := addSyntheticConnection(t, m, "second")
	for _, subscription := range []struct {
		id      string
		channel string
	}{
		{first.info.ID, "alerts"},
		{first.info.ID, "alerts"},
		{second.info.ID, "alerts"},
		{second.info.ID, "updates"},
		{otherEndpoint.info.ID, "alerts"},
	} {
		if err := m.Subscribe(subscription.id, subscription.channel); err != nil {
			t.Fatal(err)
		}
	}

	want := []ChannelRoute{
		{EndpointID: "first", Channel: "alerts"},
		{EndpointID: "first", Channel: "updates"},
		{EndpointID: "second", Channel: "alerts"},
	}
	got := m.ChannelRouteSnapshot()
	if len(got) != len(want) {
		t.Fatalf("ChannelRouteSnapshot = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ChannelRouteSnapshot = %+v, want %+v", got, want)
		}
	}
}

func TestManagerChannelRouteRevisionChangesAfterLastSubscriberDisconnects(t *testing.T) {
	m := NewManager(Config{}, &testHooks{accept: true})
	defer m.Close()
	endpoint := Endpoint{ID: "disconnect", MaxMessageBytes: 1024}
	if err := m.RegisterEndpoint(endpoint); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(m.Handler())
	defer server.Close()
	url := "ws" + strings.TrimPrefix(server.URL, "http") + ManagedPathPrefix + endpoint.ID
	client, response, err := websocket.DefaultDialer.Dial(url, nil)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	deadline := time.Now().Add(time.Second)
	var connectionID string
	for time.Now().Before(deadline) {
		connections := m.Snapshot()
		if len(connections) == 1 {
			connectionID = connections[0].ID
			break
		}
		time.Sleep(time.Millisecond)
	}
	if connectionID == "" {
		t.Fatal("connection did not become active")
	}
	if err := m.Subscribe(connectionID, "updates"); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	before := m.ChannelRouteRevision()
	if before.InstanceID == "" {
		t.Fatal("channel route revision has no process instance")
	}
	if got := m.ChannelRouteSnapshot(); len(got) != 1 || got[0] != (ChannelRoute{EndpointID: endpoint.ID, Channel: "updates"}) {
		t.Fatalf("routes before disconnect = %+v, want updates route", got)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("close client: %v", err)
	}

	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(m.ChannelRouteSnapshot()) == 0 && m.ChannelRouteRevision().Revision > before.Revision {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if got := m.ChannelRouteSnapshot(); len(got) != 0 {
		t.Fatalf("routes after disconnect = %+v, want empty", got)
	}
	after := m.ChannelRouteRevision()
	if after.InstanceID != before.InstanceID || after.Revision != before.Revision+1 {
		t.Fatalf("route revision after last disconnect = %+v, want same instance and revision %d", after, before.Revision+1)
	}
}

func TestManagerPublishConcurrentSubscriptionChanges(t *testing.T) {
	m := NewManager(Config{OutboundQueue: 512}, nil)
	defer m.cancel()
	endpoint := Endpoint{ID: "concurrent", MaxMessageBytes: 1024}
	if err := m.RegisterEndpoint(endpoint); err != nil {
		t.Fatal(err)
	}
	c := addSyntheticConnection(t, m, endpoint.ID)
	var workers sync.WaitGroup
	errors := make(chan error, 2)
	workers.Add(3)
	go func() {
		defer workers.Done()
		for i := 0; i < 100; i++ {
			if err := m.Subscribe(c.info.ID, "updates"); err != nil {
				errors <- err
				return
			}
			if err := m.Unsubscribe(c.info.ID, "updates"); err != nil {
				errors <- err
				return
			}
		}
	}()
	go func() {
		defer workers.Done()
		for i := 0; i < 100; i++ {
			if _, err := m.Publish(context.Background(), endpoint.ID, "updates", Message{Data: []byte("x")}); err != nil {
				errors <- err
				return
			}
		}
	}()
	go func() {
		defer workers.Done()
		for i := 0; i < 100; i++ {
			_ = m.Snapshot()
		}
	}()
	workers.Wait()
	close(errors)
	for err := range errors {
		t.Fatal(err)
	}
	if queued, err := m.Publish(context.Background(), endpoint.ID, "updates", Message{Data: []byte("x")}); err != nil || queued != 0 {
		t.Fatalf("publish after unsubscribe = (%d, %v), want (0, nil)", queued, err)
	}
}

func TestManagerPublishRemovesClosedConnectionFromChannelIndex(t *testing.T) {
	m := NewManager(Config{}, nil)
	defer m.Close()
	endpoint := Endpoint{ID: "disconnect", MaxMessageBytes: 1024}
	if err := m.RegisterEndpoint(endpoint); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(m.Handler())
	defer server.Close()
	client, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+ManagedPathPrefix+endpoint.ID, nil)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	deadline := time.Now().Add(time.Second)
	var id string
	for time.Now().Before(deadline) {
		connections := m.Snapshot()
		if len(connections) == 1 {
			id = connections[0].ID
			break
		}
		time.Sleep(time.Millisecond)
	}
	if id == "" {
		t.Fatal("connection did not become active")
	}
	if err := m.Subscribe(id, "updates"); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("close client: %v", err)
	}

	deadline = time.Now().Add(time.Second)
	for len(m.Snapshot()) != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := len(m.Snapshot()); got != 0 {
		t.Fatalf("connections after disconnect = %d, want 0", got)
	}
	if queued, err := m.Publish(context.Background(), endpoint.ID, "updates", Message{Data: []byte("x")}); err != nil || queued != 0 {
		t.Fatalf("publish after disconnect = (%d, %v), want (0, nil)", queued, err)
	}
}
