package main

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/api"
)

type fakeResourceMinutesClient struct {
	calls    [][]*apidpb.InstanceResourceMinute
	disabled []string
	err      error
}

func (f *fakeResourceMinutesClient) IngestInstanceResourceMinutes(_ context.Context, req *apidpb.IngestInstanceResourceMinutesRequest) (*apidpb.IngestInstanceResourceMinutesResponse, error) {
	f.calls = append(f.calls, req.GetMinutes())
	if f.err != nil {
		return nil, f.err
	}
	return &apidpb.IngestInstanceResourceMinutesResponse{Outcome: "accepted", DisabledAppIds: f.disabled}, nil
}

func testResourceMinutes(client resourceMinutesClient) *resourceMinutes {
	return newResourceMinutes(func(instance string) (resourceIdentity, bool) {
		if instance == "gone" {
			return resourceIdentity{}, false
		}
		return resourceIdentity{AccountID: "acct", AppID: "app-" + instance, DeploymentID: "dep"}, true
	}, client, slog.Default())
}

func slot(start, peak, end int, cpuUsec int64, peakAgeMS int64) resourceSlotWire {
	return resourceSlotWire{Samples: 10, MemStartMB: start, MemPeakMB: peak, MemEndMB: end,
		MemLimitMB: 2048, CPUUsec: cpuUsec, CPUPeakMillicores: 400, CPULimitMillis: 1000, PeakAgeMS: peakAgeMS}
}

func TestResourceSlotValid(t *testing.T) {
	if !slot(100, 200, 150, 1000, 500).valid() {
		t.Fatal("good slot refused")
	}
	for name, w := range map[string]resourceSlotWire{
		"no samples":       {MemPeakMB: 1},
		"peak below end":   slot(100, 120, 150, 0, 0),
		"peak below start": slot(200, 120, 100, 0, 0),
		"negative cpu":     slot(1, 2, 1, -1, 0),
		"old peak":         slot(1, 2, 1, 0, 60_000),
		"huge memory":      slot(1, 1<<21, 1, 0, 0),
	} {
		if w.valid() {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestResourceMinutesFoldByHostClock(t *testing.T) {
	client := &fakeResourceMinutesClient{}
	m := testResourceMinutes(client)
	base := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	// Two slots of the same minute fold; the peak's own time decides the
	// minute, so a slot received at 10:01:03 whose peak was 5 s earlier
	// belongs to 10:00.
	m.observe("i1", slot(100, 300, 200, 2_000_000, 0), base.Add(20*time.Second))
	m.observe("i1", slot(200, 250, 220, 1_000_000, 5000), base.Add(63*time.Second))
	m.observe("i1", slot(220, 500, 230, 500_000, 0), base.Add(70*time.Second))
	m.observe("gone", slot(1, 2, 1, 0, 0), base) // not serving: dropped
	m.observe("i1", resourceSlotWire{}, base)    // invalid: dropped
	rows := m.take(base.Add(2*time.Minute+api.RouteResourceMinuteGrace), false)
	if len(rows) != 2 {
		t.Fatalf("rows = %d", len(rows))
	}
	first := rows[0]
	if first.GetMinuteUnix() != base.Unix() || first.GetMemStartMb() != 100 || first.GetMemPeakMb() != 300 ||
		first.GetMemEndMb() != 220 || first.GetCpuMs() != 3000 || first.GetSamples() != 20 || first.GetAppId() != "app-i1" {
		t.Fatalf("first minute = %+v", first)
	}
	if rows[1].GetMemPeakMb() != 500 || rows[1].GetMemStartMb() != 220 {
		t.Fatalf("second minute = %+v", rows[1])
	}
}

func TestResourceMinutesKeepsOpenMinutes(t *testing.T) {
	m := testResourceMinutes(&fakeResourceMinutesClient{})
	base := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	m.observe("i1", slot(1, 2, 1, 1000, 0), base.Add(30*time.Second))
	if rows := m.take(base.Add(70*time.Second), false); len(rows) != 0 {
		t.Fatalf("open minute sent: %d rows", len(rows))
	}
	if rows := m.take(base.Add(70*time.Second), true); len(rows) != 1 {
		t.Fatalf("final take = %d rows", len(rows))
	}
}

func TestResourceMinutesFlushBacksOffDisabledApps(t *testing.T) {
	client := &fakeResourceMinutesClient{disabled: []string{"app-i1"}}
	m := testResourceMinutes(client)
	now := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	m.observe("i1", slot(1, 2, 1, 1000, 0), now)
	m.flush(context.Background(), true)
	if len(client.calls) != 1 {
		t.Fatalf("calls = %d", len(client.calls))
	}
	m.observe("i1", slot(1, 2, 1, 1000, 0), now.Add(time.Minute))
	m.observe("i2", slot(1, 2, 1, 1000, 0), now.Add(time.Minute))
	if rows := m.take(now, true); len(rows) != 1 || rows[0].GetInstanceId() != "i2" {
		t.Fatalf("disabled app forwarded: %+v", rows)
	}
	// After the backoff the app is forwarded again.
	m.observe("i1", slot(1, 2, 1, 1000, 0), now.Add(6*time.Minute))
	if rows := m.take(now, true); len(rows) != 1 {
		t.Fatalf("after backoff = %d rows", len(rows))
	}
}

func TestResourceMinutesDropsFailedBatch(t *testing.T) {
	client := &fakeResourceMinutesClient{err: errors.New("down")}
	m := testResourceMinutes(client)
	m.observe("i1", slot(1, 2, 1, 1000, 0), time.Now())
	m.flush(context.Background(), true)
	m.flush(context.Background(), true)
	if len(client.calls) != 1 {
		t.Fatalf("a failed batch was resent: %d calls", len(client.calls))
	}
}
