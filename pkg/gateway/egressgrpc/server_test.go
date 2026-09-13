package egressgrpc

// adr: 046

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	egresspb "github.com/onebox-faas/faas/api/proto/onebox/faas/egress/v1"
	"github.com/onebox-faas/faas/pkg/gateway/egresssink"
)

func TestSendRecordsReplaysFrameWhenStreamSendFails(t *testing.T) {
	now := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
	sink := egresssink.NewEgressSinkWithClock(func() time.Time { return now })
	sink.RecordResponseBytes("inst-1", 123)
	sink.RecordRequest("inst-1", true)
	server := NewServer(sink, slog.New(slog.NewTextHandler(io.Discard, nil)))
	records, err := server.replayRecords()
	if err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("stream closed")
	err = server.sendRecords(records, func(*egresspb.BytesFrame) error { return wantErr })
	if !errors.Is(err, wantErr) {
		t.Fatalf("sendRecords error = %v, want %v", err, wantErr)
	}
	if server.FramesSent() != 0 {
		t.Fatalf("frames sent = %d, want 0", server.FramesSent())
	}
	replayed, err := server.replayRecords()
	if err != nil {
		t.Fatal(err)
	}
	if len(replayed) != 1 || replayed[0].eventID != records[0].eventID || replayed[0].record.Bytes != 123 || replayed[0].record.Requests != 1 || replayed[0].record.ColdBoots != 1 {
		t.Fatalf("replayed records = %+v, want original frame and stable event id", replayed)
	}
	ack, err := server.AckBytes(context.Background(), &egresspb.AckBytesRequest{EventIds: []string{records[0].eventID}})
	if err != nil || ack.GetAcknowledged() != 1 {
		t.Fatalf("ack = %+v err=%v", ack, err)
	}
	afterAck, err := server.replayRecords()
	if err != nil || len(afterAck) != 0 {
		t.Fatalf("post-ack replay=%+v err=%v", afterAck, err)
	}
}

func TestPersistentServerReplaysAcrossProcessRestartUntilAck(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 13, 12, 30, 0, 0, time.UTC)
	path := t.TempDir() + "/pending.json"
	sink := egresssink.NewEgressSinkWithClock(func() time.Time { return now })
	sink.RecordResponseBytes("inst-1", 321)
	first, err := NewPersistentServer(sink, nil, path)
	if err != nil {
		t.Fatal(err)
	}
	records, err := first.replayRecords()
	if err != nil || len(records) != 1 {
		t.Fatalf("initial replay=%+v err=%v", records, err)
	}

	second, err := NewPersistentServer(egresssink.NewEgressSinkWithClock(func() time.Time { return now }), nil, path)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := second.replayRecords()
	if err != nil || len(replayed) != 1 || replayed[0].eventID != records[0].eventID || replayed[0].record.Bytes != 321 {
		t.Fatalf("restart replay=%+v err=%v", replayed, err)
	}
	if _, err := second.AckBytes(context.Background(), &egresspb.AckBytesRequest{EventIds: []string{records[0].eventID}}); err != nil {
		t.Fatal(err)
	}
	third, err := NewPersistentServer(egresssink.NewEgressSink(), nil, path)
	if err != nil {
		t.Fatal(err)
	}
	afterAck, err := third.replayRecords()
	if err != nil || len(afterAck) != 0 {
		t.Fatalf("replay after durable ACK=%+v err=%v", afterAck, err)
	}
}
