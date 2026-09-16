// handlers_app_changed_test.go — pins the Phase 2 / Gate A
// NotifyAppChanged emits on app-create paths.
//
// Background: apid dropped the in-apid placement chooser
// (cmd/apid/placement.go, deleted by PR #509). The new flow:
//   1. apid.createApp inserts the apps row with node_id = NULL.
//   2. apid.createApp emits db.NotifyAppChanged with a
//      {"kind":"created","app_id"} payload (slug is omitted; the
//      inserted apps row carries it; the subscriber only keys on
//      payload.app_id).
//   3. Every schedd's pkg/sched.PlacementClaimSubscriber reacts
//      to the payload and runs Engine.ClaimUnplaced to stamp
//      the owner via Store.SetAppNodeID's conditional UPDATE.
//
// Without the apid emit the subscriber never fires and apps
// stay unplaced until the schedd cold-start sweep reconciles
// them — which the runbook documents as a multi-second outage
// window during normal create traffic. These tests pin the
// emit shape so a future refactor that drops the notify fires
// a red gate, not a 3am pager.
//
// Companion file: pkg/sched/placement_claim_test.go covers the
// subscriber side. apid's emit is the only behavioural change
// for the Gate A promotion; the test seam below (capturingNotifier)
// is reusable for any future emit-required handler.

package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// capturingNotifier is a Notifier that records every Notify call
// into a thread-safe slice so tests can assert the channel +
// payload shape of an emit. The remaining Notifier methods are
// no-ops or use the test-supplied hooks; nothing here actually
// talks to Postgres.
type capturingNotifier struct {
	mu      sync.Mutex
	emitted []capturedNotification
	hook    func(ctx context.Context, channel string, predicate func(payload string) bool, timeout time.Duration) (string, error)
}

type capturedNotification struct {
	Channel string
	Payload string
}

func (n *capturingNotifier) Notify(_ context.Context, channel, payload string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.emitted = append(n.emitted, capturedNotification{Channel: channel, Payload: payload})
	return nil
}

func (n *capturingNotifier) Subscribe(_ context.Context, _ []string) (<-chan db.Notification, func(), error) {
	ch := make(chan db.Notification)
	close(ch)
	return ch, func() {}, nil
}

func (n *capturingNotifier) WaitFor(ctx context.Context, channel string, predicate func(payload string) bool, timeout time.Duration) (string, error) {
	if n.hook != nil {
		return n.hook(ctx, channel, predicate, timeout)
	}
	return "", db.ErrWaitTimeout
}

func (n *capturingNotifier) findAppChanged() (capturedNotification, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, e := range n.emitted {
		if e.Channel == db.NotifyAppChanged {
			return e, true
		}
	}
	return capturedNotification{}, false
}

// newTestServerWithCapturingNotifier wires a MemStore-backed
// server with a capturingNotifier on the notif hook so tests
// can assert that the handler emits the expected NotifyAppChanged
// payload. Mirrors setupWithNotifier's shape (server_test.go:69)
// but with a Notifier that records emissions.
func newTestServerWithCapturingNotifier(t *testing.T, plan api.Plan) (testEnv, *capturingNotifier) {
	t.Helper()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(context.Background(), fmt.Sprintf("%s@example.com", plan), plan)
	if err != nil {
		t.Fatal(err)
	}
	pt, hash, _ := api.GenerateAPIKey()
	if _, err := store.CreateAPIKey(context.Background(), acct.ID, hash, "test", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	ops := wire.NewOpsMetrics("apid_test")
	notif := &capturingNotifier{}
	srv := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", notif).WithOpsMetrics(context.Background(), ops)
	return testEnv{h: srv.handler(), s: srv, store: store, key: pt, acct: acct, ops: ops}, notif
}

// TestCreateApp_EmitsAppChanged is the headline PR #509
// regression test for the placement-claim wiring: a successful
// createApp must emit a single NotifyAppChanged with payload
// kind=created + app_id (post-rebase). schedd's
// pkg/sched.PlacementClaimSubscriber filters on payload.Kind
// == "created"; an emit with a different kind (or no emit at
// all) breaks the placement claim.
func TestCreateApp_EmitsAppChanged(t *testing.T) {
	e, notif := newTestServerWithCapturingNotifier(t, api.PlanPro)

	req := api.CreateAppRequest{
		Slug:           "hello-app",
		Type:           string(state.AppTypeApp),
		RAMMB:          256,
		MaxConcurrency: 2,
	}
	body, _ := json.Marshal(req)
	httpReq := httptest.NewRequest(http.MethodPost, "/v1/apps", bytes.NewReader(body))
	httpReq.Header.Set("Authorization", "Bearer "+e.key)
	httpReq.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, httpReq)

	if rec.Code != http.StatusCreated {
		t.Fatalf("createApp status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}

	// Exactly one NotifyAppChanged must fire — payload carries
	// kind=created + app_id. The shape is what
	// pkg/sched.PlacementClaimSubscriber.handle parses.
	got, ok := notif.findAppChanged()
	if !ok {
		t.Fatalf("expected a NotifyAppChanged emit on createApp, got none\nemitted: %+v", notif.emitted)
	}
	var payload struct {
		Kind  string `json:"kind"`
		AppID string `json:"app_id"`
	}
	if err := json.Unmarshal([]byte(got.Payload), &payload); err != nil {
		t.Fatalf("NotifyAppChanged payload is not valid JSON: %v\npayload=%s", err, got.Payload)
	}
	if payload.Kind != "created" {
		t.Errorf("payload.kind = %q, want %q (placement claim subscriber filters on this)", payload.Kind, "created")
	}
	// payload.slug is informational; pkg/sched.PlacementClaimSubscriber
	// reads it for logs but the claim path is keyed on apps.id (the
	// "app_id" field above). Per the post-rebase emit shape (taken
	// from main's reconcile-based apid), slug is omitted to keep the
	// notify payload minimal — the inserted row carries the slug.
	if payload.AppID == "" {
		t.Errorf("payload.app_id is empty; want the newly-inserted app's id")
	}
	// The returned AppResponse must carry the same app_id; this
	// pins the request-to-emit pipeline so a future refactor that
	// emits before the row is committed (and reads an empty id)
	// fires the test.
	var resp api.AppResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("createApp response not valid JSON: %v", err)
	}
	if resp.ID != payload.AppID {
		t.Errorf("response.id (%s) != emitted app_id (%s); commit-before-emit invariant violated",
			resp.ID, payload.AppID)
	}
}

// TestCreateApp_RejectsInvalidSlugDoesNotEmit guards the
// negative path: a 400 (slug regex violation) must NOT emit
// a placement-claim notify. Without this pin a future refactor
// that hoists the emit before validation would wake every
// schedd for every malformed request.
func TestCreateApp_RejectsInvalidSlugDoesNotEmit(t *testing.T) {
	e, notif := newTestServerWithCapturingNotifier(t, api.PlanPro)

	req := api.CreateAppRequest{
		Slug:  "AB", // invalid: 3-40 chars required
		Type:  string(state.AppTypeApp),
		RAMMB: 256,
	}
	body, _ := json.Marshal(req)
	httpReq := httptest.NewRequest(http.MethodPost, "/v1/apps", bytes.NewReader(body))
	httpReq.Header.Set("Authorization", "Bearer "+e.key)
	httpReq.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, httpReq)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("createApp status = %d, want 400", rec.Code)
	}
	if _, ok := notif.findAppChanged(); ok {
		t.Errorf("createApp emitted NotifyAppChanged on a 400 — must not fire before validation\nemitted: %+v", notif.emitted)
	}
}

// TestCreateApp_QuotaErrorDoesNotEmit guards the quota-fail
// branch: a 4xx from CreateAppIfUnderQuota must NOT emit a
// placement-claim notify (the row never landed, so no claim
// is possible). Same invariant as the slug test above.
//
// Pre-creates one app to exhaust the Free-plan DeployedApps=1
// quota; the second create (the one under test) must 403
// without firing the placement-claim notify.
func TestCreateApp_QuotaErrorDoesNotEmit(t *testing.T) {
	e, notif := newTestServerWithCapturingNotifier(t, api.PlanFree)

	// First create — succeeds and emits the placement-claim
	// notify. This warms the notify channel so a SECOND emit
	// would be a clear regression. The negative branch we test
	// is the second createApp call below.
	first := api.CreateAppRequest{
		Slug: "first-app",
		Type: string(state.AppTypeApp),
	}
	body, _ := json.Marshal(first)
	httpReq := httptest.NewRequest(http.MethodPost, "/v1/apps", bytes.NewReader(body))
	httpReq.Header.Set("Authorization", "Bearer "+e.key)
	httpReq.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, httpReq)
	if rec.Code != http.StatusCreated {
		t.Fatalf("first createApp status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if _, ok := notif.findAppChanged(); !ok {
		t.Fatal("first createApp must have emitted NotifyAppChanged to seed the test (subscribers depend on it)")
	}

	// Reset the notifier's capture so the assertion below
	// only inspects emits that happened on the rejected request.
	notif.mu.Lock()
	notif.emitted = nil
	notif.mu.Unlock()

	// Second create — quota breach. Must 4xx and NOT emit.
	second := api.CreateAppRequest{
		Slug: "second-app",
		Type: string(state.AppTypeApp),
	}
	body, _ = json.Marshal(second)
	httpReq = httptest.NewRequest(http.MethodPost, "/v1/apps", bytes.NewReader(body))
	httpReq.Header.Set("Authorization", "Bearer "+e.key)
	httpReq.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	e.h.ServeHTTP(rec, httpReq)

	if rec.Code != http.StatusForbidden && rec.Code != http.StatusConflict {
		t.Fatalf("second createApp status = %d, want 4xx (Free plan quota)", rec.Code)
	}
	if _, ok := notif.findAppChanged(); ok {
		t.Errorf("createApp emitted NotifyAppChanged on a quota failure — must not fire on a row that never landed\nemitted: %+v", notif.emitted)
	}
}

// --- ApplyProjectPlan placement-claim emits -----------------------------
//
// Background: cmd/apid/handlers_decompose.go::applyProject emits one
// NotifyAppChanged per inserted app at the end of a successful Tx.
// The same emit shape is shared with createApp ({"kind":"created",
// "app_id"}), with an additional "project_id" field that
// schedd's PlacementClaimSubscriber ignores (it filters on Kind
// alone). Without the per-app emit every schedd would only learn
// about the new apps via the cold-start sweep — multi-second
// placement latency for every /v1/projects call.
//
// The test below drives POST /v1/projects end-to-end with a minimal
// one-workload tarball upload, asserts 200 (well, the apply response
// status — scan_service returns an applyResp body), and verifies the
// notifier captured exactly one NotifyAppChanged per inserted app.

// applyProjectOneWorkloadTarGz builds a minimal source tree with one
// workload named "myapp" carrying a Dockerfile + index.js. The
// layout matches what reposcan.Scan accepts: a top-level directory
// with a Dockerfile inside.
func applyProjectOneWorkloadTarGz(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	entries := []struct {
		Name string
		Body []byte
	}{
		{"myapp/Dockerfile", []byte("FROM alpine\n")},
		{"myapp/index.js", []byte("exports.handler = () => 1;\n")},
	}
	for _, e := range entries {
		hdr := tar.Header{
			Name:     e.Name,
			Mode:     0o644,
			ModTime:  time.Unix(0, 0),
			Size:     int64(len(e.Body)),
			Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(&hdr); err != nil {
			t.Fatalf("WriteHeader(%s): %v", e.Name, err)
		}
		if _, err := tw.Write(e.Body); err != nil {
			t.Fatalf("Write(%s): %v", e.Name, err)
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

// applyProjectCountEmits counts the NotifyAppChanged payloads the
// notifier captured during the applyProject call. Used to assert
// "exactly N emits for N inserted apps" without coupling to the
// inner goroutine ordering of the apply handler.
func applyProjectCountEmits(n *capturingNotifier) int {
	n.mu.Lock()
	defer n.mu.Unlock()
	c := 0
	for _, e := range n.emitted {
		if e.Channel == db.NotifyAppChanged {
			c++
		}
	}
	return c
}

// TestApplyProjectPlan_EmitsAppChangedPerWorkload is the apply-side
// counterpart to TestCreateApp_EmitsAppChanged. It uploads a
// one-workload tarball via POST /v1/projects and asserts that the
// notifier captured exactly one NotifyAppChanged with kind=created
// + the inserted app_id + project_id. The shape is what
// pkg/sched.PlacementClaimSubscriber parses (payload.Kind == "created"
// + payload.AppID).
func TestApplyProjectPlan_EmitsAppChangedPerWorkload(t *testing.T) {
	// The scan service writes extracted tarballs to FAAS_SCAN_SPOOL_ROOT
	// (set by cmd/apid's startup) and reads back via os.DirFS. Pin a
	// test-local tempdir so concurrent tests don't collide. The
	// multipart parser uses FAAS_SPOOL_ROOT (deploy_inputs.go:238) —
	// both must point somewhere writable.
	tmpSpool := t.TempDir()
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", tmpSpool)
	t.Setenv("FAAS_SPOOL_ROOT", tmpSpool)
	if err := os.MkdirAll(tmpSpool, 0o700); err != nil {
		t.Fatalf("spool mkdir: %v", err)
	}

	e, notif := newTestServerWithCapturingNotifier(t, api.PlanPro)

	// Build the multipart body. The field name `source` matches
	// the multipart parser in scan_service.go (parseScanMultipart).
	var bodyBuf bytes.Buffer
	mw := multipart.NewWriter(&bodyBuf)
	fw, err := mw.CreateFormFile("source", "myapp.tar.gz")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := fw.Write(applyProjectOneWorkloadTarGz(t)); err != nil {
		t.Fatalf("write tarball: %v", err)
	}
	// Project slug is required alongside source. Generic root workloads use
	// this identity so two projects cannot both create the global slug "app".
	if err := mw.WriteField("project_slug", "myproj"); err != nil {
		t.Fatalf("WriteField project_slug: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("multipart close: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/projects", &bodyBuf)
	req.Header.Set("Authorization", "Bearer "+e.key)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)

	// The apply endpoint returns 200 with an applyResp body on
	// success. A 4xx is a setup failure (extraction / reposcan / quota).
	if rec.Code != http.StatusOK {
		t.Fatalf("applyProject status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	// Exactly one NotifyAppChanged must fire — one workload inserted.
	emits := applyProjectCountEmits(notif)
	if emits != 1 {
		t.Fatalf("NotifyAppChanged count = %d, want 1\nemitted: %+v", emits, notif.emitted)
	}
	// The single emit must carry kind=created + the inserted app_id
	// + the inserted project_id. payload.slug is omitted from this
	// emit (mirroring createApp's post-rebase shape); the inserted
	// row carries the slug.
	got, _ := notif.findAppChanged()
	var payload struct {
		Kind      string `json:"kind"`
		AppID     string `json:"app_id"`
		ProjectID string `json:"project_id"`
	}
	if err := json.Unmarshal([]byte(got.Payload), &payload); err != nil {
		t.Fatalf("NotifyAppChanged payload not valid JSON: %v\npayload=%s", err, got.Payload)
	}
	if payload.Kind != "created" {
		t.Errorf("payload.kind = %q, want %q", payload.Kind, "created")
	}
	if payload.AppID == "" {
		t.Errorf("payload.app_id is empty")
	}
	if payload.ProjectID == "" {
		t.Errorf("payload.project_id is empty")
	}
	// Sanity: the inserted app row should be visible through the
	// store. A future refactor that emits with an empty app_id
	// (e.g. emits before INSERT commits) would still pass the
	// emit-shape assertions above; this row-existence check is the
	// belt-and-braces.
	if _, err := e.store.AppBySlug(context.Background(), "myproj"); err != nil {
		t.Errorf("AppBySlug(myproj) post-apply: %v (inserted app missing)", err)
	}
}

// silence unused-helper lints for tar/tar-related symbols that
// would otherwise sit unused if a future edit drops them.
var _ = filepath.Join
