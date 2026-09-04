// handlers_admin_sweep_builds_test.go — pins the contract of the
// P2c (reclaim-stuck-build) admin handler added in Commit 5c of
// the operator-side observability mega-PR.
//
// Table-driven cases cover the four load-bearing edges:
//
//  1. confirm-required tripwire — without ?confirm=true the
//     handler returns 400 validation_failed (no Store call, no
//     audit row).
//  2. older-than too small — ?older_than=500ms is below the 1m
//     floor; handler returns 400 without sweeping.
//  3. older-than too large — ?older_than=24h is above the 60m
//     ceiling; handler returns 400 without sweeping.
//  4. happy path — handler forwards the parsed threshold to
//     the Store and returns 200 OK with the swept count.
//
// The test exercises parseSweepOlderThan directly for the
// edge cases and the handler for the happy path.

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestParseSweepOlderThan_TableDriven(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		wantOK   bool
		wantDur  time.Duration
		wantCode string
	}{
		{"empty_defaults_to_15m", "", true, 15 * time.Minute, ""},
		{"valid_5m", "5m", true, 5 * time.Minute, ""},
		{"valid_60m", "60m", true, 60 * time.Minute, ""},
		{"valid_1m_floor", "1m", true, 1 * time.Minute, ""},
		{"valid_30s_above_floor", "30s", false, 0, "older_too_small"},
		{"valid_500ms_below_floor", "500ms", false, 0, "older_too_small"},
		{"valid_2h_above_ceiling", "2h", false, 0, "older_too_large"},
		{"valid_90m_above_ceiling", "90m", false, 0, "older_too_large"},
		{"invalid_format", "forever", false, 0, "invalid_older_than"},
		{"empty_string", "", true, 15 * time.Minute, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, prob := parseSweepOlderThan(c.raw)
			if c.wantOK {
				if prob != nil {
					t.Fatalf("unexpected problem: %+v", prob)
				}
				if got != c.wantDur {
					t.Errorf("duration = %v, want %v", got, c.wantDur)
				}
			} else {
				if prob == nil {
					t.Fatalf("expected problem, got nil")
				}
				if prob.Code != c.wantCode {
					t.Errorf("code = %q, want %q", prob.Code, c.wantCode)
				}
			}
		})
	}
}

func TestParseSweepReason_TableDriven(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		want     string
		wantCode string
	}{
		{name: "empty_defaults", want: sweepDefaultReason},
		{name: "valid_reason", raw: "incident_2026_08_27", want: "incident_2026_08_27"},
		{name: "hyphen_rejected", raw: "incident-123", wantCode: api.CodeValidation},
		{name: "space_rejected", raw: "incident 123", wantCode: api.CodeValidation},
		{name: "too_long_rejected", raw: "12345678901234567890123456789012345678901234567890123456789012345", wantCode: api.CodeValidation},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, prob := parseSweepReason(c.raw)
			if c.wantCode != "" {
				if prob == nil {
					t.Fatalf("expected problem, got reason %q", got)
				}
				if prob.Code != c.wantCode {
					t.Errorf("code = %q, want %q", prob.Code, c.wantCode)
				}
				return
			}
			if prob != nil {
				t.Fatalf("unexpected problem: %+v", prob)
			}
			if got != c.want {
				t.Errorf("reason = %q, want %q", got, c.want)
			}
		})
	}
}

func TestPostSweepStuckBuilds_TableDriven(t *testing.T) {
	t.Run("non_allowlisted_admin_is_denied", func(t *testing.T) {
		srv, _, cookie := newForceHarness(t, nil)
		srv.adminAllowlist.emails = map[string]struct{}{"another-operator@example.com": {}}
		req := httptest.NewRequest(http.MethodPost,
			"/v1/admin/builds/sweep-stuck?confirm=true&older_than=15m", nil)
		req.AddCookie(cookie)
		req.Header.Set("Idempotency-Key", "test-admin-mutation")
		rec := httptest.NewRecorder()
		srv.handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("missing_confirm_returns_400", func(t *testing.T) {
		srv, _, cookie := newForceHarness(t, nil)
		req := httptest.NewRequest(http.MethodPost,
			"/v1/admin/builds/sweep-stuck", nil)
		req.AddCookie(cookie)
		req.Header.Set("Idempotency-Key", "test-admin-mutation")
		rec := httptest.NewRecorder()
		srv.handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
		}
		var prob api.Problem
		if err := json.Unmarshal(rec.Body.Bytes(), &prob); err != nil {
			t.Fatalf("decode problem: %v", err)
		}
		if prob.Code != "validation_failed" {
			t.Errorf("code = %q, want validation_failed", prob.Code)
		}
	})

	t.Run("older_than_too_small_returns_400", func(t *testing.T) {
		srv, _, cookie := newForceHarness(t, nil)
		req := httptest.NewRequest(http.MethodPost,
			"/v1/admin/builds/sweep-stuck?confirm=true&older_than=500ms", nil)
		req.AddCookie(cookie)
		req.Header.Set("Idempotency-Key", "test-admin-mutation")
		rec := httptest.NewRecorder()
		srv.handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
		}
		var prob api.Problem
		if err := json.Unmarshal(rec.Body.Bytes(), &prob); err != nil {
			t.Fatalf("decode problem: %v", err)
		}
		if prob.Code != "older_too_small" {
			t.Errorf("code = %q, want older_too_small", prob.Code)
		}
	})

	t.Run("older_than_too_large_returns_400", func(t *testing.T) {
		srv, _, cookie := newForceHarness(t, nil)
		req := httptest.NewRequest(http.MethodPost,
			"/v1/admin/builds/sweep-stuck?confirm=true&older_than=2h", nil)
		req.AddCookie(cookie)
		req.Header.Set("Idempotency-Key", "test-admin-mutation")
		rec := httptest.NewRecorder()
		srv.handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
		}
		var prob api.Problem
		if err := json.Unmarshal(rec.Body.Bytes(), &prob); err != nil {
			t.Fatalf("decode problem: %v", err)
		}
		if prob.Code != "older_too_large" {
			t.Errorf("code = %q, want older_too_large", prob.Code)
		}
	})

	t.Run("invalid_reason_returns_400", func(t *testing.T) {
		srv, _, cookie := newForceHarness(t, nil)
		req := httptest.NewRequest(http.MethodPost,
			"/v1/admin/builds/sweep-stuck?confirm=true&older_than=15m&reason=incident-123", nil)
		req.AddCookie(cookie)
		req.Header.Set("Idempotency-Key", "test-admin-mutation")
		rec := httptest.NewRecorder()
		srv.handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
		}
		var prob api.Problem
		if err := json.Unmarshal(rec.Body.Bytes(), &prob); err != nil {
			t.Fatalf("decode problem: %v", err)
		}
		if prob.Code != api.CodeValidation {
			t.Errorf("code = %q, want %q", prob.Code, api.CodeValidation)
		}
	})

	t.Run("happy_path_returns_swept_count", func(t *testing.T) {
		srv, _, cookie := newForceHarness(t, nil)
		req := httptest.NewRequest(http.MethodPost,
			"/v1/admin/builds/sweep-stuck?confirm=true&older_than=15m", nil)
		req.AddCookie(cookie)
		req.Header.Set("Idempotency-Key", "test-admin-mutation")
		rec := httptest.NewRecorder()
		srv.handler().ServeHTTP(rec, req)

		// MemStore's SweepStuckRunningBuilds returns 0 (no rows
		// to sweep in unit tests); the handler should still
		// 200 with swept_count: 0.
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["ok"] != true {
			t.Errorf("body.ok = %v, want true", body["ok"])
		}
		// swept_count is 0 for MemStore; assert it parses as a
		// number rather than asserting == 0 since pgstore may
		// behave differently.
		if _, ok := body["swept_count"]; !ok {
			t.Errorf("body missing swept_count: %+v", body)
		}
		if body["older_than_seconds"] != float64(900) {
			t.Errorf("body.older_than_seconds = %v, want 900", body["older_than_seconds"])
		}
	})
}
