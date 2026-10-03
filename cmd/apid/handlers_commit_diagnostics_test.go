package main

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

type commitReceiptFailureStore struct {
	state.Store
	state.CommitStore
}

func (*commitReceiptFailureStore) CommitReceiptByEvent(context.Context, string, string, string) (state.CommitReceipt, error) {
	return state.CommitReceipt{}, errors.New("private database error")
}

func TestCommitReceiptReadFailureDoesNotReportUnaccepted(t *testing.T) {
	s := &server{store: &commitReceiptFailureStore{}}
	r := httptest.NewRequest("GET", "/", nil)
	r.SetPathValue("source", uuid.NewString())
	r.SetPathValue("event", uuid.NewString())
	w := httptest.NewRecorder()
	s.getCommitReceipt(w, r, state.Account{ID: uuid.NewString()})
	if w.Code != 503 || strings.Contains(w.Body.String(), "private database error") || strings.Contains(w.Body.String(), "commit_event_not_found") {
		t.Fatalf("read failure became an absent receipt: %d %s", w.Code, w.Body.String())
	}
}
