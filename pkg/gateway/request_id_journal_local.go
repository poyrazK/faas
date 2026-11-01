// adr: 20261101085552
package gateway

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/requestjournal"
)

// LocalRequestIDJournalWriter writes journal batches to the node-local store
// instead of apid. A record whose plan keeps no debugger data (zero ExpiresAt)
// is rejected, as apid rejects it.
func LocalRequestIDJournalWriter(store *requestjournal.Store) RequestIDJournalBatchWriter {
	return func(_ context.Context, batch []RequestIDJournalRecord) (RequestIDJournalBatchResult, error) {
		var result RequestIDJournalBatchResult
		records := make([]requestjournal.Record, 0, len(batch))
		for _, record := range batch {
			account, accountErr := uuid.Parse(record.AccountID)
			app, appErr := uuid.Parse(record.AppID)
			if accountErr != nil || appErr != nil || record.ExpiresAt.IsZero() {
				result.Rejected++
				continue
			}
			records = append(records, requestjournal.Record{
				AccountID: account, AppID: app, RequestID: record.RequestID, TraceID: record.TraceID,
				ReceivedAt: record.ReceivedAt, ExpiresAt: record.ExpiresAt,
			})
		}
		skipped, err := store.Append(records)
		if err != nil {
			return RequestIDJournalBatchResult{}, err
		}
		result.Recorded += len(records) - skipped
		result.Rejected += skipped
		return result, nil
	}
}

// requestIDLookupUnimplementedWait is how long a gateway waits before asking
// again when apid predates the lookup service.
const requestIDLookupUnimplementedWait = 5 * time.Minute

// ServeRequestIDLookups holds a lookup stream open to apid and answers each
// question from the node-local journal, reconnecting with backoff until ctx
// ends.
func ServeRequestIDLookups(ctx context.Context, client apidpb.RequestIDLookupsClient, node string, store *requestjournal.Store, log *slog.Logger) {
	if log == nil {
		log = slog.Default()
	}
	backoff := time.Second
	for ctx.Err() == nil {
		established, err := serveRequestIDLookupStream(ctx, client, node, store)
		if ctx.Err() != nil {
			return
		}
		if established {
			backoff = time.Second
		}
		wait := backoff
		if status.Code(err) == codes.Unimplemented {
			wait = requestIDLookupUnimplementedWait
		}
		log.Warn("request ID lookups: stream to apid ended; reconnecting", "err", err, "wait", wait)
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		backoff = min(2*backoff, time.Minute)
	}
}

// serveRequestIDLookupStream runs one stream; established reports whether apid
// took the gateway's first message.
func serveRequestIDLookupStream(ctx context.Context, client apidpb.RequestIDLookupsClient, node string, store *requestjournal.Store) (bool, error) {
	stream, err := client.Serve(ctx)
	if err != nil {
		return false, err
	}
	if err := stream.Send(&apidpb.RequestIDLookupAnswer{Node: node}); err != nil {
		return false, err
	}
	for {
		question, err := stream.Recv()
		if err != nil {
			return true, err
		}
		if err := stream.Send(answerRequestIDLookup(store, node, question)); err != nil {
			return true, err
		}
	}
}

func answerRequestIDLookup(store *requestjournal.Store, node string, question *apidpb.RequestIDLookupQuestion) *apidpb.RequestIDLookupAnswer {
	answer := &apidpb.RequestIDLookupAnswer{QuestionId: question.GetQuestionId(), Node: node}
	account, accountErr := uuid.Parse(question.GetAccountId())
	app, appErr := uuid.Parse(question.GetAppId())
	if accountErr != nil || appErr != nil {
		answer.Error = "account and app must be UUIDs"
		return answer
	}
	record, found, err := store.Lookup(account, app, question.GetRequestId(),
		time.UnixMilli(question.GetReceivedFromUnixMs()), time.UnixMilli(question.GetReceivedUntilUnixMs()))
	if err != nil {
		answer.Error = err.Error()
		return answer
	}
	if found {
		answer.Found, answer.RequestId, answer.TraceId = true, record.RequestID, record.TraceID
		answer.ReceivedAtUnixMs, answer.ExpiresAtUnixMs = record.ReceivedAt.UnixMilli(), record.ExpiresAt.UnixMilli()
	}
	return answer
}
