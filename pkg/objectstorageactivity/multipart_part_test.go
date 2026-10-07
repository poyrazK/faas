// adr: 590
package objectstorageactivity_test

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/objectstorageactivity"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
)

type partMutationJournal struct {
	dispatchErr error
	receipt     state.ObjectBucketMutation
	finished    bool
}

func (s *partMutationJournal) DispatchObjectMultipartPartMutation(ctx context.Context, b state.ObjectBucket, id string, part int32, token string) (state.ObjectBucketMutation, error) {
	return s.receipt, s.dispatchErr
}
func (s *partMutationJournal) FinishObjectMultipartPartMutation(ctx context.Context, r state.ObjectBucketMutation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, ok := ctx.Deadline(); !ok {
		return errors.New("unbounded settlement")
	}
	s.finished = true
	return nil
}

func TestMultipartPartActivityUsesActualJournalAndBoundedSettlement(t *testing.T) {
	ordinary := &mutationStore{beginErr: state.ErrObjectBucketWriteFenced}
	journal := &partMutationJournal{receipt: state.ObjectBucketMutation{ID: "original", MultipartPartWriterID: "original"}}
	r, err := objectstorageactivity.DispatchMultipartPart(t.Context(), ordinary, journal, state.ObjectBucket{}, "session", 1, "token")
	if err != nil || ordinary.active != 0 || r.ID != "original" {
		t.Fatal("admitted a second writer", r, err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := objectstorageactivity.FinishMultipartPart(cancelled, ordinary, journal, r); err != nil || !journal.finished {
		t.Fatal("known proof lost after cancellation", err)
	}
	if err := objectstorageactivity.FinishMultipartPart(t.Context(), ordinary, struct{}{}, r); !errors.Is(err, state.ErrConflict) {
		t.Fatal("generic fallback erased bound evidence", err)
	}
	journal.dispatchErr = state.ErrConflict
	if _, err := objectstorageactivity.DispatchMultipartPart(t.Context(), ordinary, journal, state.ObjectBucket{}, "session", 1, "token"); !errors.Is(err, state.ErrConflict) || ordinary.active != 0 {
		t.Fatal("rejected claim fell back to ordinary admission", err)
	}
	if _, err := objectstorageactivity.DispatchMultipartPart(t.Context(), ordinary, struct{}{}, state.ObjectBucket{}, "session", 1, "token"); !errors.Is(err, state.ErrObjectBucketWriteFenced) {
		t.Fatal("legacy admission bypassed hold", err)
	}
}

func TestMultipartPartCopyCannotDowngradeBoundJournal(t *testing.T) {
	ordinary := &mutationStore{}
	journal := &partMutationJournal{}
	if _, err := objectstorageactivity.DispatchMultipartPartCopy(t.Context(), ordinary, journal, state.ObjectBucket{}, "session", 1, "token", state.ObjectMultipartPartCopyIntent{}); !errors.Is(err, state.ErrConflict) || ordinary.active != 0 {
		t.Fatal("bound copy silently downgraded", err)
	}
	ordinary.beginErr = state.ErrObjectBucketWriteFenced
	if _, err := objectstorageactivity.DispatchMultipartPartCopy(t.Context(), ordinary, struct{}{}, state.ObjectBucket{}, "session", 1, "token", state.ObjectMultipartPartCopyIntent{}); !errors.Is(err, state.ErrObjectBucketWriteFenced) {
		t.Fatal("legacy copy bypassed hold", err)
	}
}

func TestMultipartPartPutCannotDowngradeBoundJournal(t *testing.T) {
	ordinary := &mutationStore{}
	journal := &partMutationJournal{}
	if _, err := objectstorageactivity.DispatchMultipartPartPut(t.Context(), ordinary, journal, state.ObjectBucket{}, "session", 1, "token", state.ObjectMultipartPartPutIntent{}); !errors.Is(err, state.ErrConflict) || ordinary.active != 0 {
		t.Fatal("bound PUT silently downgraded", err)
	}
	if err := objectstorageactivity.ObserveMultipartPartBody(t.Context(), journal, state.ObjectBucketMutation{MultipartPartWriterID: "writer"}, "digest"); !errors.Is(err, state.ErrConflict) {
		t.Fatal("missing body observer accepted", err)
	}
}

type putMutationJournal struct {
	partMutationJournal
	observed bool
}

func (s *putMutationJournal) DispatchObjectMultipartPartPutMutation(ctx context.Context, b state.ObjectBucket, id string, part int32, token string, i state.ObjectMultipartPartPutIntent) (state.ObjectBucketMutation, error) {
	return s.receipt, s.dispatchErr
}
func (s *putMutationJournal) ReadObjectMultipartPartPutIntent(ctx context.Context, r state.ObjectBucketMutation) (state.ObjectMultipartPartPutIntent, error) {
	return state.ObjectMultipartPartPutIntent{}, nil
}
func (s *putMutationJournal) ObserveObjectMultipartPartBody(ctx context.Context, r state.ObjectBucketMutation, digest string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, ok := ctx.Deadline(); !ok {
		return errors.New("unbounded body observation")
	}
	s.observed = true
	return nil
}
func TestMultipartPartPutActivityUsesActualJournalWithoutRetiringReceipt(t *testing.T) {
	ordinary := &mutationStore{beginErr: state.ErrObjectBucketWriteFenced}
	journal := &putMutationJournal{partMutationJournal: partMutationJournal{receipt: state.ObjectBucketMutation{ID: "original", MultipartPartWriterID: "original"}}}
	r, err := objectstorageactivity.DispatchMultipartPartPut(t.Context(), ordinary, journal, state.ObjectBucket{}, "session", 1, "token", state.ObjectMultipartPartPutIntent{})
	if err != nil || r.ID != "original" || ordinary.active != 0 {
		t.Fatal("PUT used wrong journal", r, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err = objectstorageactivity.ObserveMultipartPartBody(ctx, journal, r, "digest"); err != nil || !journal.observed || journal.finished || ordinary.active != 0 {
		t.Fatal("body observation lost evidence or retired receipt", err)
	}
	journal.dispatchErr = state.ErrConflict
	if _, err = objectstorageactivity.DispatchMultipartPartPut(t.Context(), ordinary, journal, state.ObjectBucket{}, "session", 1, "token", state.ObjectMultipartPartPutIntent{}); !errors.Is(err, state.ErrConflict) || ordinary.active != 0 {
		t.Fatal("rejected PUT claim fell back", err)
	}
}
