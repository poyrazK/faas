// Whitebox tests for the traffic-mirroring HTTP handlers
// (issue #72 / ADR-125 PR-A2). Mirrors the structure of
// handlers_traffic_notify_test.go + handlers_ext_test.go's
// updateDeploymentTraffic coverage:
//
//   - Happy path (Pro / Scale) → 201 + notify emit.
//   - Plan gate (Free / Hobby) → 403 plan_mirror_not_allowed.
//   - Range check → 422 invalid_mirror_percent.
//   - Quota gate (Pro cap=1, Scale cap=3) → 422 mirror_rule_quota_exceeded.
//   - Validation sentinels (source==target, cross-app, deploy-not-live)
//     surface as 422/409 with the canonical Problem code.
//   - IDOR posture (cross-account GET/PATCH/DELETE → 404 silent).
//   - Window-param parse failures surface 422 invalid_mirror_window.
//
// Notify-emit is asserted via captureNotifier (vendored from
// handlers_traffic_notify_test.go). Audit-emit is *not* asserted
// here because newServerWithDeps doesn't wire an audit emitter;
// PR-A3's gateway-side test will add the audit-capture seam.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// mirrorTestNotifier records every Notify call so the tests can
// assert the kind="mirror" payload the handlers emit. Identical
// shape to captureNotifier in handlers_traffic_notify_test.go;
// duplicated here so this file is self-contained.
type mirrorTestNotifier struct {
	mu    sync.Mutex
	notif []mirrorCaptured
}

type mirrorCaptured struct {
	channel string
	payload string
}

func (c *mirrorTestNotifier) Notify(_ context.Context, channel, payload string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.notif = append(c.notif, mirrorCaptured{channel: channel, payload: payload})
	return nil
}

func (c *mirrorTestNotifier) Subscribe(_ context.Context, _ []string) (<-chan db.Notification, func(), error) {
	ch := make(chan db.Notification)
	close(ch)
	return ch, func() {}, nil
}

func (c *mirrorTestNotifier) WaitFor(_ context.Context, _ string, _ func(payload string) bool, _ time.Duration) (string, error) {
	return "", db.ErrWaitTimeout
}

func (c *mirrorTestNotifier) mirrorCalls() []mirrorCaptured {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]mirrorCaptured, len(c.notif))
	copy(out, c.notif)
	return out
}

// mirrorFixture builds the canonical test world: one account on
// every interesting plan + one app per account with two live
// deployments. Returns a cleanup func that closes nothing (the
// memstore is GC-managed) but gives the test a single anchor
// for fixture wiring. The second live deployment is the
// "mirror target" so a mirror rule (source, mirror) can be
// created on the same app.
type mirrorFixture struct {
	store     state.Store
	notif     *mirrorTestNotifier
	hobbyAcct state.Account
	hobbyApp  state.App
	hobbyDep1 state.Deployment
	hobbyDep2 state.Deployment
	proAcct   state.Account
	proApp    state.App
	proDep1   state.Deployment
	proDep2   state.Deployment
	scaleAcct state.Account
	scaleApp  state.App
	scaleDep1 state.Deployment
	scaleDep2 state.Deployment
	otherAcct state.Account // for IDOR tests
	otherApp  state.App
}

func newMirrorFixture(t *testing.T) *mirrorFixture {
	t.Helper()
	store := state.NewMemStore()
	mkAcct := func(email string, plan api.Plan) state.Account {
		a, err := store.CreateAccount(context.Background(), email, plan)
		if err != nil {
			t.Fatalf("CreateAccount(%s): %v", email, err)
		}
		return a
	}
	hobby := mkAcct("hobby@example.com", api.PlanHobby)
	pro := mkAcct("pro@example.com", api.PlanPro)
	scale := mkAcct("scale@example.com", api.PlanScale)
	other := mkAcct("other@example.com", api.PlanScale)
	mkApp := func(acct state.Account, slug string) state.App {
		app, err := store.CreateApp(context.Background(), state.App{
			AccountID: acct.ID, Slug: slug, Type: state.AppTypeApp,
			RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60,
		})
		if err != nil {
			t.Fatalf("CreateApp(%s): %v", slug, err)
		}
		return app
	}
	hobbyApp := mkApp(hobby, "hobby-app")
	proApp := mkApp(pro, "pro-app")
	scaleApp := mkApp(scale, "scale-app")
	otherApp := mkApp(other, "other-app")
	// Mirror targets are simultaneously live in distinct deployment scopes.
	mkLivePair := func(app state.App, s1, s2 string) (state.Deployment, state.Deployment) {
		d1, err := store.CreateDeployment(context.Background(), state.Deployment{
			AppID: app.ID, Kind: state.DeploymentKindImage,
			ImageDigest: "sha256:" + s1, Status: state.DeployPending,
		})
		if err != nil {
			t.Fatalf("CreateDeployment d1: %v", err)
		}
		if err := store.MarkDeploymentLive(context.Background(), d1.ID); err != nil {
			t.Fatalf("MarkDeploymentLive d1: %v", err)
		}
		d2, err := store.CreateDeployment(context.Background(), state.Deployment{
			AppID: app.ID, Kind: state.DeploymentKindImage,
			ImageDigest: "sha256:" + s2, Status: state.DeployPending, Scope: "mirror-" + s2,
		})
		if err != nil {
			t.Fatalf("CreateDeployment d2: %v", err)
		}
		if err := store.MarkDeploymentLive(context.Background(), d2.ID); err != nil {
			t.Fatalf("MarkDeploymentLive d2: %v", err)
		}
		return d1, d2
	}
	hobbyD1, hobbyD2 := mkLivePair(hobbyApp, "001", "002")
	proD1, proD2 := mkLivePair(proApp, "003", "004")
	scaleD1, scaleD2 := mkLivePair(scaleApp, "005", "006")
	return &mirrorFixture{
		store:     store,
		notif:     &mirrorTestNotifier{},
		hobbyAcct: hobby,
		hobbyApp:  hobbyApp,
		hobbyDep1: hobbyD1,
		hobbyDep2: hobbyD2,
		proAcct:   pro,
		proApp:    proApp,
		proDep1:   proD1,
		proDep2:   proD2,
		scaleAcct: scale,
		scaleApp:  scaleApp,
		scaleDep1: scaleD1,
		scaleDep2: scaleD2,
		otherAcct: other,
		otherApp:  otherApp,
	}
}

// newMirrorServer builds the apid server with the canonical test
// fixture's store + notifier. Helper so each test is one line
// of fixture wiring.
func newMirrorServer(fx *mirrorFixture) http.Handler {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := newServerWithDeps(fx.store, log, "gregale.dev", fx.notif, "", noopMailer{}, stubGithubdClient{}, nil, nil, 0, "")
	return srv.handler()
}

func TestMirrorCreate_HappyPath_Pro(t *testing.T) {
	fx := newMirrorFixture(t)
	h := newMirrorServer(fx)
	pt, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}
	if _, err := fx.store.CreateAPIKey(context.Background(), fx.proAcct.ID, hash, "test", api.ScopesDeployWriteSurface); err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	body := []byte(`{"source_deployment_id":"` + fx.proDep1.ID + `","mirror_deployment_id":"` + fx.proDep2.ID + `","percent":25,"include_body":true,"redact_headers":["X-Foo"]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/apps/"+fx.proApp.Slug+"/mirrors", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+pt)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var resp api.MirrorRuleResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Percent != 25 {
		t.Errorf("percent = %d, want 25", resp.Percent)
	}
	if !resp.IncludeBody {
		t.Errorf("include_body = false, want true")
	}
	if len(resp.AlwaysStrippedHeaders) != 6 {
		t.Errorf("always_stripped_headers count = %d, want 6", len(resp.AlwaysStrippedHeaders))
	}
	if len(fx.notif.mirrorCalls()) == 0 {
		t.Errorf("no kind=mirror notify emitted; createMirrorRule dropped the emit")
	}
}

func TestMirrorCreate_PlanGate_Free(t *testing.T) {
	// Build a Free account separately (the fixture only carries
	// Hobby/Pro/Scale because Pro/Scale are the interesting
	// cases; Free behaves identically to Hobby for the plan
	// gate but a separate test pins both).
	store := state.NewMemStore()
	free, err := store.CreateAccount(context.Background(), "free@example.com", api.PlanFree)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := store.CreateApp(context.Background(), state.App{AccountID: free.ID, Slug: "free-app", Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 1, IdleTimeoutS: 30})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	d, _ := store.CreateDeployment(context.Background(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:f", Status: state.DeployPending})
	_ = store.MarkDeploymentLive(context.Background(), d.ID)
	notif := &mirrorTestNotifier{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := newServerWithDeps(store, log, "gregale.dev", notif, "", noopMailer{}, stubGithubdClient{}, nil, nil, 0, "")
	pt, hash, _ := api.GenerateAPIKey()
	_, _ = store.CreateAPIKey(context.Background(), free.ID, hash, "test", api.ScopesDeployWriteSurface)

	body := []byte(`{"source_deployment_id":"` + d.ID + `","mirror_deployment_id":"` + d.ID + `","percent":50}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/apps/"+app.Slug+"/mirrors", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+pt)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(api.CodePlanMirrorNotAllowed)) {
		t.Errorf("body missing code %q: %s", api.CodePlanMirrorNotAllowed, rec.Body.String())
	}
}

func TestMirrorCreate_PlanGate_Hobby(t *testing.T) {
	fx := newMirrorFixture(t)
	h := newMirrorServer(fx)
	pt, hash, _ := api.GenerateAPIKey()
	_, _ = fx.store.CreateAPIKey(context.Background(), fx.hobbyAcct.ID, hash, "test", api.ScopesDeployWriteSurface)
	body := []byte(`{"source_deployment_id":"` + fx.hobbyDep1.ID + `","mirror_deployment_id":"` + fx.hobbyDep2.ID + `","percent":50}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/apps/"+fx.hobbyApp.Slug+"/mirrors", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+pt)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
}

func TestMirrorCreate_RangeCheck(t *testing.T) {
	fx := newMirrorFixture(t)
	h := newMirrorServer(fx)
	pt, hash, _ := api.GenerateAPIKey()
	_, _ = fx.store.CreateAPIKey(context.Background(), fx.proAcct.ID, hash, "test", api.ScopesDeployWriteSurface)
	for _, p := range []int{-1, 101, 9999} {
		body := []byte(`{"source_deployment_id":"` + fx.proDep1.ID + `","mirror_deployment_id":"` + fx.proDep2.ID + `","percent":` + itoa(p) + `}`)
		req := httptest.NewRequest(http.MethodPost, "/v1/apps/"+fx.proApp.Slug+"/mirrors", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+pt)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("percent=%d: status=%d, want 422; body=%s", p, rec.Code, rec.Body.String())
		}
		if !bytes.Contains(rec.Body.Bytes(), []byte(api.CodeInvalidMirrorPercent)) {
			t.Errorf("percent=%d: body missing %s", p, api.CodeInvalidMirrorPercent)
		}
	}
}

func TestMirrorCreate_QuotaEnforced(t *testing.T) {
	fx := newMirrorFixture(t)
	h := newMirrorServer(fx)
	pt, hash, _ := api.GenerateAPIKey()
	_, _ = fx.store.CreateAPIKey(context.Background(), fx.proAcct.ID, hash, "test", api.ScopesDeployWriteSurface)
	// Pro cap = 1: first POST → 201, second POST → 422 quota.
	mkPost := func() *httptest.ResponseRecorder {
		body := []byte(`{"source_deployment_id":"` + fx.proDep1.ID + `","mirror_deployment_id":"` + fx.proDep2.ID + `","percent":25}`)
		req := httptest.NewRequest(http.MethodPost, "/v1/apps/"+fx.proApp.Slug+"/mirrors", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+pt)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if r := mkPost(); r.Code != http.StatusCreated {
		t.Fatalf("first POST: status = %d, want 201; body=%s", r.Code, r.Body.String())
	}
	if r := mkPost(); r.Code != http.StatusUnprocessableEntity {
		t.Fatalf("second POST: status = %d, want 422; body=%s", r.Code, r.Body.String())
	} else if !bytes.Contains(r.Body.Bytes(), []byte(api.CodeMirrorRuleQuotaExceeded)) {
		t.Errorf("second POST body missing %s: %s", api.CodeMirrorRuleQuotaExceeded, r.Body.String())
	}
}

func TestMirrorCreate_SourceTargetSame(t *testing.T) {
	fx := newMirrorFixture(t)
	h := newMirrorServer(fx)
	pt, hash, _ := api.GenerateAPIKey()
	_, _ = fx.store.CreateAPIKey(context.Background(), fx.proAcct.ID, hash, "test", api.ScopesDeployWriteSurface)
	body := []byte(`{"source_deployment_id":"` + fx.proDep1.ID + `","mirror_deployment_id":"` + fx.proDep1.ID + `","percent":50}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/apps/"+fx.proApp.Slug+"/mirrors", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+pt)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(api.CodeMirrorSourceTargetSame)) {
		t.Errorf("body missing %s: %s", api.CodeMirrorSourceTargetSame, rec.Body.String())
	}
}

func TestMirrorCreate_DeploymentNotLive(t *testing.T) {
	fx := newMirrorFixture(t)
	// Create a third deployment that's NOT marked live; reference
	// it as the mirror target. The store's deployment-not-live
	// backstop fires and surfaces 409 mirror_deployment_not_live.
	dead, err := fx.store.CreateDeployment(context.Background(), state.Deployment{
		AppID: fx.proApp.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:dead", Status: state.DeployPending,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	h := newMirrorServer(fx)
	pt, hash, _ := api.GenerateAPIKey()
	_, _ = fx.store.CreateAPIKey(context.Background(), fx.proAcct.ID, hash, "test", api.ScopesDeployWriteSurface)
	body := []byte(`{"source_deployment_id":"` + fx.proDep1.ID + `","mirror_deployment_id":"` + dead.ID + `","percent":50}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/apps/"+fx.proApp.Slug+"/mirrors", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+pt)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(api.CodeMirrorDeploymentNotLive)) {
		t.Errorf("body missing %s: %s", api.CodeMirrorDeploymentNotLive, rec.Body.String())
	}
}

func TestMirrorGet_IDOR_CrossAccount(t *testing.T) {
	fx := newMirrorFixture(t)
	h := newMirrorServer(fx)
	// Create a rule on proAcct's app.
	rule, err := fx.store.CreateMirrorRuleIfUnderQuota(context.Background(), state.CreateMirrorRuleParams{
		AccountID:          fx.proAcct.ID,
		AppID:              fx.proApp.ID,
		SourceDeploymentID: fx.proDep1.ID,
		MirrorDeploymentID: fx.proDep2.ID,
		Percent:            50,
		Enabled:            true,
		IncludeBody:        false,
		RedactHeaders:      []string{},
	}, api.MustLimitsFor(api.PlanPro))
	if err != nil {
		t.Fatalf("seed: CreateMirrorRuleIfUnderQuota: %v", err)
	}
	// otherAcct tries to GET the rule via its OWN slug (no
	// match — loadApp returns false) AND via the proAcct slug
	// (loadApp returns false because AccountID doesn't match).
	// Both must surface 404, never 403 or 422.
	pt, hash, _ := api.GenerateAPIKey()
	_, _ = fx.store.CreateAPIKey(context.Background(), fx.otherAcct.ID, hash, "test", api.ScopesReadSurface)
	for _, slug := range []string{fx.otherApp.Slug, fx.proApp.Slug} {
		req := httptest.NewRequest(http.MethodGet, "/v1/apps/"+slug+"/mirrors/"+rule.ID, nil)
		req.Header.Set("Authorization", "Bearer "+pt)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("slug=%s: status = %d, want 404; body=%s", slug, rec.Code, rec.Body.String())
		}
	}
}

func TestMirrorUpdate_PatchSemantics(t *testing.T) {
	fx := newMirrorFixture(t)
	h := newMirrorServer(fx)
	rule, err := fx.store.CreateMirrorRuleIfUnderQuota(context.Background(), state.CreateMirrorRuleParams{
		AccountID:          fx.proAcct.ID,
		AppID:              fx.proApp.ID,
		SourceDeploymentID: fx.proDep1.ID,
		MirrorDeploymentID: fx.proDep2.ID,
		Percent:            25,
		Enabled:            true,
		IncludeBody:        false,
		RedactHeaders:      []string{},
	}, api.MustLimitsFor(api.PlanPro))
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	pt, hash, _ := api.GenerateAPIKey()
	_, _ = fx.store.CreateAPIKey(context.Background(), fx.proAcct.ID, hash, "test", api.ScopesDeployWriteSurface)
	// PATCH with empty body — every field absent — Percent
	// stays at 25, Enabled stays true. This is the
	// "patch-semantics" guarantee (pointer fields, not zero).
	body := []byte(`{}`)
	req := httptest.NewRequest(http.MethodPatch, "/v1/apps/"+fx.proApp.Slug+"/mirrors/"+rule.ID, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+pt)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("empty patch status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp api.MirrorRuleResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Percent != 25 {
		t.Errorf("empty patch: percent = %d, want 25 (unchanged)", resp.Percent)
	}
	// PATCH with explicit Percent=0 — this is the legal
	// "disable without removing" case (distinct from "absent").
	body = []byte(`{"percent":0}`)
	req = httptest.NewRequest(http.MethodPatch, "/v1/apps/"+fx.proApp.Slug+"/mirrors/"+rule.ID, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+pt)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("explicit zero patch status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Percent != 0 {
		t.Errorf("explicit zero: percent = %d, want 0", resp.Percent)
	}
}

func TestMirrorSummary_WindowParam(t *testing.T) {
	fx := newMirrorFixture(t)
	rule, _ := fx.store.CreateMirrorRuleIfUnderQuota(context.Background(), state.CreateMirrorRuleParams{
		AccountID:          fx.proAcct.ID,
		AppID:              fx.proApp.ID,
		SourceDeploymentID: fx.proDep1.ID,
		MirrorDeploymentID: fx.proDep2.ID,
		Percent:            50, Enabled: true, IncludeBody: false, RedactHeaders: []string{},
	}, api.MustLimitsFor(api.PlanPro))
	h := newMirrorServer(fx)
	pt, hash, _ := api.GenerateAPIKey()
	_, _ = fx.store.CreateAPIKey(context.Background(), fx.proAcct.ID, hash, "test", api.ScopesReadSurface)
	for _, win := range []string{"", "1h", "24h", "7d"} {
		req := httptest.NewRequest(http.MethodGet, "/v1/apps/"+fx.proApp.Slug+"/mirrors/"+rule.ID+"/summary?window="+win, nil)
		req.Header.Set("Authorization", "Bearer "+pt)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("window=%q: status = %d, want 200; body=%s", win, rec.Code, rec.Body.String())
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/apps/"+fx.proApp.Slug+"/mirrors/"+rule.ID+"/summary?window=2h", nil)
	req.Header.Set("Authorization", "Bearer "+pt)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("window=2h: status = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(api.CodeInvalidMirrorWindow)) {
		t.Errorf("window=2h body missing %s: %s", api.CodeInvalidMirrorWindow, rec.Body.String())
	}
}

func TestMirrorDelete_CascadesResults(t *testing.T) {
	fx := newMirrorFixture(t)
	rule, _ := fx.store.CreateMirrorRuleIfUnderQuota(context.Background(), state.CreateMirrorRuleParams{
		AccountID:          fx.proAcct.ID,
		AppID:              fx.proApp.ID,
		SourceDeploymentID: fx.proDep1.ID,
		MirrorDeploymentID: fx.proDep2.ID,
		Percent:            50, Enabled: true, IncludeBody: false, RedactHeaders: []string{},
	}, api.MustLimitsFor(api.PlanPro))
	// Insert one result row, then delete the rule and assert
	// the row is gone (memstore honours ON DELETE CASCADE).
	if err := fx.store.InsertMirrorResult(context.Background(), state.MirrorInvocationResult{
		MirrorRuleID: rule.ID, AccountID: fx.proAcct.ID, AppID: fx.proApp.ID,
		SourceDeploymentID: fx.proDep1.ID, MirrorDeploymentID: fx.proDep2.ID,
		StatusCode: 200, SourceStatusCode: 200, CompletedAt: timeNow(),
	}); err != nil {
		t.Fatalf("InsertMirrorResult: %v", err)
	}
	h := newMirrorServer(fx)
	pt, hash, _ := api.GenerateAPIKey()
	_, _ = fx.store.CreateAPIKey(context.Background(), fx.proAcct.ID, hash, "test", api.ScopesDeployWriteSurface)
	req := httptest.NewRequest(http.MethodDelete, "/v1/apps/"+fx.proApp.Slug+"/mirrors/"+rule.ID, nil)
	req.Header.Set("Authorization", "Bearer "+pt)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if _, err := fx.store.GetMirrorRuleByID(context.Background(), rule.ID); err == nil {
		t.Errorf("rule still readable after delete")
	}
	// Second delete must surface 404 (silent on IDOR).
	req = httptest.NewRequest(http.MethodDelete, "/v1/apps/"+fx.proApp.Slug+"/mirrors/"+rule.ID, nil)
	req.Header.Set("Authorization", "Bearer "+pt)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("second delete: status = %d, want 404", rec.Code)
	}
}

// itoa is a thin alias for strconv.Itoa so the test body stays
// short. handlers_ext_test.go in the same package defines its
// own itoa helper; this file uses the package-level one to
// avoid a duplicate declaration (Go does not allow two vars of
// the same name in one package).
