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

	// The resumable subscriber also asks for live occupancy frames.
	resumable := dialTicketWebSocket(t, node, "test-jwt")
	sendResumeTestFrame(t, resumable, resumeClientFrame{Type: "subscribe", Channel: "lobby", Occupancy: true})
	if frame := readResumeTestFrame(t, resumable); frame.Type != "subscribed" {
		t.Fatalf("resumable subscribe = %+v", frame)
	}
	waitOccupancy(2, "resumable subscribe")
	expectOccupancyFrame(t, resumable, 2)

	reader := dialTicketWebSocket(t, node, node.ticket(t, "reader", 60, "lobby"))
	sendResumeTestFrame(t, reader, resumeClientFrame{Type: "subscribe", Channel: "lobby"})
	if frame := readResumeTestFrame(t, reader); frame.Type != "subscribed" {
		t.Fatalf("ticket subscribe = %+v", frame)
	}
	waitOccupancy(3, "ticket subscribe")
	expectOccupancyFrame(t, resumable, 3)

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

	// An SSE stream asks for live counts with ?occupancy=1.
	status, events = node.openSSEQuery(t, node.ticket(t, "watcher", 60, "lobby"), "channel=lobby&occupancy=1", "")
	if status != http.StatusOK {
		t.Fatalf("SSE occupancy status = %d", status)
	}
	if ev, _ := nextSSE(t, events, 3*time.Second); ev.Event != "subscribed" {
		t.Fatalf("SSE occupancy first event = %+v", ev)
	}
	expectSSEOccupancy(t, events, 1)
	viewer := dialTicketWebSocket(t, node, node.ticket(t, "viewer-2", 60, "lobby"))
	sendResumeTestFrame(t, viewer, resumeClientFrame{Type: "subscribe", Channel: "lobby"})
	if frame := readResumeTestFrame(t, viewer); frame.Type != "subscribed" {
		t.Fatalf("second ticket subscribe = %+v", frame)
	}
	expectSSEOccupancy(t, events, 2)
}

// expectSSEOccupancy reads past other events to the next occupancy event and
// checks its count.
func expectSSEOccupancy(t *testing.T, events <-chan sseTestEvent, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ev, ok := nextSSE(t, events, time.Until(deadline))
		if !ok {
			t.Fatalf("SSE stream ended before an occupancy event with %d subscribers", want)
		}
		if ev.Event != "occupancy" {
			continue
		}
		if ev.Data["channel"] != "lobby" || ev.Data["subscribers"] != float64(want) {
			t.Fatalf("SSE occupancy event = %+v, want lobby %d", ev, want)
		}
		return
	}
	t.Fatalf("no SSE occupancy event with %d subscribers", want)
}

// expectOccupancyFrame reads past other frames to the next live occupancy
// frame for lobby and checks its count.
func expectOccupancyFrame(t *testing.T, client *websocket.Conn, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		frame := readResumeTestFrame(t, client)
		if frame.Type != "occupancy" {
			continue
		}
		if frame.Channel != "lobby" || frame.Subscribers == nil || *frame.Subscribers != want {
			t.Fatalf("occupancy frame = %+v, want lobby %d", frame, want)
		}
		return
	}
	t.Fatalf("no occupancy frame with %d subscribers", want)
}
