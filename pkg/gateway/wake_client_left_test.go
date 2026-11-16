package gateway

// adr: 20261115140000 — a client that leaves during a wake is a 499, not a
// 503, and does not mark the app's health as failed.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWakeErrorAfterTheClientLeftIsNotAWakeFailure(t *testing.T) {
	for _, tc := range []struct {
		name       string
		clientGone bool
		wantWrite  bool
	}{
		{name: "client left", clientGone: true, wantWrite: false},
		{name: "client waiting", clientGone: false, wantWrite: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, b, _ := newTestHandler(t)
			ctx, cancel := context.WithCancel(context.Background())
			if tc.clientGone {
				cancel()
			} else {
				defer cancel()
			}
			req := httptest.NewRequest(http.MethodGet, "http://"+b.host+"/", nil).WithContext(ctx)
			rr := httptest.NewRecorder()
			rec := &statusRecorder{ResponseWriter: rr, status: http.StatusOK, request: req}
			h.writePublicRoutingWakeError(rec, req, b.app, rec, true, context.DeadlineExceeded)

			if rec.wroteHeader != tc.wantWrite {
				t.Fatalf("wrote a response = %v (status %d), want %v", rec.wroteHeader, rr.Code, tc.wantWrite)
			}
			_, marked := h.healthState.Load(b.app.ID)
			if marked != tc.wantWrite {
				t.Fatalf("app health marked failed = %v, want %v", marked, tc.wantWrite)
			}
		})
	}
}
