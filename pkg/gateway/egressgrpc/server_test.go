package egressgrpc

// adr: 046

import (
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	egresspb "github.com/onebox-faas/faas/api/proto/onebox/faas/egress/v1"
	"github.com/onebox-faas/faas/pkg/gateway/egresssink"
)

func TestSendRecordsRestoresFrameWhenStreamSendFails(t *testing.T) {
	now := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
	sink := egresssink.NewEgressSinkWithClock(func() time.Time { return now })
	sink.RecordResponseBytes("inst-1", 123)
	sink.RecordRequest("inst-1", true)
	records := sink.DrainRecords()

	server := NewServer(sink, slog.New(slog.NewTextHandler(io.Discard, nil)))
	wantErr := errors.New("stream closed")
	err := server.sendRecords(records, func(*egresspb.BytesFrame) error { return wantErr })
	if !errors.Is(err, wantErr) {
		t.Fatalf("sendRecords error = %v, want %v", err, wantErr)
	}
	if server.FramesSent() != 0 {
		t.Fatalf("frames sent = %d, want 0", server.FramesSent())
	}
	restored := sink.DrainRecords()
	if len(restored) != 1 || restored[0].Bytes != 123 || restored[0].Requests != 1 || restored[0].ColdBoots != 1 {
		t.Fatalf("restored records = %+v, want original frame", restored)
	}
}
