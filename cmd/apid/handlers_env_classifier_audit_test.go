// Issue #957 — env-classifier audit-emit seam test.
//
// The env-mutation handler (handlers_env.go::setEnv) runs the data-
// placement classifier (pkg/data/infer.go) after the env row is
// persisted. When the classifier fails, the customer still gets 200
// (correct — the env row IS persisted), but a SOC 2 CC7.2 audit
// record must fire for the failure mode. Before this PR, only a
// slog.Warn line fired.
//
// This file pins the audit-emit contract for the host_hash_failed
// branch — the path where the classifier drops the row on the floor
// before reaching the caller's err return.
//
//   - TestSetEnv_ClassifierFailure_HostHashFailed_EmitsAuditEvent
//     drives the silent-skip branch (handlers_env.go::runEnvClassifier
//     returns errClassifierHostHashFailed when row.HostHashOK==false)
//     by stubbing the HashHost seam to return ("", error). Asserts:
//
//       (a) 200 partial-success contract preserved.
//       (b) env row persisted.
//       (c) data_upstream.classifier_failed audit row fires with
//           error_class=host_hash_failed, silent_skip=true
//           (silent_skip=true because no INSERT was attempted).
//       (d) env.set also fires (no double-emit / no missing emit).
//       (e) data_upstream.inferred MUST NOT fire (host-hash stub
//           means the classifier silently skipped before the
//           inferred emit).
//       (f) account-attribution invariant (Subject is the account
//           UUID).
//
// Tests for the port_out_of_range, uuid_parse, classifier_internal,
// and insert_data_upstream failure branches live in pkg/state/
// pgstore_* — they require a real Postgres (pgtest) because
// state.MemStore's ADR-098 data_upstreams methods are Postgres-only
// stubs (see pkg/state/memstore_data_upstreams.go:33). The
// audit-emit payload shape for those branches is pinned by the
// typed-sentinel table-test in
// handlers_env_classifier_sentinel_table_test.go — the error_class
// + silent_skip dispatch at handlers_env.go:340-358 is the same
// code path for every branch, so unit-pinning the sentinels +
// integration-pinning the host_hash_failed branch is sufficient.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// findAuditEventByKind fetches the audit log for the test account
// and returns the first event with the requested Kind. Returns nil
// when not found. (The package-level `findEventByKind` helper at
// handlers_audit_test.go:1003 takes a raw rows slice; this wrapper
// fetches via the testEnv.)
func findAuditEventByKind(t *testing.T, e testEnv, kind string) *state.Event {
	t.Helper()
	events, err := e.store.ListEvents(context.Background(), e.acct.ID, 100)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	for i := range events {
		if events[i].Kind == kind {
			return &events[i]
		}
	}
	return nil
}

// decodeEventPayload unmarshals an Event's Data json.RawMessage
// into the requested pointer shape. t.Fatal on unmarshal failure so
// the caller can do `payload := decodeEventPayload[map](t, ev); ...`
// without a per-assertion error guard.
func decodeEventPayload(t *testing.T, ev *state.Event, into any) {
	t.Helper()
	if ev == nil {
		t.Fatal("decodeEventPayload: event is nil")
	}
	if err := json.Unmarshal(ev.Data, into); err != nil {
		t.Fatalf("decode event data: %v (raw=%s)", err, string(ev.Data))
	}
}

// listAllAuditKinds returns a compact dump of every event Kind in
// the test account so a failure can be triaged without reaching
// for grep.
func listAllAuditKinds(t *testing.T, e testEnv) []string {
	t.Helper()
	events, err := e.store.ListEvents(context.Background(), e.acct.ID, 100)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	out := make([]string, 0, len(events))
	for i := range events {
		out = append(out, events[i].Kind)
	}
	return out
}

// TestSetEnv_ClassifierFailure_HostHashFailed_EmitsAuditEvent
// drives the silent-skip branch (handlers_env.go::runEnvClassifier
// returns errClassifierHostHashFailed when row.HostHashOK==false)
// by stubbing the HashHost seam to return ("", error). Asserts
// the full issue #957 contract:
//
//   - 200 partial-success preserved.
//   - env row persisted.
//   - data_upstream.classifier_failed audit row emitted with the right
//     payload (error_class, silent_skip, app_id, scope, name).
//   - env.set row also fires (no double-emit / no missing emit).
//   - data_upstream.inferred MUST NOT fire (host-hash stub means
//     the classifier silently skipped before the inferred emit).
//   - data_upstream.classifier_failed's Subject is the account UUID
//     (account-attribution invariant).
func TestSetEnv_ClassifierFailure_HostHashFailed_EmitsAuditEvent(t *testing.T) {
	e := setup(t, api.PlanPro)
	e.s.WithDataPlacement(true).WithHostHashFunc(func(host string) (string, error) {
		return "", errors.New("simulated salt missing")
	})
	app := createApp(t, e, "host-hash-failed-app")

	rec := e.do(t, "PUT", "/v1/apps/"+app.Slug+"/env/DATABASE_URL",
		api.PutAppEnvRequest{Value: "postgres://u:p@db.example.com:5432/mydb"}, nil)
	if rec.Code != 200 {
		t.Fatalf("PUT status = %d, want 200 (partial-success contract); body=%s", rec.Code, rec.Body.String())
	}

	// Env row landed.
	envs, err := e.store.ListAppEnv(context.Background(), e.acct.ID, app.ID)
	if err != nil {
		t.Fatalf("ListAppEnv: %v", err)
	}
	if len(envs) != 1 || envs[0].Key != "DATABASE_URL" {
		t.Fatalf("env rows = %+v, want one DATABASE_URL row", envs)
	}

	// data_upstream.classifier_failed row with the right payload.
	failed := findAuditEventByKind(t, e, "data_upstream.classifier_failed")
	if failed == nil {
		t.Fatalf("data_upstream.classifier_failed audit row missing; events = %v", listAllAuditKinds(t, e))
	}
	var payload map[string]any
	decodeEventPayload(t, failed, &payload)
	if payload["app_id"] != app.ID {
		t.Errorf("payload app_id = %v, want %s", payload["app_id"], app.ID)
	}
	if payload["scope"] != "default" {
		t.Errorf("payload scope = %v, want default", payload["scope"])
	}
	if payload["name"] != "DATABASE_URL" {
		t.Errorf("payload name = %v, want DATABASE_URL", payload["name"])
	}
	if payload["error_class"] != "host_hash_failed" {
		t.Errorf("payload error_class = %v, want host_hash_failed (full payload: %+v)", payload["error_class"], payload)
	}
	if payload["silent_skip"] != true {
		t.Errorf("payload silent_skip = %v, want true", payload["silent_skip"])
	}

	// The precise audit class is normalized to the bounded Prometheus
	// reason vocabulary: host_hash_failed → salt_missing.
	metrics := scrapeOpsMetrics(t, e.ops)
	if !strings.Contains(metrics, `apid_test_data_upstream_classifier_failures_total{reason="salt_missing"} 1`) {
		t.Fatalf("classifier failure metric missing salt_missing sample:\n%s", metrics)
	}

	// Account-attribution invariant.
	if failed.Subject == nil {
		t.Fatalf("data_upstream.classifier_failed Subject is nil; account attribution lost")
	}
	wantSubj := uuid.MustParse(e.acct.ID)
	if *failed.Subject != wantSubj {
		t.Errorf("data_upstream.classifier_failed Subject = %s, want %s", failed.Subject, wantSubj)
	}

	// env.set row also fires (no missing emit on the success-side
	// surface; the classifier-failed branch is additive).
	if set := findAuditEventByKind(t, e, "env.set"); set == nil {
		t.Fatal("env.set audit row missing; classifier-failed branch must not eat env.set")
	}

	// data_upstream.inferred MUST NOT fire — the host-hash stub
	// means the classifier silently skipped before the inferred
	// audit emit at handlers_env.go.
	if inferred := findAuditEventByKind(t, e, "data_upstream.inferred"); inferred != nil {
		t.Errorf("data_upstream.inferred should not fire when HashHost stub returns error; got %s", string(inferred.Data))
	}
}
