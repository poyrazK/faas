package httpkit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestNotFound(t *testing.T) {
	rec := httptest.NewRecorder()
	NotFound(rec, "no such app")
	p := assertProblem(t, rec, http.StatusNotFound, api.CodeNotFound)
	if p.Detail != "no such app" {
		t.Fatalf("detail = %q", p.Detail)
	}
}

func TestRouteRemovalBlockedProblem(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantCode   string
		wantDetail string
	}{
		{name: "nil", err: nil},
		{name: "unrelated", err: errors.New("boom")},
		{name: "unrelated constraint", err: &pgconn.PgError{ConstraintName: "apps_slug_key"}},
		{
			name:       "route gate blocked",
			err:        fmt.Errorf("promote: %w", &state.RouteGateBlockedError{Decision: api.RouteGateDecision{Reasons: []string{"a", "b"}}}),
			wantCode:   api.CodeRouteGateBlocked,
			wantDetail: "a, b",
		},
		{
			name:       "lifecycle constraint",
			err:        &pgconn.PgError{ConstraintName: "production_lifecycle_required"},
			wantCode:   api.CodeRouteGateBlocked,
			wantDetail: "The transaction has no current lifecycle authorization.",
		},
		{
			name:       "route removal blocked",
			err:        fmt.Errorf("shift: %w", &state.RouteRemovalBlockedError{Reason: "  missing approval  "}),
			wantCode:   api.CodeRouteRemovalRequired,
			wantDetail: "missing approval",
		},
		{
			name:       "route removal constraint",
			err:        &pgconn.PgError{ConstraintName: "route_removal_required", Detail: "baseline changed"},
			wantCode:   api.CodeRouteRemovalRequired,
			wantDetail: "baseline changed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := RouteRemovalBlockedProblem(tt.err)
			if tt.wantCode == "" {
				if p != nil {
					t.Fatalf("problem = %+v, want nil", p)
				}
				return
			}
			if p == nil {
				t.Fatal("problem = nil")
			}
			if p.Status != http.StatusConflict || p.Code != tt.wantCode || p.Detail != tt.wantDetail {
				t.Fatalf("problem = status %d code %q detail %q", p.Status, p.Code, p.Detail)
			}
		})
	}
}

func TestCustomerProblems(t *testing.T) {
	gate := &state.RouteRemovalBlockedError{Reason: "blocked"}
	tests := []struct {
		name       string
		build      func(*slog.Logger) *api.Problem
		wantStatus int
		wantCode   string
		wantLogged bool
	}{
		{
			name: "internal hides cause and logs it",
			build: func(l *slog.Logger) *api.Problem {
				return CustomerInternalProblem(l, "op", "d", "h", errors.New("secret-db-text"))
			},
			wantStatus: http.StatusInternalServerError,
			wantCode:   api.CodeInternal,
			wantLogged: true,
		},
		{
			name: "internal deadline is a retryable 503",
			build: func(l *slog.Logger) *api.Problem {
				return CustomerInternalProblem(l, "op", "d", "h", fmt.Errorf("secret-db-text: %w", context.DeadlineExceeded))
			},
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   api.CodeCapacity,
			wantLogged: true,
		},
		{
			name:       "internal keeps route removal 409",
			build:      func(l *slog.Logger) *api.Problem { return CustomerInternalProblem(l, "op", "d", "h", gate) },
			wantStatus: http.StatusConflict,
			wantCode:   api.CodeRouteRemovalRequired,
		},
		{
			name: "capacity is retryable 503",
			build: func(l *slog.Logger) *api.Problem {
				return CustomerCapacityProblem(l, "op", "t", "d", "h", errors.New("secret-db-text"))
			},
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   api.CodeCapacity,
			wantLogged: true,
		},
		{
			name:       "capacity keeps route removal 409",
			build:      func(l *slog.Logger) *api.Problem { return CustomerCapacityProblem(l, "op", "t", "d", "h", gate) },
			wantStatus: http.StatusConflict,
			wantCode:   api.CodeRouteRemovalRequired,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var logs bytes.Buffer
			p := tt.build(slog.New(slog.NewJSONHandler(&logs, nil)))
			if p.Status != tt.wantStatus || p.Code != tt.wantCode {
				t.Fatalf("problem = status %d code %q", p.Status, p.Code)
			}
			if strings.Contains(p.Detail, "secret-db-text") {
				t.Fatalf("detail leaked cause: %q", p.Detail)
			}
			if got := strings.Contains(logs.String(), "secret-db-text"); got != tt.wantLogged {
				t.Fatalf("cause logged = %v, want %v (logs %s)", got, tt.wantLogged, logs.String())
			}
		})
	}
}

func TestCustomerCapacityProblemRetryAfter(t *testing.T) {
	rec := httptest.NewRecorder()
	api.WriteProblem(rec, CustomerCapacityProblem(nil, "op", "t", "d", "h", errors.New("x")))
	if got := rec.Header().Get("Retry-After"); got != "5" {
		t.Fatalf("Retry-After = %q, want 5", got)
	}
}

func TestWriteCustomerInternalProblem(t *testing.T) {
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/v1/apps/x", nil)
	WriteCustomerInternalProblem(rec, r, nil, "op", "could not load", "retry", errors.New("x"))
	assertProblem(t, rec, http.StatusInternalServerError, api.CodeInternal)
}

func TestLogCustomerFailureNilSafe(t *testing.T) {
	LogCustomerFailure(nil, "op", errors.New("x"))
	var logs bytes.Buffer
	LogCustomerFailure(slog.New(slog.NewJSONHandler(&logs, nil)), "op", nil)
	if logs.Len() != 0 {
		t.Fatalf("logged a nil cause: %s", logs.String())
	}
}
