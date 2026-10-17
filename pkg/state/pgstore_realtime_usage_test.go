package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

func usagePeaks(values map[int]int32) []int32 {
	peaks := make([]int32, state.RealtimeUsageMinutes)
	for minute, v := range values {
		peaks[minute] = v
	}
	return peaks
}

func TestPgStoreRealtimeUsageRecordsAndAggregatesFleetHours(t *testing.T) {
	s, ctx := pgStore(t)
	accountID, appID, _ := seedLiveDeploy(t, s, ctx, "-rt-usage")
	endpoint, err := s.CreateManagedRealtimeEndpointIfUnderQuota(ctx, pgManagedRealtimeEndpoint(accountID, appID), 10, 50)
	if err != nil {
		t.Fatal(err)
	}
	hour := time.Now().UTC().Truncate(time.Hour)
	nodeA, nodeB := uuid.NewString(), uuid.NewString()
	report := func(node, incarnation string, h time.Time, usage state.RealtimeEndpointUsage) int {
		t.Helper()
		n, err := s.RecordRealtimeUsage(ctx, state.RealtimeUsageReport{NodeName: node, Incarnation: incarnation, Hour: h, Endpoints: []state.RealtimeEndpointUsage{usage}})
		if err != nil {
			t.Fatalf("RecordRealtimeUsage: %v", err)
		}
		return n
	}

	// Node A reports twice; the second report carries larger cumulative values.
	first := state.RealtimeEndpointUsage{EndpointID: endpoint.ID, MinutePeakConnections: usagePeaks(map[int]int32{0: 3, 1: 5}),
		ConnectionSeconds: 100, DeliveredMessages: 10, DeliveredBytes: 1000, ReceivedMessages: 2, ReceivedBytes: 20}
	if n := report("node-a", nodeA, hour, first); n != 1 {
		t.Fatalf("recorded = %d, want 1", n)
	}
	later := first
	later.MinutePeakConnections = usagePeaks(map[int]int32{0: 3, 1: 5, 2: 4})
	later.ConnectionSeconds, later.DeliveredMessages, later.DeliveredBytes = 160, 15, 1500
	report("node-a", nodeA, hour, later)
	// A stale, out-of-order repeat of the first report must not lower anything.
	report("node-a", nodeA, hour, first)
	// Node B overlaps node A in minute 1; a restart of node B starts a new row.
	report("node-b", nodeB, hour, state.RealtimeEndpointUsage{EndpointID: endpoint.ID, MinutePeakConnections: usagePeaks(map[int]int32{1: 2, 2: 1}),
		ConnectionSeconds: 40, DeliveredMessages: 5, DeliveredBytes: 500})
	report("node-b", uuid.NewString(), hour, state.RealtimeEndpointUsage{EndpointID: endpoint.ID, MinutePeakConnections: usagePeaks(map[int]int32{5: 1}),
		ConnectionSeconds: 10, DeliveredMessages: 1, DeliveredBytes: 100})
	// Unknown endpoints are skipped, not failed.
	if n := report("node-a", nodeA, hour, state.RealtimeEndpointUsage{EndpointID: uuid.NewString(), MinutePeakConnections: usagePeaks(nil)}); n != 0 {
		t.Fatalf("unknown endpoint recorded %d rows", n)
	}
	// The previous hour stays separate.
	report("node-a", nodeA, hour.Add(-time.Hour), state.RealtimeEndpointUsage{EndpointID: endpoint.ID, MinutePeakConnections: usagePeaks(map[int]int32{59: 7}), ConnectionSeconds: 7})

	usage, err := s.ReadRealtimeUsage(ctx, accountID, hour.Add(-time.Hour), hour.Add(time.Hour))
	if err != nil {
		t.Fatalf("ReadRealtimeUsage: %v", err)
	}
	if len(usage) != 2 {
		t.Fatalf("usage rows = %+v, want 2 hours", usage)
	}
	if prev := usage[0]; !prev.Hour.Equal(hour.Add(-time.Hour)) || prev.PeakConnections != 7 || prev.ConnectionSeconds != 7 {
		t.Fatalf("previous hour = %+v", prev)
	}
	got := usage[1]
	want := state.RealtimeUsageHour{EndpointID: endpoint.ID, AppID: appID, Hour: hour,
		PeakConnections:   7, // minute 1: node A 5 + node B 2
		ConnectionSeconds: 210, DeliveredMessages: 21, DeliveredBytes: 2100, ReceivedMessages: 2, ReceivedBytes: 20}
	if got != want {
		t.Fatalf("current hour = %+v, want %+v", got, want)
	}
	if only, err := s.ReadRealtimeUsage(ctx, accountID, hour, hour.Add(time.Hour)); err != nil || len(only) != 1 {
		t.Fatalf("range read = %+v, %v, want only the current hour", only, err)
	}
}

func TestValidateRealtimeUsageReport(t *testing.T) {
	now := time.Date(2026, 10, 14, 10, 30, 0, 0, time.UTC)
	valid := func() state.RealtimeUsageReport {
		return state.RealtimeUsageReport{NodeName: "node-a", Incarnation: uuid.NewString(), Hour: now.Truncate(time.Hour),
			Endpoints: []state.RealtimeEndpointUsage{{EndpointID: uuid.NewString(), MinutePeakConnections: usagePeaks(nil)}}}
	}
	cases := []struct {
		name   string
		mutate func(*state.RealtimeUsageReport)
		ok     bool
	}{
		{name: "valid", mutate: func(*state.RealtimeUsageReport) {}, ok: true},
		{name: "day-old hour", mutate: func(r *state.RealtimeUsageReport) { r.Hour = r.Hour.Add(-24 * time.Hour) }, ok: true},
		{name: "too old", mutate: func(r *state.RealtimeUsageReport) { r.Hour = r.Hour.Add(-26 * time.Hour) }},
		{name: "future hour", mutate: func(r *state.RealtimeUsageReport) { r.Hour = r.Hour.Add(time.Hour) }},
		{name: "unaligned hour", mutate: func(r *state.RealtimeUsageReport) { r.Hour = r.Hour.Add(time.Minute) }},
		{name: "no node", mutate: func(r *state.RealtimeUsageReport) { r.NodeName = "" }},
		{name: "bad incarnation", mutate: func(r *state.RealtimeUsageReport) { r.Incarnation = "x" }},
		{name: "no endpoints", mutate: func(r *state.RealtimeUsageReport) { r.Endpoints = nil }},
		{name: "short peaks", mutate: func(r *state.RealtimeUsageReport) { r.Endpoints[0].MinutePeakConnections = []int32{1} }},
		{name: "negative peak", mutate: func(r *state.RealtimeUsageReport) { r.Endpoints[0].MinutePeakConnections[3] = -1 }},
		{name: "negative count", mutate: func(r *state.RealtimeUsageReport) { r.Endpoints[0].DeliveredBytes = -1 }},
		{name: "duplicate endpoint", mutate: func(r *state.RealtimeUsageReport) { r.Endpoints = append(r.Endpoints, r.Endpoints[0]) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := valid()
			tc.mutate(&r)
			err := state.ValidateRealtimeUsageReport(r, now)
			if tc.ok != (err == nil) || (err != nil && !errors.Is(err, state.ErrRealtimeUsageInvalid)) {
				t.Fatalf("err = %v, want ok=%v", err, tc.ok)
			}
		})
	}
}

func TestPgStoreRealtimeAccountUsageAndOpenConnections(t *testing.T) {
	s, ctx := pgStore(t)
	accountID, appID, _ := seedLiveDeploy(t, s, ctx, "-rt-account-usage")
	first, err := s.CreateManagedRealtimeEndpointIfUnderQuota(ctx, pgManagedRealtimeEndpoint(accountID, appID), 10, 50)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.CreateManagedRealtimeEndpointIfUnderQuota(ctx, pgManagedRealtimeEndpoint(accountID, appID), 10, 50)
	if err != nil {
		t.Fatal(err)
	}
	hour := time.Now().UTC().Truncate(time.Hour)
	if _, err := s.RecordRealtimeUsage(ctx, state.RealtimeUsageReport{NodeName: "node-a", Incarnation: uuid.NewString(), Hour: hour, Endpoints: []state.RealtimeEndpointUsage{
		{EndpointID: first.ID, MinutePeakConnections: usagePeaks(map[int]int32{0: 4, 1: 1}), ConnectionSeconds: 100, DeliveredMessages: 3},
		{EndpointID: second.ID, MinutePeakConnections: usagePeaks(map[int]int32{0: 2, 1: 6}), ConnectionSeconds: 50, DeliveredMessages: 2},
	}}); err != nil {
		t.Fatal(err)
	}
	hours, err := s.ReadRealtimeAccountUsage(ctx, accountID, hour, hour.Add(time.Hour))
	if err != nil {
		t.Fatalf("ReadRealtimeAccountUsage: %v", err)
	}
	// Minute 0: 4+2=6, minute 1: 1+6=7; the per-endpoint peaks (4, 6) must not be added.
	if len(hours) != 1 || hours[0].PeakConnections != 7 || hours[0].ConnectionSeconds != 150 || hours[0].DeliveredMessages != 5 {
		t.Fatalf("account hours = %+v, want peak 7 and summed counts", hours)
	}

	if n, err := s.ReadRealtimeOpenConnections(ctx, accountID); err != nil || n != 0 {
		t.Fatalf("open connections before any sync = %d, %v", n, err)
	}
	for node, count := range map[string]int64{"node-a": 3, "node-b": 5} {
		if _, err := s.SyncRealtimeNodeConnections(ctx, node, []state.RealtimeAccountConnections{{AccountID: accountID, Connections: count}}); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := s.ReadRealtimeOpenConnections(ctx, accountID); err != nil || n != 8 {
		t.Fatalf("open connections = %d, %v, want 8 across nodes", n, err)
	}
	if _, err := s.ReadRealtimeOpenConnections(ctx, "not-a-uuid"); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("bad account err = %v", err)
	}
}
