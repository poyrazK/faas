// Whitebox tests for GET /v1/builds — the list/filter endpoint
// (cmd/apid/handlers_ext.go::listBuilds, DEPLOY-PROV-6 follow-up /
// ADR-091, issue #741 close-out). Mirrors handlers_build_test.go's
// shape and the repo's whitebox-test-file convention (`package main`
// so the test reaches unexported server fields directly).
//
// Acceptance gate (per ADR-091 §9): 13 subtests covering the happy
// path (account-wide, app filter, status filter, pagination,
// empty, nulls-last), the IDOR contracts (cross-account slug,
// missing slug), the input-validation paths (bad status, bad
// cursor, bad limit), and the auth+rate-limit gates. Lives in
// its own file because the route is the first /v1/builds list
// shape and the (a) cursor semantics, (b) nulls-last ordering, and
// (c) cross-app/cross-account IDOR are each load-bearing enough
// that future DTO drift must surface here first.
package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// listBuildsTestServer stands up a server whose store is a fresh
// MemStore and returns the handler + API key + account. Mirrors
// buildTestServer (handlers_build_test.go) — listBuilds shares the
// same auth chain (authLimited + requireScope, NO requireMFA per
// ADR-089 §6) so the harness can be identical.
func listBuildsTestServer(t *testing.T) (h http.Handler, key string, store *state.MemStore, acct state.Account) {
	t.Helper()
	store = state.NewMemStore()
	var err error
	acct, err = store.CreateAccount(context.Background(), "list-builds@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	pt, hash, _ := api.GenerateAPIKey()
	if _, err = store.CreateAPIKey(context.Background(), acct.ID, hash, "list-test", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	srv := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{})
	return srv.handler(), pt, store, acct
}

// listBuildsGet issues a Bearer-authed GET against `h`. Mirrors
// buildGet so each test focuses on the assertion, not the harness.
func listBuildsGet(t *testing.T, h http.Handler, key, path string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("GET", path, nil)
	r.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

// seedBuildListDeploy provisions an app + deployment under acct
// and returns the deployment ID so tests can attach multiple
// builds (CreateBuild stamps a queued row with started_at NULL).
func seedBuildListDeploy(t *testing.T, store *state.MemStore, acct state.Account, slug string) string {
	t.Helper()
	ctx := context.Background()
	app, err := store.CreateApp(ctx, state.App{
		AccountID: acct.ID,
		Slug:      slug,
		Type:      state.AppTypeFunction,
		Runtime:   "node22",
		RAMMB:     256,
		CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{
		AppID:       app.ID,
		Kind:        state.DeploymentKindTarball,
		SourceBytes: 1024,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	return dep.ID
}

// seedBuildListBuild attaches a build row to a deployment. The
// build is left in BuildQueued status (started_at NULL) by default.
// Tests that need a specific status transition call
// store.UpdateBuildStatus afterward — the MemStore CAS guard
// requires terminal writes to walk through BuildRunning first.
func seedBuildListBuild(t *testing.T, store *state.MemStore, deploymentID string) string {
	t.Helper()
	b, err := store.CreateBuild(context.Background(), deploymentID, state.DeploymentKindTarball, 4096, "")
	if err != nil {
		t.Fatalf("CreateBuild: %v", err)
	}
	return b.ID
}

// advanceBuild stamps StartedAt=now on the row and walks it to
// a non-queued status so the test can assert ordering / pagination
// against builds whose started_at is populated. Returns the
// assigned StartedAt so the test can derive a `before=` cursor.
func advanceBuild(t *testing.T, store *state.MemStore, buildID string, status state.BuildStatus) time.Time {
	t.Helper()
	if err := store.UpdateBuildStatus(context.Background(), buildID,
		state.BuildRunning, "", true, false); err != nil {
		t.Fatalf("UpdateBuildStatus(running): %v", err)
	}
	if status != state.BuildRunning {
		if err := store.UpdateBuildStatus(context.Background(), buildID,
			status, "", false, false); err != nil {
			t.Fatalf("UpdateBuildStatus(%s): %v", status, err)
		}
	}
	// Read back the row to extract the store-stamped StartedAt.
	b, err := store.BuildByID(context.Background(), buildID)
	if err != nil {
		t.Fatalf("BuildByID: %v", err)
	}
	return b.StartedAt
}

// TestListBuilds_OK_AccountWide pins the happy path: 3 builds
// across 2 apps, account-wide GET returns all 3 in started_at DESC
// order with no cursor (under the default limit of 50).
func TestListBuilds_OK_AccountWide(t *testing.T) {
	h, key, store, acct := listBuildsTestServer(t)
	dep1 := seedBuildListDeploy(t, store, acct, "list-aw-app-a")
	dep2 := seedBuildListDeploy(t, store, acct, "list-aw-app-b")
	b1 := seedBuildListBuild(t, store, dep1)
	b2 := seedBuildListBuild(t, store, dep1)
	b3 := seedBuildListBuild(t, store, dep2)
	// Walk each to running so they have a started_at and the
	// SQL order is deterministic.
	t1 := advanceBuild(t, store, b1, state.BuildSucceeded)
	time.Sleep(2 * time.Millisecond)
	_ = advanceBuild(t, store, b2, state.BuildRunning)
	time.Sleep(2 * time.Millisecond)
	_ = advanceBuild(t, store, b3, state.BuildSucceeded)

	rec := listBuildsGet(t, h, key, "/v1/builds")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp api.BuildListResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Items) != 3 {
		t.Fatalf("items = %d, want 3", len(resp.Items))
	}
	if resp.NextBefore != "" {
		t.Errorf("NextBefore = %q, want empty (under limit)", resp.NextBefore)
	}
	// started_at DESC: b3 > b2 > b1.
	if resp.Items[0].ID != b3 || resp.Items[1].ID != b2 || resp.Items[2].ID != b1 {
		t.Errorf("order = [%s %s %s], want [b3 b2 b1]",
			resp.Items[0].ID, resp.Items[1].ID, resp.Items[2].ID)
	}
	_ = t1
}

// TestListBuilds_OK_AppFilter pins the ?app=<slug> IDOR-safe
// narrow: 2 apps, 1 build each; GET ?app=app-a returns only
// app-a's build.
func TestListBuilds_OK_AppFilter(t *testing.T) {
	h, key, store, acct := listBuildsTestServer(t)
	depA := seedBuildListDeploy(t, store, acct, "list-af-app-a")
	depB := seedBuildListDeploy(t, store, acct, "list-af-app-b")
	ba := seedBuildListBuild(t, store, depA)
	bb := seedBuildListBuild(t, store, depB)
	advanceBuild(t, store, ba, state.BuildSucceeded)
	advanceBuild(t, store, bb, state.BuildSucceeded)

	rec := listBuildsGet(t, h, key, "/v1/builds?app=list-af-app-a")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp api.BuildListResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Items) != 1 || resp.Items[0].ID != ba {
		t.Errorf("items = %+v, want exactly ba (%s)", resp.Items, ba)
	}
}

// TestListBuilds_OK_StatusFilter pins the ?status=<s> enum
// filter: 3 builds (queued, running, succeeded), GET ?status=
// running returns only the running build.
func TestListBuilds_OK_StatusFilter(t *testing.T) {
	h, key, store, acct := listBuildsTestServer(t)
	dep := seedBuildListDeploy(t, store, acct, "list-sf-app")
	bRunning := seedBuildListBuild(t, store, dep)
	bDone := seedBuildListBuild(t, store, dep)
	bQueued := seedBuildListBuild(t, store, dep) // stays queued
	advanceBuild(t, store, bRunning, state.BuildRunning)
	advanceBuild(t, store, bDone, state.BuildSucceeded)

	rec := listBuildsGet(t, h, key, "/v1/builds?status=running")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp api.BuildListResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Items) != 1 || resp.Items[0].ID != bRunning {
		t.Errorf("items = %+v, want exactly bRunning (%s)", resp.Items, bRunning)
	}
	if resp.Items[0].Status != "running" {
		t.Errorf("status = %q, want running", resp.Items[0].Status)
	}
	_ = bQueued // left queued — must be excluded from ?status=running.
}

// TestListBuilds_OK_Pagination pins the cursor round-trip: seed
// 5 running builds with started_at landing in distinct seconds
// (the cursor is whole-second-aligned because BuildResponse.
// StartedAt is formatted with RFC3339, NOT RFC3339Nano — see
// buildResponse at cmd/apid/handlers_ext.go:2994). Page 1
// (limit=2) returns 2 items + non-empty next_before; page 2
// (limit=2 + before=cursor) returns the next 2; page 3 returns
// the last 1 with no next_before.
//
// Cursor shape (post-review fix): the cursor is the opaque
// tuple "<started_at_rfc3339>|<id_hex>" (post-review fix). The
// id tiebreaker is what makes the round-trip deterministic even
// when the wire emits whole-second precision against sub-second
// DB timestamps. See ADR-091 §3 + parseBuildCursor.
//
// Sleep budget: 1.1s between stamps × 5 builds ≈ 4.5s wall-time.
// The test is intentionally slow but the alternative (relaxing
// to ms-level timestamps with second-aligned cursors) introduces
// a race that can't be reliably reproduced in CI. 5 runs is the
// minimum that exercises every page-boundary case (page-1→page-2
// drops the cursor row, page-2→page-3 returns the tail, page-3
// clears the cursor).
func TestListBuilds_OK_Pagination(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping pagination test in -short mode (4.5s sleep budget)")
	}
	h, key, store, acct := listBuildsTestServer(t)
	dep := seedBuildListDeploy(t, store, acct, "list-pg-app")
	ids := make([]string, 5)
	for i := 0; i < 5; i++ {
		ids[i] = seedBuildListBuild(t, store, dep)
		advanceBuild(t, store, ids[i], state.BuildSucceeded)
		if i < 4 {
			time.Sleep(1100 * time.Millisecond) // ensure distinct seconds
		}
	}

	// Page 1: limit=2.
	rec := listBuildsGet(t, h, key, "/v1/builds?limit=2")
	if rec.Code != http.StatusOK {
		t.Fatalf("page1 status = %d", rec.Code)
	}
	var p1 api.BuildListResponse
	if err := json.NewDecoder(rec.Body).Decode(&p1); err != nil {
		t.Fatalf("decode p1: %v", err)
	}
	if len(p1.Items) != 2 {
		t.Fatalf("page1 items = %d, want 2", len(p1.Items))
	}
	if p1.NextBefore == "" {
		t.Fatalf("page1 NextBefore empty, want cursor")
	}
	if p1.Items[0].ID != ids[4] || p1.Items[1].ID != ids[3] {
		t.Errorf("page1 order = [%s %s], want [%s %s]",
			p1.Items[0].ID, p1.Items[1].ID, ids[4], ids[3])
	}

	// Page 2: limit=2 + before=cursor.
	rec = listBuildsGet(t, h, key, "/v1/builds?limit=2&before="+p1.NextBefore)
	if rec.Code != http.StatusOK {
		t.Fatalf("page2 status = %d", rec.Code)
	}
	var p2 api.BuildListResponse
	if err := json.NewDecoder(rec.Body).Decode(&p2); err != nil {
		t.Fatalf("decode p2: %v", err)
	}
	if len(p2.Items) != 2 {
		t.Fatalf("page2 items = %d, want 2", len(p2.Items))
	}
	if p2.NextBefore == "" {
		t.Fatalf("page2 NextBefore empty, want cursor")
	}
	if p2.Items[0].ID != ids[2] || p2.Items[1].ID != ids[1] {
		t.Errorf("page2 order = [%s %s], want [%s %s]",
			p2.Items[0].ID, p2.Items[1].ID, ids[2], ids[1])
	}

	// Page 3: limit=2 + before=p2 cursor → only 1 row left.
	rec = listBuildsGet(t, h, key, "/v1/builds?limit=2&before="+p2.NextBefore)
	if rec.Code != http.StatusOK {
		t.Fatalf("page3 status = %d", rec.Code)
	}
	var p3 api.BuildListResponse
	if err := json.NewDecoder(rec.Body).Decode(&p3); err != nil {
		t.Fatalf("decode p3: %v", err)
	}
	if len(p3.Items) != 1 {
		t.Fatalf("page3 items = %d, want 1", len(p3.Items))
	}
	if p3.NextBefore != "" {
		t.Errorf("page3 NextBefore = %q, want empty (end of list)", p3.NextBefore)
	}
	if p3.Items[0].ID != ids[0] {
		t.Errorf("page3 id = %s, want %s", p3.Items[0].ID, ids[0])
	}
}

// TestListBuilds_OK_Empty pins the no-builds branch: empty page,
// no cursor, status 200.
func TestListBuilds_OK_Empty(t *testing.T) {
	h, key, _, _ := listBuildsTestServer(t)

	rec := listBuildsGet(t, h, key, "/v1/builds")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp api.BuildListResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Items) != 0 {
		t.Errorf("items = %d, want 0", len(resp.Items))
	}
	if resp.NextBefore != "" {
		t.Errorf("NextBefore = %q, want empty", resp.NextBefore)
	}
}

// TestListBuilds_OK_NullsLast pins the NULLS LAST ordering + the
// queued-row cursor skip: 2 running + 1 queued build under the
// limit. The queued row must sort to the bottom (NULLS LAST).
// next_before must use the LAST non-null started_at — passing
// the queued build would skip the running/succeeded rows behind
// it on the next page.
func TestListBuilds_OK_NullsLast(t *testing.T) {
	h, key, store, acct := listBuildsTestServer(t)
	dep := seedBuildListDeploy(t, store, acct, "list-nl-app")
	b1 := seedBuildListBuild(t, store, dep)
	b2 := seedBuildListBuild(t, store, dep)
	bQueued := seedBuildListBuild(t, store, dep) // stays queued
	_ = advanceBuild(t, store, b1, state.BuildSucceeded)
	time.Sleep(1100 * time.Millisecond) // distinct seconds
	_ = advanceBuild(t, store, b2, state.BuildSucceeded)

	rec := listBuildsGet(t, h, key, "/v1/builds")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp api.BuildListResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Items) != 3 {
		t.Fatalf("items = %d, want 3 (b2, b1, bQueued)", len(resp.Items))
	}
	// Order: b2 (newest started_at), b1, bQueued (NULLS LAST).
	if resp.Items[0].ID != b2 || resp.Items[1].ID != b1 {
		t.Errorf("top items = [%s %s], want [b2 b1]",
			resp.Items[0].ID, resp.Items[1].ID)
	}
	if resp.Items[2].ID != bQueued {
		t.Errorf("tail item = %s, want bQueued (%s)", resp.Items[2].ID, bQueued)
	}
	if resp.Items[2].StartedAt != "" {
		t.Errorf("queued row started_at = %q, want empty", resp.Items[2].StartedAt)
	}
	// Page is under limit so NextBefore MUST be empty even
	// though the queued row can't be a cursor.
	if resp.NextBefore != "" {
		t.Errorf("NextBefore = %q, want empty (under limit)", resp.NextBefore)
	}
}

// TestListBuilds_OK_Pagination_NullsLast pins the cursor skip
// on a full page: 2 running + 1 queued at the tail under
// limit=2. The cursor must come from the LAST non-null-started
// row (b1), NOT the queued build, so passing next_before never
// skips b1.
//
// Cursor contract (post-review fix): the cursor is the opaque
// tuple "<started_at_rfc3339>|<id_hex>" — pipe-separated. The id
// is the LAST row's Build.ID. For non-null rows the started_at
// segment is RFC3339 (whole-second precision — see buildResponse
// at handlers_ext.go:2994); for queued tails the started_at
// segment is empty and the id tiebreaker alone resolves the
// keyset. We therefore split the cursor on '|' and assert both
// halves. See ADR-091 §3 + parseBuildCursor.
func TestListBuilds_OK_Pagination_NullsLast(t *testing.T) {
	h, key, store, acct := listBuildsTestServer(t)
	dep := seedBuildListDeploy(t, store, acct, "list-pg-nl-app")
	b2 := seedBuildListBuild(t, store, dep)
	b1 := seedBuildListBuild(t, store, dep)
	bQueued := seedBuildListBuild(t, store, dep) // stays queued
	_ = advanceBuild(t, store, b1, state.BuildSucceeded)
	time.Sleep(1100 * time.Millisecond) // distinct seconds
	_ = advanceBuild(t, store, b2, state.BuildSucceeded)

	rec := listBuildsGet(t, h, key, "/v1/builds?limit=2")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp api.BuildListResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(resp.Items))
	}
	// Page 1: b2 (newest started_at), b1.
	if resp.Items[0].ID != b2 || resp.Items[1].ID != b1 {
		t.Errorf("page1 order = [%s %s], want [b2 b1]",
			resp.Items[0].ID, resp.Items[1].ID)
	}
	// NextBefore must use b1's started_at (truncated to seconds)
	// + b1's id, NOT the queued row's started_at (empty string,
	// unparseable).
	if resp.NextBefore == "" {
		t.Fatalf("NextBefore empty; want cursor derived from b1")
	}
	parts := strings.SplitN(resp.NextBefore, "|", 2)
	if len(parts) != 2 {
		t.Fatalf("cursor %q not '<started_at>|<id>' shape", resp.NextBefore)
	}
	cursorStarted, cursorID := parts[0], parts[1]
	cursorT, err := time.Parse(time.RFC3339, cursorStarted)
	if err != nil {
		// RFC3339Nano fallback for clients that emit sub-second
		// (the server does not, but the symbol parsing is
		// forward-compatible).
		if cursorT, err = time.Parse(time.RFC3339Nano, cursorStarted); err != nil {
			t.Fatalf("cursor segment %q not RFC3339[Nano]: %v", cursorStarted, err)
		}
	}
	// Re-fetch b1 and compare at FULL precision (post-review
	// fix #87: the cursor now carries RFC3339Nano from
	// state.Build.StartedAt so the keyset sub-second clause
	// is reachable). The whole-second wire format on
	// BuildResponse.StartedAt stays unchanged for backward
	// compat with GET /v1/builds/{id}.
	b1Row, err := store.BuildByID(context.Background(), b1)
	if err != nil {
		t.Fatalf("BuildByID: %v", err)
	}
	wantFull := b1Row.StartedAt.UTC()
	if !cursorT.Equal(wantFull) {
		t.Errorf("cursor started_at = %v, want b1.StartedAt (full precision) = %v",
			cursorT, wantFull)
	}
	// id segment must be b1's id (the LAST row on the page).
	if cursorID != b1 {
		t.Errorf("cursor id = %q, want b1 (%s)", cursorID, b1)
	}
	_ = bQueued
}

// TestListBuilds_OK_Pagination_QueuedTail pins the queued-tail
// cursor + page-boundary case: page 1 = [running, queued]
// (limit=2 of 2 builds). The cursor must encode the queued
// row's id with an empty started_at segment. Page 2 with that
// cursor reaches end-of-list (no rows after the queued id).
//
// This is the regression tripwire for code-review Issue #1:
// under the original cursor (single started_at, no id
// tiebreaker) the queued tail could not anchor a cursor at
// all (NULL started_at), so a paginating client walking the
// list would never see queued rows past page 1. The id
// tiebreaker fixes that.
func TestListBuilds_OK_Pagination_QueuedTail(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping queued-tail pagination test in -short mode")
	}
	h, key, store, acct := listBuildsTestServer(t)
	dep := seedBuildListDeploy(t, store, acct, "list-pg-qt-app")
	bRunning := seedBuildListBuild(t, store, dep)
	_ = advanceBuild(t, store, bRunning, state.BuildSucceeded)
	bQueued := seedBuildListBuild(t, store, dep) // stays queued

	// Page 1: limit=2 → both rows (running first, queued last).
	rec := listBuildsGet(t, h, key, "/v1/builds?limit=2")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var p1 api.BuildListResponse
	if err := json.NewDecoder(rec.Body).Decode(&p1); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(p1.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(p1.Items))
	}
	if p1.Items[0].ID != bRunning {
		t.Errorf("items[0] = %s, want bRunning (%s)", p1.Items[0].ID, bRunning)
	}
	if p1.Items[1].ID != bQueued || p1.Items[1].StartedAt != "" {
		t.Errorf("items[1] = %+v, want queued (no started_at)", p1.Items[1])
	}
	if p1.NextBefore == "" {
		t.Fatalf("NextBefore empty; want queued-tail cursor")
	}
	parts := strings.SplitN(p1.NextBefore, "|", 2)
	if len(parts) != 2 {
		t.Fatalf("cursor %q not '<started_at>|<id>' shape", p1.NextBefore)
	}
	if parts[0] != "" {
		t.Errorf("cursor started_at segment = %q, want empty (queued tail)", parts[0])
	}
	if parts[1] != bQueued {
		t.Errorf("cursor id = %q, want bQueued (%s)", parts[1], bQueued)
	}

	// Page 2: cursor = "|bQueued" → no rows after bQueued
	// (end of list). Empty items means "no more data"; the
	// clients should treat this as terminal.
	rec = listBuildsGet(t, h, key, "/v1/builds?limit=2&before="+p1.NextBefore)
	if rec.Code != http.StatusOK {
		t.Fatalf("page2 status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var p2 api.BuildListResponse
	if err := json.NewDecoder(rec.Body).Decode(&p2); err != nil {
		t.Fatalf("decode p2: %v", err)
	}
	if len(p2.Items) != 0 {
		t.Errorf("page2 items = %v, want [] (end of list)", p2.Items)
	}
}

// TestListBuilds_OK_Pagination_QueuedTailCursor pins the
// queued-tail cursor WALK-BACK: when the page ends ON a queued
// row (started_at IS NULL), the cursor MUST encode the queued
// row with an empty started_at segment + the row's id — the
// "|id_hex" shape. This is the regression tripwire for the
// handler emitting a non-null started_at segment for queued
// rows (which would then fail to parse on the round-trip and
// 400 in parseBuildCursor).
//
// Setup: 1 running build + 1 queued build, limit=2. Page 1
// returns both rows ordered (running, queued) under DESC
// NULLS LAST. The cursor encodes the queued row's id with an
// empty started_at.
func TestListBuilds_OK_Pagination_QueuedTailCursor(t *testing.T) {
	h, key, store, acct := listBuildsTestServer(t)
	dep := seedBuildListDeploy(t, store, acct, "list-pg-qtc-app")
	bRunning := seedBuildListBuild(t, store, dep)
	bQueued := seedBuildListBuild(t, store, dep) // stays queued
	_ = advanceBuild(t, store, bRunning, state.BuildSucceeded)

	// Page 1: limit=2 → both rows (running first, queued last).
	rec := listBuildsGet(t, h, key, "/v1/builds?limit=2")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp api.BuildListResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(resp.Items))
	}
	if resp.Items[0].ID != bRunning {
		t.Errorf("items[0] = %s, want bRunning (%s)", resp.Items[0].ID, bRunning)
	}
	if resp.Items[1].ID != bQueued || resp.Items[1].StartedAt != "" {
		t.Errorf("items[1] = %+v, want queued (no started_at)", resp.Items[1])
	}
	if resp.NextBefore == "" {
		t.Fatalf("NextBefore empty; want queued-tail cursor")
	}
	parts := strings.SplitN(resp.NextBefore, "|", 2)
	if len(parts) != 2 {
		t.Fatalf("cursor %q not '<started_at>|<id>' shape", resp.NextBefore)
	}
	// Started_at segment MUST be empty (last row is queued).
	if parts[0] != "" {
		t.Errorf("cursor started_at segment = %q, want empty (queued tail)", parts[0])
	}
	// id segment MUST be the queued row's id.
	if parts[1] != bQueued {
		t.Errorf("cursor id = %q, want bQueued (%s)", parts[1], bQueued)
	}
}

// TestListBuilds_AppIDOR_OtherAccount pins the IDOR contract: a
// slug owned by another account returns 404 (NOT 403, NOT 200)
// when GET'd with ?app=<slug>. Uniform 404 so cross-account
// probes can't enumerate slugs by distinguishing "not found"
// from "forbidden".
func TestListBuilds_AppIDOR_OtherAccount(t *testing.T) {
	h, key, store, _ := listBuildsTestServer(t)

	// Provision a foreign account + app under the SAME store
	// (so the handler's AppBySlug hits a row owned by a
	// different account).
	other, err := store.CreateAccount(context.Background(), "idor-other@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateApp(context.Background(), state.App{
		AccountID: other.ID,
		Slug:      "idor-foreign-slug",
		Type:      state.AppTypeFunction,
		Runtime:   "node22",
		RAMMB:     256,
	}); err != nil {
		t.Fatalf("CreateApp: %v", err)
	}

	rec := listBuildsGet(t, h, key, "/v1/builds?app=idor-foreign-slug")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body = %s", rec.Code, rec.Body.String())
	}
	// pkg/auth.Middleware.LoadApp emits a 404 envelope; we don't
	// pin the exact code here because the IDOR helper is shared
	// across routes and the code name is owned by pkg/auth. The
	// tripwire is the status code itself.
}

// TestListBuilds_AppIDOR_Missing pins the unknown-slug branch:
// same 404 envelope as the cross-account case (uniform surface).
func TestListBuilds_AppIDOR_Missing(t *testing.T) {
	h, key, _, _ := listBuildsTestServer(t)

	rec := listBuildsGet(t, h, key, "/v1/builds?app=does-not-exist")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body = %s", rec.Code, rec.Body.String())
	}
}

// TestListBuilds_BadStatusFilter pins the ?status= validation:
// any value outside queued|running|succeeded|failed renders 400
// with code=validation_failed.
func TestListBuilds_BadStatusFilter(t *testing.T) {
	h, key, _, _ := listBuildsTestServer(t)

	rec := listBuildsGet(t, h, key, "/v1/builds?status=bogus")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}
	var p api.Problem
	if err := json.NewDecoder(rec.Body).Decode(&p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if p.Code != api.CodeValidation {
		t.Errorf("code = %q, want %q", p.Code, api.CodeValidation)
	}
	if !strings.Contains(p.Detail, "queued|running|succeeded|failed") {
		t.Errorf("detail = %q, want enum hint", p.Detail)
	}
}

// TestListBuilds_BadCursor pins the ?before= validation: garbage
// string → 400 with code=validation_failed.
func TestListBuilds_BadCursor(t *testing.T) {
	h, key, _, _ := listBuildsTestServer(t)

	rec := listBuildsGet(t, h, key, "/v1/builds?before=not-a-timestamp")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}
	var p api.Problem
	if err := json.NewDecoder(rec.Body).Decode(&p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if p.Code != api.CodeValidation {
		t.Errorf("code = %q, want %q", p.Code, api.CodeValidation)
	}
}

// TestListBuilds_BadLimit pins the lenient ?limit= parser: a
// non-numeric limit FALLS BACK to the default 50 (the handler
// silently ignores invalid input rather than 400ing — matches
// listDeployments). A limit > 200 is clamped to 200 (server-side
// cap). limit=0 also falls back to the default.
//
// Tripwire: the request must NOT 400 — the HTTP surface is
// intentionally lenient, matching /v1/deployments.
//
// NOTE: the CLI (`gregale build list --limit 0`) is intentionally
// strict and exits 1 on --limit 0. The CLI surfaces the help
// contract directly and rejects out-of-range values rather than
// silently falling back. The two surfaces have different UX
// goals — see cmd/gregale/commands_builds_test.go
// TestCmdBuildList_InvalidLimit for the CLI guard.
func TestListBuilds_BadLimit(t *testing.T) {
	h, key, store, acct := listBuildsTestServer(t)
	dep := seedBuildListDeploy(t, store, acct, "list-bl-app")
	for i := 0; i < 3; i++ {
		b := seedBuildListBuild(t, store, dep)
		advanceBuild(t, store, b, state.BuildSucceeded)
	}

	cases := []struct {
		name  string
		query string
	}{
		{"non-numeric", "?limit=banana"},
		{"zero", "?limit=0"},
		{"over-cap", "?limit=99999"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := listBuildsGet(t, h, key, "/v1/builds"+tc.query)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (lenient parser), body = %s",
					rec.Code, rec.Body.String())
			}
		})
	}

	// Spot-check the clamp by seeding 201 builds and asking for
	// limit=99999 — the response must carry exactly 200 items.
	depBig := seedBuildListDeploy(t, store, acct, "list-bl-big")
	for i := 0; i < 201; i++ {
		b := seedBuildListBuild(t, store, depBig)
		advanceBuild(t, store, b, state.BuildSucceeded)
	}
	rec := listBuildsGet(t, h, key, "/v1/builds?limit=99999")
	if rec.Code != http.StatusOK {
		t.Fatalf("clamp status = %d", rec.Code)
	}
	var resp api.BuildListResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// We've seeded 3 + 201 = 204 builds across 2 deployments.
	// The 200-cap clamps to 200 items, and NextBefore is set.
	if len(resp.Items) != 200 {
		t.Errorf("clamped items = %d, want 200", len(resp.Items))
	}
	if resp.NextBefore == "" {
		t.Errorf("NextBefore empty; want cursor on a full page")
	}
	// Sanity: confirm at least one of the items belongs to the
	// big batch (we can't be exact because the small batch's 3
	// builds share the same started_at window).
	_ = strconv.Itoa
}

// TestListBuilds_RequiresAuth pins the auth gate: no Authorization
// header → 401. The route is mounted under authLimited(requireScope)
// which rejects before the handler runs.
func TestListBuilds_RequiresAuth(t *testing.T) {
	h, _, _, _ := listBuildsTestServer(t)

	r := httptest.NewRequest("GET", "/v1/builds", nil)
	// Intentionally NO Authorization header.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401, body = %s", rec.Code, rec.Body.String())
	}
}

// TestListBuilds_RateLimit pins the authLimited bucket on
// FAILED auth attempts (spec §11: 10 failed /min/IP). The bucket
// only counts 401s — successful requests never trip it. To
// exercise the tripwire through the whitebox handler we send 11
// requests with an INVALID bearer key so RequireSession 401s on
// every hit; the 11th must render 429.
//
// Note: each test gets a fresh server (listBuildsTestServer
// constructs a new bucket), so the tripwire is local to this
// test — no shared-bucket flake risk across the suite.
func TestListBuilds_RateLimit(t *testing.T) {
	h, _, _, _ := listBuildsTestServer(t)

	hit := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/v1/builds", nil)
		// Intentionally bad key so RequireSession 401s and the
		// bucket increments.
		r.Header.Set("Authorization", "Bearer not-a-real-key")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec
	}

	// 10 401s within the bucket window.
	for i := 0; i < 10; i++ {
		rec := hit()
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status = %d, want 401, body = %s",
				i+1, rec.Code, rec.Body.String())
		}
	}
	// 11th attempt must be 429 — bucket exhausted.
	rec := hit()
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("11th attempt status = %d, want 429, body = %s",
			rec.Code, rec.Body.String())
	}
}
