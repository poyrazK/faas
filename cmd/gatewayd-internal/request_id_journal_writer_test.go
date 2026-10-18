package main

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/gateway"
)

type fakeJournalClient struct {
	batchErr   error
	batchResp  *apidpb.RecordRequestIDJournalBatchResponse
	batchCalls int
	batchSizes []int
	single     map[string]error // request id -> error; absent = recorded
	notRecord  map[string]bool  // request id -> apid answers recorded=false
	singleIDs  []string
}

func (f *fakeJournalClient) RecordRequestIDJournalBatch(_ context.Context, req *apidpb.RecordRequestIDJournalBatchRequest) (*apidpb.RecordRequestIDJournalBatchResponse, error) {
	f.batchCalls++
	f.batchSizes = append(f.batchSizes, len(req.GetRecords()))
	if f.batchErr != nil {
		return nil, f.batchErr
	}
	return f.batchResp, nil
}

func (f *fakeJournalClient) RecordRequestIDJournal(_ context.Context, req *apidpb.RecordRequestIDJournalRequest) (*apidpb.RecordRequestIDJournalResponse, error) {
	f.singleIDs = append(f.singleIDs, req.GetRequestId())
	if err := f.single[req.GetRequestId()]; err != nil {
		return nil, err
	}
	return &apidpb.RecordRequestIDJournalResponse{Recorded: !f.notRecord[req.GetRequestId()]}, nil
}

func journalRecords(ids ...string) []gateway.RequestIDJournalRecord {
	out := make([]gateway.RequestIDJournalRecord, 0, len(ids))
	for _, id := range ids {
		out = append(out, gateway.RequestIDJournalRecord{
			ID: uuid.NewString(), AccountID: uuid.NewString(), AppID: uuid.NewString(),
			RequestID: id, ReceivedAt: time.Now(),
		})
	}
	return out
}

// adr: 1521 — one batch RPC per flush; apid's not_entitled and rejected
// counts both surface as rejected, and a storage error fails the batch
// without falling back.
func TestRequestIDJournalWriterBatch(t *testing.T) {
	unavailable := status.Error(codes.Unavailable, "database down")
	for _, tc := range []struct {
		name    string
		client  *fakeJournalClient
		want    gateway.RequestIDJournalBatchResult
		wantErr bool
	}{
		{"counts apid outcomes", &fakeJournalClient{batchResp: &apidpb.RecordRequestIDJournalBatchResponse{Recorded: 2, NotEntitled: 1, Rejected: 1}},
			gateway.RequestIDJournalBatchResult{Recorded: 2, Rejected: 2}, false},
		{"storage error fails the batch", &fakeJournalClient{batchErr: fmt.Errorf("apidgrpc: %w", unavailable)},
			gateway.RequestIDJournalBatchResult{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newRequestIDJournalWriter(tc.client, nil)
			got, err := w.Write(context.Background(), journalRecords("a", "b", "c", "d"))
			if (err != nil) != tc.wantErr || got != tc.want {
				t.Fatalf("Write = %+v, %v; want %+v, error %v", got, err, tc.want, tc.wantErr)
			}
			if tc.client.batchCalls != 1 || len(tc.client.singleIDs) != 0 {
				t.Fatalf("batch calls = %d, single calls = %v; want 1 batch call and no single calls", tc.client.batchCalls, tc.client.singleIDs)
			}
		})
	}
}

// adr: 1521 — while apid answers Unimplemented the writer sends one RPC per
// record, skips the batch RPC for requestIDJournalBatchRetryAfter, and then
// tries it again.
func TestRequestIDJournalWriterFallsBackWhileApidLacksBatch(t *testing.T) {
	client := &fakeJournalClient{
		batchErr:  fmt.Errorf("apidgrpc: RecordRequestIDJournalBatch: %w", status.Error(codes.Unimplemented, "unknown method")),
		single:    map[string]error{"broken": errors.New("apid unavailable")},
		notRecord: map[string]bool{"free": true},
	}
	now := time.Now()
	w := newRequestIDJournalWriter(client, nil)
	w.now = func() time.Time { return now }

	got, err := w.Write(context.Background(), journalRecords("ok", "free", "broken"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if want := (gateway.RequestIDJournalBatchResult{Recorded: 1, Rejected: 1, Failed: 1}); got != want {
		t.Fatalf("Write = %+v, want %+v", got, want)
	}
	if _, err := w.Write(context.Background(), journalRecords("again")); err != nil {
		t.Fatalf("second Write: %v", err)
	}
	if client.batchCalls != 1 {
		t.Fatalf("batch calls inside the retry window = %d, want 1", client.batchCalls)
	}
	now = now.Add(requestIDJournalBatchRetryAfter)
	client.batchErr = nil
	client.batchResp = &apidpb.RecordRequestIDJournalBatchResponse{Recorded: 1}
	if got, err := w.Write(context.Background(), journalRecords("upgraded")); err != nil || got.Recorded != 1 {
		t.Fatalf("Write after the retry window = %+v, %v", got, err)
	}
	if client.batchCalls != 2 || len(client.singleIDs) != 4 {
		t.Fatalf("batch calls = %d, single calls = %v; want 2 and 4", client.batchCalls, client.singleIDs)
	}
}

func TestRequestIDJournalWriterWithoutClientFails(t *testing.T) {
	w := newRequestIDJournalWriter(nil, nil)
	if _, err := w.Write(context.Background(), journalRecords("a")); err == nil {
		t.Fatal("Write without an apid client succeeded")
	}
}
