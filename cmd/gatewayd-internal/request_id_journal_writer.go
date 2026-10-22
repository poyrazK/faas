package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/gateway"
)

// requestIDJournalRPCTimeout bounds one journal RPC to apid.
const requestIDJournalRPCTimeout = 2 * time.Second

// requestIDJournalBatchRetryAfter is how long the writer uses the per-record
// RPC after apid answered Unimplemented for the batch RPC. Compute nodes can
// roll out before the control plane's apid does (ADR-1521).
const requestIDJournalBatchRetryAfter = 5 * time.Minute

// requestIDJournalClient is the apid surface the journal writer needs.
type requestIDJournalClient interface {
	RecordRequestIDJournal(context.Context, *apidpb.RecordRequestIDJournalRequest) (*apidpb.RecordRequestIDJournalResponse, error)
	RecordRequestIDJournalBatch(context.Context, *apidpb.RecordRequestIDJournalBatchRequest) (*apidpb.RecordRequestIDJournalBatchResponse, error)
}

// requestIDJournalWriter writes the gateway's journal batches to apid with
// one RPC per batch, falling back to one RPC per record while apid predates
// RecordRequestIDJournalBatch.
type requestIDJournalWriter struct {
	client requestIDJournalClient
	log    *slog.Logger
	now    func() time.Time
	// singleUntil is the unix-nano time before which the batch RPC is skipped.
	singleUntil atomic.Int64
}

func newRequestIDJournalWriter(client requestIDJournalClient, log *slog.Logger) *requestIDJournalWriter {
	if log == nil {
		log = slog.Default()
	}
	return &requestIDJournalWriter{client: client, log: log, now: time.Now}
}

// Write is a gateway.RequestIDJournalBatchWriter.
func (w *requestIDJournalWriter) Write(ctx context.Context, records []gateway.RequestIDJournalRecord) (gateway.RequestIDJournalBatchResult, error) {
	if w.client == nil {
		return gateway.RequestIDJournalBatchResult{}, errors.New("request ID journal apid client unavailable")
	}
	if w.now().UnixNano() >= w.singleUntil.Load() {
		result, err := w.writeBatch(ctx, records)
		if status.Code(err) != codes.Unimplemented {
			return result, err
		}
		w.singleUntil.Store(w.now().Add(requestIDJournalBatchRetryAfter).UnixNano())
		w.log.Info("request ID journal: apid has no batch RPC; writing one record per RPC", "retry_after", requestIDJournalBatchRetryAfter)
	}
	return w.writeEach(ctx, records), nil
}

func (w *requestIDJournalWriter) writeBatch(ctx context.Context, records []gateway.RequestIDJournalRecord) (gateway.RequestIDJournalBatchResult, error) {
	request := &apidpb.RecordRequestIDJournalBatchRequest{Records: make([]*apidpb.RecordRequestIDJournalRequest, 0, len(records))}
	for _, record := range records {
		request.Records = append(request.Records, requestIDJournalWire(record))
	}
	rpcCtx, cancel := context.WithTimeout(ctx, requestIDJournalRPCTimeout)
	defer cancel()
	response, err := w.client.RecordRequestIDJournalBatch(rpcCtx, request)
	if err != nil {
		return gateway.RequestIDJournalBatchResult{}, fmt.Errorf("persist request ID journal batch: %w", err)
	}
	return gateway.RequestIDJournalBatchResult{
		Recorded: int(response.GetRecorded()),
		Rejected: int(response.GetNotEntitled() + response.GetRejected()),
	}, nil
}

// writeEach is the pre-ADR-1521 path: one RPC per record, each with its own
// deadline. A record apid did not record (plan without debugger retention)
// counts as rejected.
func (w *requestIDJournalWriter) writeEach(ctx context.Context, records []gateway.RequestIDJournalRecord) gateway.RequestIDJournalBatchResult {
	var result gateway.RequestIDJournalBatchResult
	for _, record := range records {
		rpcCtx, cancel := context.WithTimeout(ctx, requestIDJournalRPCTimeout)
		response, err := w.client.RecordRequestIDJournal(rpcCtx, requestIDJournalWire(record))
		cancel()
		switch {
		case err != nil:
			result.Failed++
			w.log.Debug("request ID journal: record write failed", "app_id", record.AppID, "err", err)
		case response.GetRecorded():
			result.Recorded++
		default:
			result.Rejected++
		}
	}
	return result
}

func requestIDJournalWire(record gateway.RequestIDJournalRecord) *apidpb.RecordRequestIDJournalRequest {
	return &apidpb.RecordRequestIDJournalRequest{
		RecordId: record.ID, AccountId: record.AccountID, AppId: record.AppID,
		RequestId: record.RequestID, TraceId: record.TraceID,
		ReceivedAtUnixMs: record.ReceivedAt.UnixMilli(),
	}
}
