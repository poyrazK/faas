package receivers

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// journalBatchStore serves several accounts and models the batch insert's
// ownership join: an entry is recorded only when owned[entry.AppID] is its
// account.
type journalBatchStore struct {
	consumerTelemetryStore
	plans      map[string]api.Plan
	owned      map[string]string
	lookupErr  error
	lookups    map[string]int
	batches    [][]state.RequestIDJournalEntry
	batchError error
}

func (s *journalBatchStore) AccountByID(_ context.Context, id string) (state.Account, error) {
	if s.lookups == nil {
		s.lookups = map[string]int{}
	}
	s.lookups[id]++
	if s.lookupErr != nil {
		return state.Account{}, s.lookupErr
	}
	plan, ok := s.plans[id]
	if !ok {
		return state.Account{}, state.ErrNotFound
	}
	return state.Account{ID: id, Plan: plan}, nil
}

func (s *journalBatchStore) RecordRequestIDJournalBatch(_ context.Context, entries []state.RequestIDJournalEntry) ([]string, error) {
	if s.batchError != nil {
		return nil, s.batchError
	}
	s.batches = append(s.batches, entries)
	var recorded []string
	for _, entry := range entries {
		if s.owned[entry.AppID] == entry.AccountID {
			recorded = append(recorded, entry.ID)
		}
	}
	return recorded, nil
}

func journalRecord(accountID, appID, requestID string, receivedAt time.Time) *apidpb.RecordRequestIDJournalRequest {
	return &apidpb.RecordRequestIDJournalRequest{
		RecordId: uuid.NewString(), AccountId: accountID, AppId: appID,
		RequestId: requestID, ReceivedAtUnixMs: receivedAt.UnixMilli(),
	}
}

// adr: 1521 — a batch reads each account once, records entitled rows in one
// insert, and counts malformed, repeated, unknown-account, not-owned and
// not-entitled records instead of failing the records around them.
func TestRecordRequestIDJournalBatchCountsEachOutcome(t *testing.T) {
	pro, free, unknown := uuid.NewString(), uuid.NewString(), uuid.NewString()
	proApp, freeApp, otherApp := uuid.NewString(), uuid.NewString(), uuid.NewString()
	store := &journalBatchStore{
		plans: map[string]api.Plan{pro: api.PlanPro, free: api.PlanFree},
		owned: map[string]string{proApp: pro, freeApp: free, otherApp: uuid.NewString()},
	}
	receiver := NewRequestTelemetryReceiver(store, nil, nil, false)
	now := time.Now().UTC().Truncate(time.Millisecond)
	repeated := journalRecord(pro, proApp, "repeated", now)
	malformed := journalRecord(pro, proApp, strings.Repeat("x", 129), now)
	records := []*apidpb.RecordRequestIDJournalRequest{
		journalRecord(pro, proApp, "first", now),
		repeated,
		repeated,
		journalRecord(free, freeApp, "free-plan", now),
		journalRecord(unknown, uuid.NewString(), "unknown-account", now),
		journalRecord(pro, otherApp, "not-owned", now),
		malformed,
	}
	response, err := receiver.RecordRequestIDJournalBatch(context.Background(), &apidpb.RecordRequestIDJournalBatchRequest{Records: records})
	if err != nil {
		t.Fatalf("RecordRequestIDJournalBatch: %v", err)
	}
	if response.GetRecorded() != 2 || response.GetNotEntitled() != 1 || response.GetRejected() != 4 {
		t.Fatalf("response = %+v, want recorded 2, not_entitled 1, rejected 4", response)
	}
	for account, n := range store.lookups {
		if n != 1 {
			t.Errorf("account %s looked up %d times, want 1", account, n)
		}
	}
	if len(store.batches) != 1 || len(store.batches[0]) != 3 {
		t.Fatalf("batches = %+v, want one insert of 3 entries", store.batches)
	}
	for _, entry := range store.batches[0] {
		if want := now.Add(7 * 24 * time.Hour); !entry.ExpiresAt.Equal(want) {
			t.Errorf("entry %s expires %s, want %s", entry.RequestID, entry.ExpiresAt, want)
		}
	}
}

// adr: 1521 — size bounds are InvalidArgument; lookup and storage failures
// fail the whole call as Unavailable so the gateway counts it as failed.
func TestRecordRequestIDJournalBatchErrors(t *testing.T) {
	account, app := uuid.NewString(), uuid.NewString()
	now := time.Now().UTC()
	oneRecord := []*apidpb.RecordRequestIDJournalRequest{journalRecord(account, app, "id", now)}
	tooMany := make([]*apidpb.RecordRequestIDJournalRequest, requestIDJournalBatchLimit+1)
	for i := range tooMany {
		tooMany[i] = journalRecord(account, app, "id", now)
	}
	for _, tc := range []struct {
		name    string
		records []*apidpb.RecordRequestIDJournalRequest
		store   *journalBatchStore
		want    codes.Code
	}{
		{"empty batch", nil, &journalBatchStore{}, codes.InvalidArgument},
		{"over the limit", tooMany, &journalBatchStore{}, codes.InvalidArgument},
		{"account lookup fails", oneRecord, &journalBatchStore{lookupErr: errors.New("database down")}, codes.Unavailable},
		{"insert fails", oneRecord, &journalBatchStore{
			plans:      map[string]api.Plan{account: api.PlanPro},
			batchError: errors.New("database down"),
		}, codes.Unavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			receiver := NewRequestTelemetryReceiver(tc.store, nil, nil, false)
			_, err := receiver.RecordRequestIDJournalBatch(context.Background(), &apidpb.RecordRequestIDJournalBatchRequest{Records: tc.records})
			if status.Code(err) != tc.want {
				t.Fatalf("error = %v, want %s", err, tc.want)
			}
		})
	}
}
