package realtime

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestChannelOccupancyIndex(t *testing.T) {
	m := NewManager(Config{}, nil)
	defer func() { _ = m.Close() }()
	if err := m.RegisterEndpoint(Endpoint{ID: "e", MaxMessageBytes: 1 << 10}); err != nil {
		t.Fatal(err)
	}
	first, _ := outboundTestConnection(t, m, "e")
	second, _ := outboundTestConnection(t, m, "e")
	count := func(channel string) int { return m.ChannelOccupancy("e", []string{channel})[0].Subscribers }

	for _, c := range []*connection{first, second} {
		if err := m.Subscribe(c.info.ID, "lobby"); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.Subscribe(first.info.ID, "lobby"); err != nil { // a repeat counts once
		t.Fatal(err)
	}
	if err := m.Subscribe(first.info.ID, "game"); err != nil {
		t.Fatal(err)
	}
	if count("lobby") != 2 || count("game") != 1 || count("empty") != 0 {
		t.Fatalf("lobby %d game %d empty %d, want 2 1 0", count("lobby"), count("game"), count("empty"))
	}
	if err := m.Unsubscribe(second.info.ID, "lobby"); err != nil {
		t.Fatal(err)
	}
	if count("lobby") != 1 {
		t.Fatalf("lobby after unsubscribe = %d, want 1", count("lobby"))
	}
	m.removeConnection(t.Context(), first)
	if count("lobby") != 0 || count("game") != 0 {
		t.Fatalf("after removal lobby %d game %d, want 0", count("lobby"), count("game"))
	}
	// A subscription that races the removal is never counted.
	first.mu.Lock()
	first.addChannelLocked("late")
	first.removeChannelLocked("late")
	first.addChannelLocked("late")
	first.releaseChannelsLocked()
	first.mu.Unlock()
	if count("late") != 0 {
		t.Fatalf("late subscription after removal = %d, want 0", count("late"))
	}
}

func TestChannelOccupancyInternalEndpoint(t *testing.T) {
	m := NewManager(Config{OccupancyPreview: true}, nil)
	defer func() { _ = m.Close() }()
	if err := m.RegisterEndpoint(Endpoint{ID: "e", MaxMessageBytes: 1 << 10}); err != nil {
		t.Fatal(err)
	}
	c, _ := outboundTestConnection(t, m, "e")
	if err := m.Subscribe(c.info.ID, "lobby"); err != nil {
		t.Fatal(err)
	}
	get := func(query string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		m.internalHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/internal/endpoints/e/occupancy"+query, nil))
		return rec
	}
	rec := get("?channel=lobby&channel=game")
	var counts []ChannelOccupancy
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &counts) != nil {
		t.Fatalf("occupancy = %d %s", rec.Code, rec.Body)
	}
	if len(counts) != 2 || counts[0] != (ChannelOccupancy{Channel: "lobby", Subscribers: 1}) || counts[1] != (ChannelOccupancy{Channel: "game"}) {
		t.Fatalf("counts = %+v", counts)
	}
	tooMany := "?channel=" + strings.Repeat("c&channel=", api.RealtimeOccupancyChannelsMax) + "c"
	for _, query := range []string{"", tooMany, "?channel=a%2Fb"} {
		if rec := get(query); rec.Code != http.StatusBadRequest {
			t.Fatalf("occupancy %q = %d, want 400", query, rec.Code)
		}
	}
}

// adr: 1324 — without the preview the internal read is not served and a
// subscribe frame's occupancy request is ignored, as before occupancy existed.
func TestChannelOccupancyPreviewOff(t *testing.T) {
	m := NewManager(Config{}, nil)
	defer func() { _ = m.Close() }()
	if err := m.RegisterEndpoint(Endpoint{ID: "e", MaxMessageBytes: 1 << 10}); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	m.internalHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/internal/endpoints/e/occupancy?channel=lobby", nil))
	if rec.Code == http.StatusOK {
		t.Fatalf("occupancy without the preview = %d %s, want it unserved", rec.Code, rec.Body)
	}
	c, _ := outboundTestConnection(t, m, "e")
	m.watchSubscriptionOccupancy(t.Context(), c, resumeClientFrame{Type: "subscribe", Channel: "lobby", Occupancy: true})
	m.occupancyLive.mu.Lock()
	watched := len(m.occupancyLive.watchers)
	m.occupancyLive.mu.Unlock()
	if watched != 0 {
		t.Fatalf("watched channels without the preview = %d, want 0", watched)
	}
}
