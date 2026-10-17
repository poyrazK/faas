package realtime

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// adr: 1324 — legacy and resumable WebSocket, ticket and SSE subscriptions
// all count toward channel occupancy, and each leaves the count when it ends.
func TestChannelOccupancyCountsEveryTransport(t *testing.T) {
	node := startPGConnectionsNode(t)
	occupancy := func() int {
		return node.manager.ChannelOccupancy(node.endpoint, []string{"lobby"})[0].Subscribers
	}
	waitOccupancy := func(want int, why string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for occupancy() != want {
			if time.Now().After(deadline) {
				t.Fatalf("%s: occupancy = %d, want %d", why, occupancy(), want)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	target := "ws" + strings.TrimPrefix(node.server.URL, "http") + ManagedPathPrefix + node.endpoint
	legacy, response, err := websocket.DefaultDialer.Dial(target, http.Header{"Authorization": {"Bearer test-jwt"}})
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = legacy.Close() }()
	connections := node.manager.Snapshot()
	for deadline := time.Now().Add(5 * time.Second); len(connections) != 1; connections = node.manager.Snapshot() {
		if time.Now().After(deadline) {
			t.Fatalf("connections = %+v, want the legacy socket", connections)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := node.manager.Subscribe(connections[0].ID, "lobby"); err != nil {
		t.Fatal(err)
	}
	waitOccupancy(1, "legacy subscribe")

	resumable := dialTicketWebSocket(t, node, "test-jwt")
	sendResumeTestFrame(t, resumable, resumeClientFrame{Type: "subscribe", Channel: "lobby"})
	if frame := readResumeTestFrame(t, resumable); frame.Type != "subscribed" {
		t.Fatalf("resumable subscribe = %+v", frame)
	}
	waitOccupancy(2, "resumable subscribe")

	reader := dialTicketWebSocket(t, node, node.ticket(t, "reader", 60, "lobby"))
	sendResumeTestFrame(t, reader, resumeClientFrame{Type: "subscribe", Channel: "lobby"})
	if frame := readResumeTestFrame(t, reader); frame.Type != "subscribed" {
		t.Fatalf("ticket subscribe = %+v", frame)
	}
	waitOccupancy(3, "ticket subscribe")

	status, events := node.openSSE(t, node.ticket(t, "viewer", 1, "lobby"), "lobby", "")
	if status != http.StatusOK {
		t.Fatalf("SSE status = %d", status)
	}
	if ev, _ := nextSSE(t, events, 3*time.Second); ev.Event != "subscribed" {
		t.Fatalf("SSE first event = %+v", ev)
	}
	waitOccupancy(4, "SSE stream")
	waitOccupancy(3, "SSE stream ended at ticket expiry")

	sendResumeTestFrame(t, resumable, resumeClientFrame{Type: "unsubscribe", Channel: "lobby"})
	waitOccupancy(2, "resumable unsubscribe")
	_ = reader.Close()
	waitOccupancy(1, "ticket socket closed")
	_ = legacy.Close()
	waitOccupancy(0, "legacy socket closed")
}
