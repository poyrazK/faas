package receivers

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type countingAppErrorsStore struct {
	increments []sqlc.IncrementAppErrorParams
	kept       int64
	plan       api.Plan
}

func (s *countingAppErrorsStore) IncrementAppError(_ context.Context, arg sqlc.IncrementAppErrorParams) (bool, error) {
	s.increments = append(s.increments, arg)
	return len(s.increments) == 1, nil
}

func (s *countingAppErrorsStore) InsertAppErrorRequest(context.Context, sqlc.InsertAppErrorRequestParams) error {
	s.kept++
	return nil
}

func (s *countingAppErrorsStore) AccountByID(_ context.Context, id string) (state.Account, error) {
	return state.Account{ID: id, Plan: s.plan}, nil
}

func (s *countingAppErrorsStore) CountAppErrorRequestsForFingerprint(context.Context, sqlc.CountAppErrorRequestsForFingerprintParams) (int64, error) {
	return s.kept, nil
}

// A gateway record stands for count requests, a count-only record stores no
// request row, and an error group stops gaining request rows at the plan's
// AppErrorsMaxRequestRowsPerFingerprint (bug hunt 2026-10-28 H41).
func TestAppErrorsReceiverCountsFoldedRequestsAndCapsRequestRows(t *testing.T) {
	store := &countingAppErrorsStore{plan: api.PlanFree}
	receiver := NewAppErrorsReceiver(store, nil, true)
	record := func(count int64, omitted bool) {
		t.Helper()
		out := receiver.HandleOne(context.Background(), &apidpb.IncrementAppErrorRequest{
			AccountId: uuid.NewString(), AppId: uuid.NewString(), RouteTemplate: "GET /wp-login.php",
			HttpStatus: 404, ErrorClass: "not_found", Fingerprint: strings.Repeat("a", 64),
			Count: count, RequestRowOmitted: omitted,
		})
		if out.GetOutcome() != OutcomeInserted && out.GetOutcome() != "merged" {
			t.Fatalf("outcome = %q", out.GetOutcome())
		}
	}

	record(0, false) // an older gateway: one request
	record(500, false)
	record(40, true)
	if len(store.increments) != 3 || store.increments[0].Count != 1 || store.increments[1].Count != 500 || store.increments[2].Count != 40 {
		t.Fatalf("increments = %+v, want counts 1, 500, 40", store.increments)
	}
	if store.kept != 2 {
		t.Fatalf("request rows = %d, want 2 (the count-only record stores none)", store.kept)
	}

	limit := int64(api.PlanFree.AppErrorsMaxRequestRowsPerFingerprint())
	for store.kept < limit+5 {
		before := store.kept
		record(1, false)
		if store.kept == before {
			break
		}
	}
	if store.kept != limit {
		t.Fatalf("request rows = %d, want the Free cap %d", store.kept, limit)
	}
}
