package gateway

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

var (
	irAccount = uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	irApp     = uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
	irBase    = time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
)

func TestInstanceRouteMinutesSplitsLongRequests(t *testing.T) {
	c := newInstanceRouteMinutes(InstanceRouteMinutesConfig{}, nil)
	// A 90 s export that finishes at 10:01:30 is in flight for all of
	// 10:00 and 30 s of 10:01: present in both, counted once.
	c.add(irAccount, irApp, "i-1", "POST /export", "POST", irBase.Add(90*time.Second), 90*time.Second)
	c.add(irAccount, irApp, "i-1", "GET /items", "GET", irBase.Add(10*time.Second), 20*time.Millisecond)
	c.add(irAccount, irApp, "i-1", "GET /items", "GET", irBase.Add(11*time.Second), 30*time.Millisecond)
	rows := c.take(irBase, true)
	want := []InstanceRouteMinute{
		{AccountID: irAccount, AppID: irApp, InstanceID: "i-1", Minute: irBase, Route: "GET /items", Method: "GET", Requests: 2, BusyMS: 50, MaxLatencyMS: 30},
		{AccountID: irAccount, AppID: irApp, InstanceID: "i-1", Minute: irBase, Route: "POST /export", Method: "POST", BusyMS: 60000},
		{AccountID: irAccount, AppID: irApp, InstanceID: "i-1", Minute: irBase.Add(time.Minute), Route: "POST /export", Method: "POST", Requests: 1, BusyMS: 30000, MaxLatencyMS: 90000},
	}
	if fmt.Sprint(rows) != fmt.Sprint(want) {
		t.Fatalf("rows\n got %+v\nwant %+v", rows, want)
	}
}

func TestInstanceRouteMinutesBoundsSpanAndRoutes(t *testing.T) {
	c := newInstanceRouteMinutes(InstanceRouteMinutesConfig{}, nil)
	// An hour-long stream is present only in its last minutes.
	c.add(irAccount, irApp, "i-1", "GET /stream", "GET", irBase.Add(time.Hour), time.Hour)
	if rows := c.take(irBase, true); len(rows) != instanceRouteSpanMax {
		t.Fatalf("stream minutes = %d", len(rows))
	}
	for i := 0; i < api.RouteResourceRoutesPerInstanceMinuteMax+5; i++ {
		c.add(irAccount, irApp, "i-1", fmt.Sprintf("GET /r%d", i), "GET", irBase.Add(time.Second), time.Millisecond)
	}
	rows := c.take(irBase, true)
	if len(rows) != api.RouteResourceRoutesPerInstanceMinuteMax+1 {
		t.Fatalf("routes = %d", len(rows))
	}
	var other *InstanceRouteMinute
	for i := range rows {
		if rows[i].Route == api.RouteResourceOtherRoute {
			other = &rows[i]
		}
	}
	if other == nil || other.Requests != 5 {
		t.Fatalf("other = %+v", other)
	}
}

func TestInstanceRouteMinutesKeepsOpenMinutesAndDropsFailures(t *testing.T) {
	now := irBase.Add(70 * time.Second)
	var shipped [][]InstanceRouteMinute
	dropped := 0
	c := newInstanceRouteMinutes(InstanceRouteMinutesConfig{
		Now:       func() time.Time { return now },
		OnDropped: func(n int) { dropped += n },
		Ship: func(_ context.Context, rows []InstanceRouteMinute) error {
			shipped = append(shipped, rows)
			return errors.New("apid down")
		},
	}, nil)
	c.add(irAccount, irApp, "i-1", "GET /", "GET", irBase.Add(5*time.Second), time.Millisecond)
	c.flush(context.Background(), false)
	if len(shipped) != 0 {
		t.Fatal("open minute shipped")
	}
	now = irBase.Add(2 * time.Minute)
	c.flush(context.Background(), false)
	c.flush(context.Background(), false)
	if len(shipped) != 1 || dropped != 1 {
		t.Fatalf("shipped %d batches, dropped %d", len(shipped), dropped)
	}
}

func TestCountInstanceRouteNeedsTheFlag(t *testing.T) {
	h := &Handler{instanceRoutes: newInstanceRouteMinutes(InstanceRouteMinutesConfig{}, nil)}
	h.countInstanceRoute(irAccount, irApp, "pro", "i-1", "GET /", "GET", irBase, time.Millisecond)
	if rows := h.instanceRoutes.take(irBase, true); len(rows) != 0 {
		t.Fatalf("counted without platform flags: %d", len(rows))
	}
}
