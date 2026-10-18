package receivers

import (
	"context"
	"errors"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/state"
)

// requestIDJournalBatchLimit bounds one RecordRequestIDJournalBatch call. A
// gateway flushes far fewer records per interval; the cap keeps one call's
// arrays and its single statement small.
const requestIDJournalBatchLimit = 512

// RecordRequestIDJournalBatch commits a gateway's buffered journal records
// with one account lookup per distinct account and one multi-row insert
// (ADR-1521). One gateway used to cost apid an RPC, an account read and an
// insert for every customer request. A malformed record, a repeated record id
// or an unknown account is counted as rejected rather than failing the
// records around it; a storage failure fails the whole call so the gateway can
// count every record in it as failed.
func (r *requestTelemetryReceiver) RecordRequestIDJournalBatch(ctx context.Context, req *apidpb.RecordRequestIDJournalBatchRequest) (*apidpb.RecordRequestIDJournalBatchResponse, error) {
	records := req.GetRecords()
	if len(records) == 0 || len(records) > requestIDJournalBatchLimit {
		return nil, status.Errorf(codes.InvalidArgument, "a request ID journal batch holds 1..%d records", requestIDJournalBatchLimit)
	}
	journal, ok := r.store.(state.RequestIDJournalBatchWriter)
	if !ok {
		return nil, status.Error(codes.Unavailable, "durable request ID journal is unavailable")
	}
	response := &apidpb.RecordRequestIDJournalBatchResponse{}
	entries, err := r.entitledRequestIDJournalEntries(ctx, records, response)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return response, nil
	}
	recorded, err := journal.RecordRequestIDJournalBatch(ctx, entries)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "persist request ID journal batch: %v", err)
	}
	// The insert leaves out rows whose app is not owned by the account and
	// ids that already hold different content.
	response.Recorded = int32(len(recorded))
	response.Rejected += int32(len(entries) - len(recorded))
	return response, nil
}

// entitledRequestIDJournalEntries validates records and keeps the ones whose
// account plan retains debugger data, counting the rest on response. Each
// distinct account is read once per call.
func (r *requestTelemetryReceiver) entitledRequestIDJournalEntries(ctx context.Context, records []*apidpb.RecordRequestIDJournalRequest, response *apidpb.RecordRequestIDJournalBatchResponse) ([]state.RequestIDJournalEntry, error) {
	now := time.Now().UTC()
	retention := map[string]time.Duration{}
	seen := make(map[string]bool, len(records))
	entries := make([]state.RequestIDJournalEntry, 0, len(records))
	for _, record := range records {
		entry, err := parseRequestIDJournalRecord(record, now)
		if err != nil || seen[entry.ID] {
			response.Rejected++
			continue
		}
		seen[entry.ID] = true
		keep, known := retention[entry.AccountID]
		if !known {
			account, err := r.store.AccountByID(ctx, entry.AccountID)
			switch {
			case errors.Is(err, state.ErrNotFound):
				keep = -1
			case err != nil:
				return nil, status.Errorf(codes.Unavailable, "resolve request ID journal account: %v", err)
			default:
				keep = requestIDJournalRetention(account.Plan)
			}
			retention[entry.AccountID] = keep
		}
		switch {
		case keep < 0:
			response.Rejected++
		case keep == 0:
			response.NotEntitled++
		default:
			entry.ExpiresAt = entry.ReceivedAt.Add(keep)
			entries = append(entries, entry)
		}
	}
	return entries, nil
}
