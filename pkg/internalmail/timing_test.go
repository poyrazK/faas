package internalmail

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func timeRef(at time.Time) *time.Time { return &at }

func TestSchedulingDefaultOffBeforeStoreOrCrypto(t *testing.T) {
	for _, tc := range []struct{ master, flag string }{{"", "1"}, {"true", "1"}, {"1", ""}, {"1", "0"}, {"1", "true"}, {"1", "1 "}} {
		if SchedulingEnabled(func(key string) string {
			if key == EnableEnv {
				return tc.master
			}
			return tc.flag
		}) {
			t.Fatal("scheduling enabled without exact opt-in")
		}
	}
	s := &Service{Enabled: func() bool { return true }}
	for _, timing := range []MessageTiming{{SendAt: timeRef(time.Now().Add(time.Hour))}, {ExpiresAt: timeRef(time.Now().Add(time.Minute))}} {
		r := request()
		r.MessageTiming = timing
		if _, err := s.Submit(t.Context(), "disabled", r); !errors.Is(err, ErrDisabled) {
			t.Fatal("disabled timing accessed a store or codec", err)
		}
	}
	if err := s.DeliverOne(t.Context()); err != nil {
		t.Fatal("disabled workers accessed state", err)
	}
	h, err := Handler(s, testToken)
	if err != nil {
		t.Fatal(err)
	}
	r := request()
	r.ExpiresAt = timeRef(time.Now().Add(time.Minute))
	raw, _ := json.Marshal(r)
	req := httptest.NewRequest(http.MethodPost, APIPath+"/messages", strings.NewReader(string(raw)))
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "disabled-http")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatal("disabled timing request accepted", w.Code)
	}
	s.SchedulingEnabled = func() bool { return true }
	if _, err := s.claimMail(t.Context()); !errors.Is(err, ErrInvalid) {
		t.Fatal("timed worker accepted a store without timing fences", err)
	}
	if err := s.beginMailSubmission(t.Context(), Recipient{}, ""); !errors.Is(err, ErrInvalid) {
		t.Fatal("timed handoff accepted an unfenced store", err)
	}
}

func TestTimingCanonicalizationPreservesLegacyAndCallerValues(t *testing.T) {
	legacy, _, err := normalizeRequest(request())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(legacy)
	if strings.Contains(string(raw), "send_at") || strings.Contains(string(raw), "expires_at") {
		t.Fatal("legacy fingerprint format changed")
	}
	at := time.Date(2030, 1, 2, 3, 4, 5, 987654321, time.FixedZone("caller", 10800))
	original := at
	normalized, err := (MessageTiming{SendAt: &at, ExpiresAt: timeRef(at.Add(time.Hour))}).normalized()
	if err != nil || !at.Equal(original) || at.Location() != original.Location() || normalized.SendAt == &at || normalized.SendAt.Location() != time.UTC || normalized.SendAt.Nanosecond()%1000 != 0 {
		t.Fatal("timing normalization changed caller memory or lost canonical precision", err)
	}
	for _, invalid := range []MessageTiming{
		{SendAt: timeRef(time.Time{})}, {ExpiresAt: timeRef(time.Time{})},
		{SendAt: &at, ExpiresAt: &at}, {SendAt: &at, ExpiresAt: timeRef(at.Add(-time.Second))},
		{SendAt: &at, ExpiresAt: timeRef(at.Add(api.InternalMailMaxQueueAge + time.Second))},
		{SendAt: timeRef(time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC))},
	} {
		if _, err := invalid.normalized(); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid timing shape accepted", err)
		}
	}
}
