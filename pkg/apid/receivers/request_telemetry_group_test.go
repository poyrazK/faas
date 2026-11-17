// adr: 127 — request telemetry group commit.

package receivers

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// batchTelemetryStore adds the group write to consumerTelemetryStore.
type batchTelemetryStore struct {
	*consumerTelemetryStore
	batches  [][]state.RequestTelemetryInsert
	batchErr error
	// refuse fails the single-row write of these event IDs.
	refuse map[string]bool
}

func (s *batchTelemetryStore) InsertRequestTelemetryBatchWithLogEvents(_ context.Context, rows []state.RequestTelemetryInsert) error {
	if s.batchErr != nil {
		return s.batchErr
	}
	s.batches = append(s.batches, rows)
	return nil
}

func (s *batchTelemetryStore) InsertRequestTelemetryWithLogEvent(ctx context.Context, arg sqlc.InsertRequestTelemetryParams, eventID string) error {
	if s.refuse[eventID] {
		return errors.New("check violation")
	}
	return s.consumerTelemetryStore.InsertRequestTelemetryWithLogEvent(ctx, arg, eventID)
}

// telemetryStream feeds records to the receiver and collects its outcomes.
// With lockstep, each record after the first waits for the previous
// outcome, as a client that waits per record would.
type telemetryStream struct {
	grpc.ServerStream
	ctx      context.Context
	records  []*apidpb.IncrementRequestTelemetryRequest
	next     int
	lockstep bool
	sent     chan *apidpb.IncrementRequestTelemetryResponse
	outs     []*apidpb.IncrementRequestTelemetryResponse
}

func newTelemetryStream(ctx context.Context, records []*apidpb.IncrementRequestTelemetryRequest, lockstep bool) *telemetryStream {
	return &telemetryStream{ctx: ctx, records: records, lockstep: lockstep, sent: make(chan *apidpb.IncrementRequestTelemetryResponse, len(records))}
}

func (s *telemetryStream) Context() context.Context { return s.ctx }

func (s *telemetryStream) Recv() (*apidpb.IncrementRequestTelemetryRequest, error) {
	if s.next >= len(s.records) {
		if s.lockstep && s.next > 0 {
			select {
			case <-s.sent:
			case <-s.ctx.Done():
				return nil, s.ctx.Err()
			}
		}
		return nil, io.EOF
	}
	if s.lockstep && s.next > 0 {
		select {
		case <-s.sent:
		case <-s.ctx.Done():
			return nil, s.ctx.Err()
		}
	}
	s.next++
	return s.records[s.next-1], nil
}

func (s *telemetryStream) Send(out *apidpb.IncrementRequestTelemetryResponse) error {
	s.outs = append(s.outs, out)
	s.sent <- out
	return nil
}

func groupTelemetryRecords(n int) ([]*apidpb.IncrementRequestTelemetryRequest, *batchTelemetryStore) {
	accountID, appID, deploymentID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	store := &batchTelemetryStore{consumerTelemetryStore: &consumerTelemetryStore{account: state.Account{ID: accountID, Plan: api.PlanPro}}}
	records := make([]*apidpb.IncrementRequestTelemetryRequest, n)
	for i := range records {
		records[i] = rollupTelemetryRequest(accountID, appID, deploymentID, time.Now())
	}
	return records, store
}

// A gateway batch (records, then a half-close) commits its rows in one
// transaction and gets every outcome back in record order.
func TestRequestTelemetryCommitsAGatewayBatchOnce(t *testing.T) {
	records, store := groupTelemetryRecords(10)
	receiver := NewRequestTelemetryReceiver(store, nil, nil, true)
	stream := newTelemetryStream(t.Context(), records, false)
	if err := receiver.IncrementRequestTelemetry(stream); err != nil {
		t.Fatal(err)
	}
	if len(store.batches) != 1 || len(store.batches[0]) != 10 || len(store.inserted) != 0 {
		t.Fatalf("batches %d (first %d rows), single inserts %d; want one batch of 10 and none alone", len(store.batches), len(store.batches[0]), len(store.inserted))
	}
	for i, out := range stream.outs {
		if out.GetOutcome() != RtOutcomeInserted || store.batches[0][i].EventID != records[i].GetEventId() {
			t.Fatalf("record %d: outcome %q, batch event %s; want inserted in record order", i, out.GetOutcome(), store.batches[0][i].EventID)
		}
	}
	if len(stream.outs) != 10 {
		t.Fatalf("outcomes = %d, want 10", len(stream.outs))
	}
}

// A failed group write falls back to one write per row, so only the row
// Postgres refuses is a db_error.
func TestRequestTelemetryGroupFallsBackToRowsOnFailure(t *testing.T) {
	records, store := groupTelemetryRecords(5)
	store.batchErr = errors.New("serialization failure")
	store.refuse = map[string]bool{records[2].GetEventId(): true}
	receiver := NewRequestTelemetryReceiver(store, nil, nil, true)
	stream := newTelemetryStream(t.Context(), records, false)
	if err := receiver.IncrementRequestTelemetry(stream); err != nil {
		t.Fatal(err)
	}
	if len(stream.outs) != 5 || len(store.inserted) != 4 {
		t.Fatalf("outcomes %d, rows written alone %d; want 5 and 4", len(stream.outs), len(store.inserted))
	}
	for i, out := range stream.outs {
		want := RtOutcomeInserted
		if i == 2 {
			want = rtOutcomeDBError
		}
		if out.GetOutcome() != want {
			t.Fatalf("record %d outcome = %q, want %q", i, out.GetOutcome(), want)
		}
	}
}

// A client that waits for each outcome before sending the next record is
// answered after the group wait instead of waiting for a half-close.
func TestRequestTelemetryAnswersAClientThatWaitsPerRecord(t *testing.T) {
	records, store := groupTelemetryRecords(3)
	receiver := NewRequestTelemetryReceiver(store, nil, nil, true)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	stream := newTelemetryStream(ctx, records, true)
	if err := receiver.IncrementRequestTelemetry(stream); err != nil {
		t.Fatal(err)
	}
	if len(stream.outs) != 3 || len(store.inserted) != 3 || len(store.batches) != 0 {
		t.Fatalf("outcomes %d, single inserts %d, batches %d; want each record answered on its own", len(stream.outs), len(store.inserted), len(store.batches))
	}
}

// A group never exceeds the bound, however fast records arrive.
func TestRequestTelemetryGroupsStayWithinTheBound(t *testing.T) {
	n := api.RequestTelemetryReceiveGroupMax + 10
	records, store := groupTelemetryRecords(n)
	receiver := NewRequestTelemetryReceiver(store, nil, nil, true)
	stream := newTelemetryStream(t.Context(), records, false)
	if err := receiver.IncrementRequestTelemetry(stream); err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, b := range store.batches {
		if len(b) > api.RequestTelemetryReceiveGroupMax {
			t.Fatalf("batch of %d rows, bound %d", len(b), api.RequestTelemetryReceiveGroupMax)
		}
		total += len(b)
	}
	if total+len(store.inserted) != n || len(stream.outs) != n {
		t.Fatalf("rows written %d, outcomes %d; want %d each", total+len(store.inserted), len(stream.outs), n)
	}
}
