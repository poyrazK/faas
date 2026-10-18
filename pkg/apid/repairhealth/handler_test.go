package repairhealth_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/repairhealth"
)

type healthReader func(context.Context, time.Time, string, int) (api.DataRepairHealth, error)

func (f healthReader) DataRepairHealth(ctx context.Context, at time.Time, cursor string, limit int) (api.DataRepairHealth, error) {
	return f(ctx, at, cursor, limit)
}

func TestRepairHealthHTTPProjection(t *testing.T) {
	cursor := "t:030e2012-e15a-4205-9ede-d9796f2b7544"
	called := false
	h := repairhealth.Handler{Store: healthReader(func(ctx context.Context, now time.Time, after string, limit int) (api.DataRepairHealth, error) {
		called = true
		deadline, ok := ctx.Deadline()
		if !ok || deadline.Sub(now) > time.Duration(api.DataRepairHealthQuerySeconds)*time.Second || after != cursor || limit != 2 {
			t.Fatalf("unbounded or incorrect request: %s %d", after, limit)
		}
		return api.DataRepairHealth{ObservedAt: now, Limit: limit, HeldSlots: 1, HeldMemoryMB: 520, NextCursor: cursor, Items: []api.DataRepairHealthItem{{TaskID: "task", Phase: "cleanup", FailureCode: "repair_cleanup_unconfirmed", CapacityHeld: true}}}, nil
	})}
	r := httptest.NewRequest(http.MethodGet, "/?limit=2&cursor="+cursor, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var got api.DataRepairHealth
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !called || w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" || got.HeldMemoryMB != 520 || got.Items[0].FailureCode != "repair_cleanup_unconfirmed" || got.NextCursor != cursor {
		t.Fatalf("response: %d %s", w.Code, w.Body.String())
	}
}

func TestRepairHealthHTTPValidationAndUnavailable(t *testing.T) {
	for _, tc := range []struct {
		name, query string
		missing     bool
		status      int
	}{
		{"bad cursor", "?cursor=t:invalid", false, http.StatusBadRequest},
		{"zero UUID", "?cursor=o:00000000-0000-0000-0000-000000000000", false, http.StatusBadRequest},
		{"wrong cursor kind", "?cursor=x:030e2012-e15a-4205-9ede-d9796f2b7544", false, http.StatusBadRequest},
		{"noncanonical UUID", "?cursor=t:030E2012-E15A-4205-9EDE-D9796F2B7544", false, http.StatusBadRequest},
		{"bad limit", "?limit=abc", false, http.StatusBadRequest},
		{"zero limit", "?limit=0", false, http.StatusBadRequest},
		{"oversized limit", "?limit=501", false, http.StatusBadRequest},
		{"read failed", "", false, http.StatusServiceUnavailable},
		{"missing reader", "", true, http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			h := repairhealth.Handler{Store: healthReader(func(context.Context, time.Time, string, int) (api.DataRepairHealth, error) {
				calls++
				return api.DataRepairHealth{}, errors.New("postgres://private:password@database/source-values")
			})}
			if tc.missing {
				h.Store = nil
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/"+tc.query, nil))
			if w.Code != tc.status || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "password") || strings.Contains(w.Body.String(), `"held_slots":0`) {
				t.Fatalf("response: %d %s", w.Code, w.Body.String())
			}
			if tc.status == http.StatusBadRequest && calls != 0 {
				t.Fatal("invalid query reached the store")
			}
		})
	}
}
