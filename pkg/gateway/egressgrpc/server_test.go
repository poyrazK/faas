package egressgrpc

// adr: 046

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
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

// ADR-1736: a hand-off's old generation flushes what its sink holds into its
// own ledger; the generation serving meterd absorbs that ledger with the same
// event IDs, and absorbing it twice adds nothing.
func TestAbsorbMovesAnOldGenerationsLedgerWithItsEventIDs(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	oldSink := egresssink.NewEgressSinkWithClock(func() time.Time { return now })
	old, err := NewPersistentServer(oldSink, nil, dir+"/pending.json")
	if err != nil {
		t.Fatal(err)
	}
	oldSink.RecordResponseBytes("inst-old", 700)
	if err := old.Flush(); err != nil {
		t.Fatal(err)
	}
	oldRecords, err := old.replayRecords()
	if err != nil || len(oldRecords) != 1 {
		t.Fatalf("old ledger = %+v err=%v", oldRecords, err)
	}

	newSink := egresssink.NewEgressSinkWithClock(func() time.Time { return now })
	newSink.RecordResponseBytes("inst-new", 50)
	next, err := NewPersistentServer(newSink, nil, dir+"/.generation-1/pending.json")
	if err != nil {
		t.Fatal(err)
	}
	added, err := next.Absorb(dir + "/pending.json")
	if err != nil || added != 1 {
		t.Fatalf("absorb added %d err=%v, want 1", added, err)
	}
	if _, err := os.Stat(dir + "/pending.json"); !os.IsNotExist(err) {
		t.Fatalf("absorbed ledger still on disk: %v", err)
	}
	if added, err := next.Absorb(dir + "/pending.json"); err != nil || added != 0 {
		t.Fatalf("second absorb added %d err=%v", added, err)
	}
	replay, err := next.replayRecords()
	if err != nil || len(replay) != 2 {
		t.Fatalf("replay = %+v err=%v", replay, err)
	}
	var sawOld bool
	for _, r := range replay {
		if r.record.InstanceID == "inst-old" {
			sawOld = r.eventID == oldRecords[0].eventID && r.record.Bytes == 700
		}
	}
	if !sawOld {
		t.Fatalf("replay %+v lost the old generation's record or its event ID", replay)
	}
	// The absorbed record is durable in the new generation's ledger.
	reopened, err := NewPersistentServer(nil, nil, dir+"/.generation-1/pending.json")
	if err != nil || len(reopened.pending) != 2 {
		t.Fatalf("reopened ledger has %d records err=%v, want 2", len(reopened.pending), err)
	}
}

func TestMergeLedgerReturnsRecordsHome(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	sink := egresssink.NewEgressSinkWithClock(func() time.Time { return now })
	slot, err := NewPersistentServer(sink, nil, dir+"/.generation-1/pending.json")
	if err != nil {
		t.Fatal(err)
	}
	sink.RecordResponseBytes("inst-1", 9)
	if err := slot.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := MergeLedger(dir+"/.generation-1/pending.json", dir+"/pending.json", nil); err != nil {
		t.Fatal(err)
	}
	home, err := NewPersistentServer(nil, nil, dir+"/pending.json")
	if err != nil || len(home.pending) != 1 {
		t.Fatalf("home ledger has %d records err=%v, want 1", len(home.pending), err)
	}
}
