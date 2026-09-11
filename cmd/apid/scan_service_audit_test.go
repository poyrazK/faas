package main

// scan_service_audit_test.go — unit tests for the ADR-124 ship-
// blocker #2 audit emission (emitWorkloadSkippedRow +
// KindWorkloadSkipped). The audit row is the durable SOC 2
// CC7.2 paper trail the operator needs to answer "who deployed
// v3 and what did they skip?" — it is emitted after apply; a
// read-only scan must not create durable history.
// These tests pin:
//
//  1. kind = project.workload.skipped
//  2. actor pass-through (resolvedActorString format)
//  3. payload shape: {project_id, app_id, workload_name, reason,
//     commit_sha}
//  4. nil-auditor safety (matches cmd/apid/audit.go:316-317)
//  5. per-row independence (loop over partition.Skipped fires
//     one row per entry, not coalesced)

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reconcile"
	"github.com/onebox-faas/faas/pkg/state"
)

// skippedStubAudit satisfies auditEmitterAs. Records every EmitAs
// call in order so tests can assert per-row emission, kind, and
// payload. Concurrency-safe (the production EmitAs is invoked
// from a single goroutine per request today, but a future batch
// path might emit concurrently — the stub mirrors that possibility
// without committing to it).
type skippedStubAudit struct {
	mu    sync.Mutex
	calls []skippedAuditCall
}

func scanAuditRegressionTarGz(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	entries := []struct {
		name string
		body string
	}{
		{"repo/apps/included/Dockerfile", "FROM alpine\n"},
		{"repo/apps/excluded/Dockerfile", "FROM alpine\n"},
	}
	for _, entry := range entries {
		if err := tw.WriteHeader(&tar.Header{
			Name:     entry.name,
			Mode:     0o644,
			ModTime:  time.Unix(0, 0),
			Size:     int64(len(entry.body)),
			Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatalf("tar header %s: %v", entry.name, err)
		}
		if _, err := tw.Write([]byte(entry.body)); err != nil {
			t.Fatalf("tar body %s: %v", entry.name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

func scanAuditRegressionRequest(t *testing.T, key string, source []byte, fields map[string]string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("source", "project.tar.gz")
	if err != nil {
		t.Fatalf("create source part: %v", err)
	}
	if _, err := fw.Write(source); err != nil {
		t.Fatalf("write source part: %v", err)
	}
	for name, value := range fields {
		if err := mw.WriteField(name, value); err != nil {
			t.Fatalf("write field %s: %v", name, err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/projects/scan", &body)
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

// TestScanProject_ExclusionsRemainReadOnly pins issue #1978: the scan
// endpoint preserves the affected-workload projection, but neither a
// per-deploy --exclude nor --persist-exclude may append durable audit rows or
// create a project before the operator applies the plan.
func TestScanProject_ExclusionsRemainReadOnly(t *testing.T) {
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())

	cases := []struct {
		name       string
		fields     map[string]string
		wantStatus int
		wantSkip   bool
	}{
		{
			name:       "exclude",
			fields:     map[string]string{"project_slug": "scan-exclude", "exclude": "excluded"},
			wantStatus: http.StatusOK,
			wantSkip:   true,
		},
		{
			name:       "persist-exclude",
			fields:     map[string]string{"project_slug": "scan-persist", "exclude": "excluded", "persist_exclude": "true"},
			wantStatus: http.StatusOK,
			wantSkip:   true,
		},
		{
			name:       "unknown-exclude",
			fields:     map[string]string{"project_slug": "scan-invalid", "exclude": "missing"},
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, _ := newTestServerWithCapturingNotifier(t, api.PlanPro)
			req := scanAuditRegressionRequest(t, e.key, scanAuditRegressionTarGz(t), tc.fields)
			rec := httptest.NewRecorder()
			e.h.ServeHTTP(rec, req)
			if rec.Code != tc.wantStatus {
				t.Fatalf("scan status = %d, want %d; body=%s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantSkip {
				var resp scanPlanResponse
				if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
					t.Fatalf("scan response is not JSON: %v", err)
				}
				if len(resp.Skipped) != 1 || resp.Skipped[0].Slug != "excluded" {
					t.Fatalf("scan skipped = %#v, want the excluded workload preserved", resp.Skipped)
				}
			}
			events, err := e.store.ListEvents(context.Background(), "", 100)
			if err != nil {
				t.Fatalf("list audit events: %v", err)
			}
			if len(events) != 0 {
				t.Fatalf("scan wrote %d audit events: %#v", len(events), events)
			}
			if _, err := e.store.ProjectBySlug(context.Background(), e.acct.ID, tc.fields["project_slug"]); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("scan created project: err=%v, want %v", err, state.ErrNotFound)
			}
		})
	}
}

// TestApplyProject_ExclusionAuditAfterSuccess is the positive counterpart to
// the read-only regression: the same skipped partition is audited once the
// apply has completed, and the event is tied to the persisted project.
func TestApplyProject_ExclusionAuditAfterSuccess(t *testing.T) {
	spool := t.TempDir()
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", spool)
	t.Setenv("FAAS_SPOOL_ROOT", spool)
	e, _ := newTestServerWithCapturingNotifier(t, api.PlanPro)
	req := scanAuditRegressionRequest(t, e.key, scanAuditRegressionTarGz(t), map[string]string{
		"project_slug": "apply-audit",
		"exclude":      "excluded",
	})
	req.URL.Path = "/v1/projects"
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("apply status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		ProjectID string `json:"project_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("apply response is not JSON: %v", err)
	}
	if resp.ProjectID == "" {
		t.Fatal("apply response omitted project_id")
	}
	events, err := e.store.ListEvents(context.Background(), "", 100)
	if err != nil {
		t.Fatalf("list audit events: %v", err)
	}
	var skipped []state.Event
	for _, event := range events {
		if event.Kind == reconcile.KindWorkloadSkipped {
			skipped = append(skipped, event)
		}
	}
	if len(skipped) != 1 {
		t.Fatalf("skipped audit events = %d, want 1; events=%#v", len(skipped), events)
	}
	var data map[string]any
	if err := json.Unmarshal(skipped[0].Data, &data); err != nil {
		t.Fatalf("skipped audit data is not JSON: %v", err)
	}
	if data["project_id"] != resp.ProjectID {
		t.Errorf("audit project_id = %v, want %s", data["project_id"], resp.ProjectID)
	}
	if data["workload_name"] != "excluded" {
		t.Errorf("audit workload_name = %v, want excluded", data["workload_name"])
	}
}

type skippedAuditCall struct {
	Actor     string
	Kind      string
	AccountID *string
	Data      map[string]any
}

func (s *skippedStubAudit) EmitAs(_ context.Context, actor, kind string, accountID *string, data map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Defensive copy of data so the test's later mutations don't
	// bleed into the recorded call.
	cp := make(map[string]any, len(data))
	for k, v := range data {
		cp[k] = v
	}
	s.calls = append(s.calls, skippedAuditCall{
		Actor:     actor,
		Kind:      kind,
		AccountID: accountID,
		Data:      cp,
	})
}

// TestEmitWorkloadSkippedRow_KindAndPayload pins the audit row's
// kind string + payload keys + values. A drift in any of these
// breaks the dashboard's audit-events filter ("show me all
// project.workload.skipped rows for project X") and SOC 2 CC7.2
// reporting (the audit-events table group-by joins on kind +
// project_id + workload_name).
func TestEmitWorkloadSkippedRow_KindAndPayload(t *testing.T) {
	stub := &skippedStubAudit{}
	emitWorkloadSkippedRow(
		context.Background(),
		stub,
		"dashboard:8a2f3a2e-1111-2222-3333-444455556666",
		"acct-uuid-1111",
		"proj-uuid-aaaa",
		"app-uuid-bbbb",
		"checkout-api",
		"abcdef0123456789",
	)

	if got := len(stub.calls); got != 1 {
		t.Fatalf("calls: got %d, want 1", got)
	}
	call := stub.calls[0]

	if call.Kind != reconcile.KindWorkloadSkipped {
		t.Errorf("kind: got %q, want %q", call.Kind, reconcile.KindWorkloadSkipped)
	}
	if call.Kind != "project.workload.skipped" {
		t.Errorf("kind literal drift: got %q, want %q (any drift breaks the audit-events group-by)", call.Kind, "project.workload.skipped")
	}
	if call.Actor != "dashboard:8a2f3a2e-1111-2222-3333-444455556666" {
		t.Errorf("actor pass-through: got %q, want %q", call.Actor, "dashboard:8a2f3a2e-1111-2222-3333-444455556666")
	}
	if call.AccountID == nil || *call.AccountID != "acct-uuid-1111" {
		t.Errorf("accountID: got %v, want pointer to acct-uuid-1111", call.AccountID)
	}

	// Payload shape pin — the dashboard joins on these keys.
	wantKeys := map[string]any{
		"project_id":    "proj-uuid-aaaa",
		"app_id":        "app-uuid-bbbb",
		"workload_name": "checkout-api",
		"reason":        "unchanged via exclude",
		"commit_sha":    "abcdef0123456789",
	}
	for k, want := range wantKeys {
		got, ok := call.Data[k]
		if !ok {
			t.Errorf("payload missing key %q", k)
			continue
		}
		if got != want {
			t.Errorf("payload[%q]: got %v, want %v", k, got, want)
		}
	}

	// Negative: no unexpected keys leak (mirrors PR #984's
	// annotation-merge "omit when zero" rule — the closed-set
	// shape is part of the contract).
	for k := range call.Data {
		if _, expected := wantKeys[k]; !expected {
			t.Errorf("unexpected payload key %q (drift in audit shape breaks dashboard joins)", k)
		}
	}
}

// TestEmitWorkloadSkippedRow_NilAuditorIsSafe pins the
// nil-receiver safety contract (matches cmd/apid/audit.go:316-317
// on *auditor.EmitAs). A nil auditor must not panic — the emit
// path runs on every preview that includes a --exclude entry, and
// tests that build a server without an audit handle must still
// pass.
func TestEmitWorkloadSkippedRow_NilAuditorIsSafe(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("nil auditor panicked: %v", r)
		}
	}()
	emitWorkloadSkippedRow(
		context.Background(),
		nil,
		"dashboard:any",
		"acct",
		"proj",
		"app",
		"workload",
		"sha",
	)
}

// TestEmitWorkloadSkippedRow_PerRowIndependence pins the
// per-row-emission invariant: looping over N partition.Skipped
// entries emits N audit rows, not one coalesced row. A refactor
// that coalesces (e.g. "one row with skipped_workloads[]") would
// break the dashboard's per-row event timeline + the SOC 2
// "show me every skip decision" filter.
func TestEmitWorkloadSkippedRow_PerRowIndependence(t *testing.T) {
	stub := &skippedStubAudit{}
	cases := []struct {
		workloadName string
		appID        string
		commitSHA    string
	}{
		{"checkout-api", "app-a", "sha-a"},
		{"checkout-web", "app-b", "sha-b"},
		{"inventory", "app-c", "sha-c"},
	}
	for _, tc := range cases {
		emitWorkloadSkippedRow(
			context.Background(),
			stub,
			"dashboard:user-x",
			"acct-1",
			"proj-1",
			tc.appID,
			tc.workloadName,
			tc.commitSHA,
		)
	}

	if got := len(stub.calls); got != len(cases) {
		t.Fatalf("calls: got %d, want %d (per-row independence broken)", got, len(cases))
	}
	for i, call := range stub.calls {
		if call.Data["workload_name"] != cases[i].workloadName {
			t.Errorf("call %d workload_name: got %v, want %v", i, call.Data["workload_name"], cases[i].workloadName)
		}
		if call.Data["app_id"] != cases[i].appID {
			t.Errorf("call %d app_id: got %v, want %v", i, call.Data["app_id"], cases[i].appID)
		}
		if call.Data["commit_sha"] != cases[i].commitSHA {
			t.Errorf("call %d commit_sha: got %v, want %v", i, call.Data["commit_sha"], cases[i].commitSHA)
		}
	}
}

// TestEmitWorkloadSkippedRow_KindConstIsStable pins the
// pkg/reconcile.KindWorkloadSkipped constant — a stray string at
// a future call site would silently fork the kind namespace and
// the dashboard's group-by would miss the row. The constant is
// the source of truth.
func TestEmitWorkloadSkippedRow_KindConstIsStable(t *testing.T) {
	if reconcile.KindWorkloadSkipped == "" {
		t.Fatal("KindWorkloadSkipped must be non-empty (typed const prevents fork)")
	}
	// Closed-set membership in the project.workload.<verb> shape.
	wantPrefix := "project.workload."
	if got := reconcile.KindWorkloadSkipped; len(got) <= len(wantPrefix) || got[:len(wantPrefix)] != wantPrefix {
		t.Errorf("KindWorkloadSkipped %q does not start with %q (closed-set shape broken)", got, wantPrefix)
	}
}

// TestEmitSkippedAuditRows_BrandNewExcluded pins the brand-new
// exclude fix (code-review finding #4): the loop MUST emit an
// audit row for a Skipped entry whose Slug has no corresponding
// app (ID == ""). The earlier code looked the row up in
// `slugToApp` and silently dropped it, leaving a SOC 2 trail
// gap for every brand-new excluded workload. The pin:
//
//  1. A row with ID == "" still emits an audit row.
//  2. workload_name uses row.Slug (the scan workload's Name),
//     NOT a stale `slugToApp` value.
//  3. app_id is empty for brand-new (the audit-events schema
//     allows null; the dashboard renders the row by
//     workload_name).
//  4. The mixed case (existing + brand-new in the same Skipped
//     slice) emits one row per entry — the loop is independent
//     of whether the app already exists.
func TestEmitSkippedAuditRows_BrandNewExcluded(t *testing.T) {
	stub := &skippedStubAudit{}
	rows := []api.PlanAffectedApp{
		// Brand-new: scan emits `payments-api`, no app with that
		// slug has been deployed yet, operator excludes it. row.ID
		// is empty by construction (computeAffectedPartition only
		// sets ID when an app exists at the same key).
		{Slug: "payments-api", Action: "noop"},
		// Existing: scan emits `inventory`, an app with that slug
		// is already deployed, operator excludes it. row.ID is
		// populated.
		{Slug: "inventory", ID: "app-uuid-inventory", Action: "noop"},
		// Brand-new with a hyphen — guards against any future
		// slug-normalization refactor that mangles the name.
		{Slug: "checkout-web", Action: "noop"},
	}
	emitSkippedAuditRows(
		context.Background(),
		stub,
		"dashboard:user-z",
		"acct-uuid-aaaa",
		"proj-uuid-bbbb",
		"sha-feedface",
		rows,
	)

	if got := len(stub.calls); got != 3 {
		t.Fatalf("calls: got %d, want 3 (brand-new rows must NOT be silently skipped)", got)
	}

	// Call 0: brand-new payments-api.
	c0 := stub.calls[0]
	if c0.Data["workload_name"] != "payments-api" {
		t.Errorf("call[0] workload_name: got %v, want %q", c0.Data["workload_name"], "payments-api")
	}
	if c0.Data["app_id"] != "" {
		t.Errorf("call[0] app_id: got %v, want empty (brand-new has no app_id)", c0.Data["app_id"])
	}
	if c0.Data["project_id"] != "proj-uuid-bbbb" {
		t.Errorf("call[0] project_id: got %v, want %q", c0.Data["project_id"], "proj-uuid-bbbb")
	}
	if c0.Data["commit_sha"] != "sha-feedface" {
		t.Errorf("call[0] commit_sha: got %v, want %q", c0.Data["commit_sha"], "sha-feedface")
	}
	if c0.Data["reason"] != "unchanged via exclude" {
		t.Errorf("call[0] reason: got %v, want %q", c0.Data["reason"], "unchanged via exclude")
	}

	// Call 1: existing inventory (regression — make sure the fix
	// didn't break the existing-app case).
	c1 := stub.calls[1]
	if c1.Data["workload_name"] != "inventory" {
		t.Errorf("call[1] workload_name: got %v, want %q", c1.Data["workload_name"], "inventory")
	}
	if c1.Data["app_id"] != "app-uuid-inventory" {
		t.Errorf("call[1] app_id: got %v, want %q", c1.Data["app_id"], "app-uuid-inventory")
	}

	// Call 2: brand-new checkout-web (guarding slug normalization).
	c2 := stub.calls[2]
	if c2.Data["workload_name"] != "checkout-web" {
		t.Errorf("call[2] workload_name: got %v, want %q", c2.Data["workload_name"], "checkout-web")
	}
	if c2.Data["app_id"] != "" {
		t.Errorf("call[2] app_id: got %v, want empty (brand-new has no app_id)", c2.Data["app_id"])
	}
}

// TestEmitSkippedAuditRows_NilAuditorIsSafe pins nil-receiver
// safety at the loop level (the per-row helper already has this
// test; the loop must inherit it). A nil auditor + a non-empty
// Skipped slice must NOT panic — the audit emit is a side-effect
// that must never break the preview render.
func TestEmitSkippedAuditRows_NilAuditorIsSafe(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("nil auditor panicked: %v", r)
		}
	}()
	emitSkippedAuditRows(
		context.Background(),
		nil,
		"dashboard:any",
		"acct",
		"proj",
		"sha",
		[]api.PlanAffectedApp{
			{Slug: "a", Action: "noop"},
			{Slug: "b", Action: "noop"},
		},
	)
}

// TestEmitSkippedAuditRows_EmptySliceIsNoop pins the trivial
// invariant: an empty Skipped slice (no --exclude entries) emits
// zero rows. A regression that always emitted one row would
// pollute the audit-events table on every preview scan.
func TestEmitSkippedAuditRows_EmptySliceIsNoop(t *testing.T) {
	stub := &skippedStubAudit{}
	emitSkippedAuditRows(
		context.Background(),
		stub,
		"dashboard:user-q",
		"acct",
		"proj",
		"sha",
		nil,
	)
	if got := len(stub.calls); got != 0 {
		t.Fatalf("calls on nil rows: got %d, want 0", got)
	}
	emitSkippedAuditRows(
		context.Background(),
		stub,
		"dashboard:user-q",
		"acct",
		"proj",
		"sha",
		[]api.PlanAffectedApp{},
	)
	if got := len(stub.calls); got != 0 {
		t.Fatalf("calls on empty rows: got %d, want 0", got)
	}
}
