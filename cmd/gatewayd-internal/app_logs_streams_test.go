package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// An account's open log streams are bounded by its plan; nothing used to
// bound them, and each follow stream polls the shared database from schedd.
func TestLogStreamSlotsBoundAnAccount(t *testing.T) {
	slots := newLogStreamSlots()
	free := state.Account{ID: "acct-free", Plan: api.PlanFree}
	var releases []func()
	for i := 0; i < api.PlanFree.LogStreamsMax(); i++ {
		release, ok := slots.admit(httptest.NewRecorder(), free)
		if !ok {
			t.Fatalf("stream %d refused under the limit", i+1)
		}
		releases = append(releases, release)
	}
	rec := httptest.NewRecorder()
	if _, ok := slots.admit(rec, free); ok {
		t.Fatal("stream over the plan's limit admitted")
	}
	if rec.Code != http.StatusTooManyRequests || !strings.Contains(rec.Body.String(), `"limit":2`) || !strings.Contains(rec.Body.String(), `"observed":2`) {
		t.Fatalf("refusal = %d %s, want 429 with limit and observed", rec.Code, rec.Body)
	}
	if _, ok := slots.admit(httptest.NewRecorder(), state.Account{ID: "acct-other", Plan: api.PlanFree}); !ok {
		t.Fatal("another account refused by this account's streams")
	}
	releases[0]()
	releases[0]() // releasing twice frees one slot only
	if _, ok := slots.admit(httptest.NewRecorder(), free); !ok {
		t.Fatal("a released slot was not reusable")
	}
	if _, ok := slots.admit(httptest.NewRecorder(), free); ok {
		t.Fatal("a double release freed two slots")
	}
}
