package egressgrpc

// adr: 20261104132753 — bytes served without an instance drain as edge frames
// that reach only a meterd that asked for them, and survive a ledger restart.

import (
	"testing"
	"time"

	egresspb "github.com/onebox-faas/faas/api/proto/onebox/faas/egress/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway/egresssink"
)

func TestEdgeFramesGoOnlyToClientsThatAskForThem(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 4, 13, 0, 0, 0, time.UTC)
	sink := egresssink.NewEgressSinkWithClock(func() time.Time { return now })
	sink.RecordResponseBytes("inst-1", 10)
	sink.RecordEdgeBytes("app-1", 1<<20)
	path := t.TempDir() + "/pending.json"
	server, err := NewPersistentServer(sink, nil, path)
	if err != nil {
		t.Fatal(err)
	}
	records, err := server.replayRecords()
	if err != nil {
		t.Fatal(err)
	}
	var old []*egresspb.BytesFrame
	if err := server.sendRecords(records, false, func(f *egresspb.BytesFrame) error { old = append(old, f); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(old) != 1 || old[0].GetInstanceId() != "inst-1" || old[0].GetAppId() != "" {
		t.Fatalf("frames for a client without edge frames = %+v, want only the instance frame", old)
	}

	// The edge record stays pending across a restart for a client that asks.
	restarted, err := NewPersistentServer(egresssink.NewEgressSinkWithClock(func() time.Time { return now }), nil, path)
	if err != nil {
		t.Fatal(err)
	}
	records, err = restarted.replayRecords()
	if err != nil {
		t.Fatal(err)
	}
	var edge *egresspb.BytesFrame
	if err := restarted.sendRecords(records, true, func(f *egresspb.BytesFrame) error {
		if f.GetAppId() != "" {
			edge = f
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if edge == nil || edge.GetAppId() != "app-1" || edge.GetInstanceId() != api.EdgeUsageInstanceID("app-1") || edge.GetBytes() != 1<<20 {
		t.Fatalf("edge frame = %+v, want app-1's bytes under its edge usage ID", edge)
	}
}
