// adr: 1195
package acceptance_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type operationTimelineFixtureStore interface {
	operationControlFixtureStore
	state.OperationExecutionStampStore
	state.OperationTimelineStore
}

func readOperationTimeline(t *testing.T, s state.OperationTimelineStore, account, id string, limit int) []api.OperationTimelineEntry {
	t.Helper()
	var all []api.OperationTimelineEntry
	cursor := state.OperationTimelineCursor{}
	for range 100 {
		page, err := s.OperationTimeline(t.Context(), account, id, cursor, limit)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Response.Entries) > limit || page.Response.OperationID != id {
			t.Fatalf("page shape: %+v", page.Response)
		}
		all = append(all, page.Response.Entries...)
		if page.Next == nil {
			return all
		}
		cursor = *page.Next
	}
	t.Fatal("timeline never ended")
	return nil
}

func operationTimelineKey(e api.OperationTimelineEntry) string {
	switch {
	case e.Event != nil:
		return fmt.Sprintf("event/%d", e.Event.Sequence)
	case e.Attempt != nil:
		return fmt.Sprintf("%s/%d/%s/%d", e.Kind, e.Attempt.Generation, e.Attempt.Step, e.Attempt.Attempt)
	case e.Delivery != nil:
		return fmt.Sprintf("delivery/%d/%d", e.Delivery.ReplayGeneration, e.Delivery.AttemptNumber)
	}
	return "malformed/" + e.Kind
}

func testOperationTimeline(t *testing.T, s operationTimelineFixtureStore) {
	ctx := t.Context()
	op, first, proof := operationControlClaim(t, s)
	app, err := s.AppByID(ctx, proof.AppID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.StampOperationExecutionAttempt(ctx, first.ID, proof.InstanceID, first.Attempts); err != nil {
		t.Fatal(err)
	}
	if err := s.FailInvocation(ctx, first.ID, "wake unavailable", time.Millisecond, 10, state.WithClaimAttempt(first.Attempts), state.WithDispatchNotStarted()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	second, err := s.CreateInstance(ctx, app.ID, op.DeploymentID, "stopped", 128, mustLocalNode(t, s), "")
	if err != nil {
		t.Fatal(err)
	}
	retry, err := s.ClaimInvocationWithCap(ctx, first.ID, second.ID, 60, 100)
	if err != nil || retry.Attempts != 2 {
		t.Fatalf("retry claim: %+v %v", retry, err)
	}
	if err := s.StampOperationExecutionAttempt(ctx, retry.ID, second.ID, retry.Attempts); err != nil {
		t.Fatal(err)
	}
	// A repeated stamp of the same attempt keeps its first placement.
	if err := s.StampOperationExecutionAttempt(ctx, retry.ID, proof.InstanceID, retry.Attempts); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteKeyedInvocation(ctx, retry.ID, retry.Attempts, []byte(`{"file":"export.csv"}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.StampOperationExecutionAttempt(ctx, retry.ID, proof.InstanceID, retry.Attempts); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("stamp after completion: %v", err)
	}

	whole := readOperationTimeline(t, s, proof.AccountID, op.ID, api.OperationHistoryPageMax)
	for _, limit := range []int{1, 2, 3} {
		paged := readOperationTimeline(t, s, proof.AccountID, op.ID, limit)
		if len(paged) != len(whole) {
			t.Fatalf("limit %d read %d entries, whole read %d", limit, len(paged), len(whole))
		}
		for i := range paged {
			if operationTimelineKey(paged[i]) != operationTimelineKey(whole[i]) {
				t.Fatalf("limit %d entry %d = %s, want %s", limit, i, operationTimelineKey(paged[i]), operationTimelineKey(whole[i]))
			}
		}
	}
	seen := map[string]bool{}
	var attempts []api.OperationTimelineAttempt
	var events []string
	for i, e := range whole {
		key := operationTimelineKey(e)
		if seen[key] {
			t.Fatalf("duplicate entry %s", key)
		}
		seen[key] = true
		if i > 0 && e.At.Before(whole[i-1].At) {
			t.Fatalf("entry %d (%s) precedes its predecessor", i, key)
		}
		switch e.Kind {
		case api.OperationTimelineKindAttempt:
			attempts = append(attempts, *e.Attempt)
		case api.OperationTimelineKindEvent:
			events = append(events, e.Event.Type)
		default:
			t.Fatalf("unexpected entry %s", key)
		}
	}
	if len(events) == 0 || events[0] != "accepted" || events[len(events)-1] != "succeeded" {
		t.Fatalf("events = %v", events)
	}
	if len(attempts) != 2 {
		t.Fatalf("attempts = %+v", attempts)
	}
	failed, succeeded := attempts[0], attempts[1]
	if failed.Attempt != 1 || failed.Outcome != api.OperationAttemptRetry || failed.Error != "wake unavailable" || failed.FinishedAt == nil || failed.ExecutionID != first.ID || failed.Generation != 1 {
		t.Fatalf("first attempt: %+v", failed)
	}
	if succeeded.Attempt != 2 || succeeded.Outcome != api.OperationAttemptSucceeded || succeeded.FinishedAt == nil {
		t.Fatalf("second attempt: %+v", succeeded)
	}
	for i, want := range []string{proof.InstanceID, second.ID} {
		logs := attempts[i].Logs
		if logs == nil || !sameTimelineUUID(logs.InstanceID, want) || logs.Correlation != api.OperationAttemptLogCorrelation || !logs.Since.Equal(attempts[i].StartedAt) || logs.Until == nil || !logs.Until.Equal(*attempts[i].FinishedAt) || len(logs.ArchiveDates) == 0 {
			t.Fatalf("attempt %d logs = %+v, want instance %s", i+1, logs, want)
		}
		if logs.ArchiveDates[len(logs.ArchiveDates)-1] != attempts[i].FinishedAt.UTC().Format(time.DateOnly) {
			t.Fatalf("attempt %d archive days = %v", i+1, logs.ArchiveDates)
		}
	}

	if _, err := s.OperationTimeline(ctx, uuid.NewString(), op.ID, state.OperationTimelineCursor{}, 10); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("foreign account read timeline: %v", err)
	}
	for _, bad := range []state.OperationTimelineCursor{{Event: -1}, {Attempt: state.OperationTimelineAttemptKey{Generation: -1}}, {Delivery: state.OperationTimelineAttemptKey{Attempt: -1}}} {
		if _, err := s.OperationTimeline(ctx, proof.AccountID, op.ID, bad, 10); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("cursor %+v: %v", bad, err)
		}
	}
	for _, limit := range []int{0, api.OperationHistoryPageMax + 1} {
		if _, err := s.OperationTimeline(ctx, proof.AccountID, op.ID, state.OperationTimelineCursor{}, limit); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("limit %d: %v", limit, err)
		}
	}
	if _, err := s.OperationTimeline(ctx, proof.AccountID, op.ID, state.OperationTimelineCursor{Event: whole[len(whole)-1].Event.Sequence + 100}, 10); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("cursor beyond the latest event: %v", err)
	}
}

func sameTimelineUUID(a, b string) bool {
	x, errX := uuid.Parse(a)
	y, errY := uuid.Parse(b)
	return errX == nil && errY == nil && x == y
}

func mustLocalNode(t *testing.T, s operationTimelineFixtureStore) string {
	t.Helper()
	if node, err := s.ComputeNodeByName(t.Context(), state.DefaultLocalNodeName); err == nil {
		return node.ID
	}
	return uuid.NewString()
}

func TestMemOperationTimeline(t *testing.T) { testOperationTimeline(t, state.NewMemStore()) }
func TestPgOperationTimeline(t *testing.T) {
	s, _ := pgStore(t)
	testOperationTimeline(t, s)
}
