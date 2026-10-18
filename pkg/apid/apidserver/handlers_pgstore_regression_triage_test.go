//go:build !no_pg

// adr: 20261017210000
package apidserver_test

import (
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"net/http"
	"net/url"
	"sync"
	"testing"
	"time"
)

func TestPGHandler_RegressionTriageGuardAndReplay(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	app := apidserver.SeedPGApp(t, e, "pg-triage")
	dep := uuid.NewString()
	route := "GET /orders"
	params := sqlc.UpsertRegressionObservationParams{AppID: pgtype.UUID{Bytes: uuid.MustParse(app.ID), Valid: true}, DeploymentID: pgtype.UUID{Bytes: uuid.MustParse(dep), Valid: true}, Route: route, P95Ms: 200, P95BaseMs: 100, AffectedCount: 20, RegressionFactor: pgNumeric(t, "2.00"), Kind: "latency", ErrorRatePct: pgNumeric(t, "0"), ErrorRateBasePct: pgNumeric(t, "0")}
	upsert := func() {
		t.Helper()
		if err := (*e.Store()).UpsertRegressionObservation(t.Context(), params); err != nil {
			t.Fatal(err)
		}
	}
	upsert()
	path := "/v1/apps/pg-triage/debug/regressions"
	review := func() api.DebugRegressionReview {
		t.Helper()
		rec := e.Do(t, http.MethodGet, path+"/review?"+url.Values{"deployment_id": {dep}, "route": {route}}.Encode(), nil, nil)
		if rec.Code != 200 {
			t.Fatalf("review=%d %s", rec.Code, rec.Body.String())
		}
		var v api.DebugRegressionReview
		if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		if v.AppID != app.ID || v.DeploymentID != dep || v.Route != route || v.Version == "" {
			t.Fatal(v)
		}
		return v
	}
	original := review()
	request := func(v api.DebugRegressionReview, action string) api.GuardedDebugRegressionActionRequest {
		return api.GuardedDebugRegressionActionRequest{DeploymentID: dep, Route: route, Action: action, ExpectedAppID: app.ID, ExpectedVersion: v.Version, Reason: "Investigate retained observation"}
	}
	// Two independently reviewed operators race: PostgreSQL permits one winner.
	codes := make(chan int, 2)
	var wg sync.WaitGroup
	for _, action := range []string{"acknowledge", "resolve"} {
		wg.Add(1)
		go func(action string) {
			defer wg.Done()
			rec := e.Do(t, http.MethodPost, path+"/actions", request(original, action), map[string]string{"Idempotency-Key": uuid.NewString()})
			codes <- rec.Code
		}(action)
	}
	wg.Wait()
	close(codes)
	counts := map[int]int{}
	for code := range codes {
		counts[code]++
	}
	if counts[200] != 1 || counts[409] != 1 {
		t.Fatal(counts)
	}
	changed := review()
	if changed.Version == original.Version {
		t.Fatal("triage did not invalidate version")
	}
	req := request(changed, "resolve")
	headers := map[string]string{"Idempotency-Key": uuid.NewString()}
	rec := e.Do(t, http.MethodPost, path+"/actions", req, headers)
	if rec.Code != 200 {
		t.Fatalf("apply=%d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	after := review()
	if after.State != "resolved" {
		t.Fatal("resolved rows cannot be reviewed")
	}
	replay := e.Do(t, http.MethodPost, path+"/actions", req, headers)
	if replay.Code != 200 || replay.Body.String() != body || review().Version != after.Version {
		t.Fatal("same-key retry did not replay original receipt")
	}
	// Detector refresh invalidates both prior state and timestamps, even if it
	// preserves the workflow state. Guarded retries must not overwrite it.
	upsert()
	detected := review()
	if detected.Version == after.Version {
		t.Fatal("detector update kept review version")
	}
	stale := e.Do(t, http.MethodPost, path+"/actions", request(after, "dismiss"), map[string]string{"Idempotency-Key": uuid.NewString()})
	if stale.Code != 409 {
		t.Fatalf("stale=%d %s", stale.Code, stale.Body.String())
	}
	// A legacy writer also rotates the version; no older client bypasses the
	// guard for an already-reviewed MCP action.
	legacy := api.DebugRegressionActionRequest{DeploymentID: dep, Route: route, Action: "dismiss"}
	guardless := e.Do(t, http.MethodPost, path+"/actions", legacy, nil)
	if guardless.Code != http.StatusBadRequest {
		t.Fatalf("guardless checked action=%d %s", guardless.Code, guardless.Body.String())
	}
	rec = e.Do(t, http.MethodPatch, path, legacy, nil)
	if rec.Code != 200 {
		t.Fatalf("legacy=%d %s", rec.Code, rec.Body.String())
	}
	dismissed := review()
	if dismissed.State != "dismissed" || dismissed.Version == detected.Version {
		t.Fatal(dismissed)
	}
	rec = e.Do(t, http.MethodPost, path+"/actions", request(dismissed, "reopen"), nil)
	if rec.Code != 200 {
		t.Fatalf("reopen=%d %s", rec.Code, rec.Body.String())
	}
	if review().State != "active" {
		t.Fatal("reopen failed")
	}

	atomicBefore := review()
	atomicReq := request(atomicBefore, "resolve")
	_, err := (*e.Pool()).Exec(t.Context(), `CREATE FUNCTION regression_receipt_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.key LIKE 'checked-regression:%' AND NEW.response_status = 200 THEN RAISE EXCEPTION 'injected receipt failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER regression_receipt_fail BEFORE INSERT OR UPDATE ON idempotency_keys FOR EACH ROW EXECUTE FUNCTION regression_receipt_fail()`)
	if err != nil {
		t.Fatal(err)
	}
	failed := e.Do(t, http.MethodPost, path+"/actions", atomicReq, nil)
	if failed.Code != 503 || review().Version != atomicBefore.Version {
		t.Fatalf("receipt failure committed action: %d %s", failed.Code, failed.Body.String())
	}
	if _, err := (*e.Pool()).Exec(t.Context(), `DROP TRIGGER regression_receipt_fail ON idempotency_keys; DROP FUNCTION regression_receipt_fail()`); err != nil {
		t.Fatal(err)
	}
	committed := e.Do(t, http.MethodPost, path+"/actions", atomicReq, map[string]string{"Idempotency-Key": "first-client-key"})
	if committed.Code != 200 {
		t.Fatalf("atomic retry=%d %s", committed.Code, committed.Body.String())
	}
	committedVersion := review().Version
	replayed := e.Do(t, http.MethodPost, path+"/actions", atomicReq, map[string]string{"Idempotency-Key": "different-client-key"})
	if replayed.Code != 200 || replayed.Body.String() != committed.Body.String() || review().Version != committedVersion || replayed.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatal("exact-body native replay did not recover atomic receipt")
	}

	expiryReq := request(review(), "dismiss")
	until := time.Now().UTC().Add(2 * time.Second).Truncate(time.Microsecond)
	expiryReq.DismissedUntil = until.Format(time.RFC3339Nano)
	expiryCommit := e.Do(t, http.MethodPost, path+"/actions", expiryReq, nil)
	if expiryCommit.Code != 200 {
		t.Fatalf("short dismissal=%d %s", expiryCommit.Code, expiryCommit.Body.String())
	}
	expiryVersion := review().Version
	time.Sleep(time.Until(until.Add(25 * time.Millisecond)))
	expiryReplay := e.Do(t, http.MethodPost, path+"/actions", expiryReq, nil)
	if expiryReplay.Code != 200 || expiryReplay.Body.String() != expiryCommit.Body.String() || review().Version != expiryVersion {
		t.Fatal("expired committed dismissal could not replay")
	}
	for _, invalidUntil := range []time.Time{time.Now().Add(-time.Minute), time.Now().Add(31 * 24 * time.Hour)} {
		bad := request(review(), "dismiss")
		bad.DismissedUntil = invalidUntil.UTC().Format(time.RFC3339Nano)
		rejected := e.Do(t, http.MethodPost, path+"/actions", bad, nil)
		if rejected.Code != 400 || review().Version != expiryVersion {
			t.Fatalf("invalid fresh dismissal=%d %s", rejected.Code, rejected.Body.String())
		}
	}
	other := apidserver.SeedPGApp(t, e, "pg-triage-other")
	req = request(review(), "resolve")
	req.ExpectedAppID = other.ID
	rec = e.Do(t, http.MethodPost, path+"/actions", req, nil)
	if rec.Code != 409 {
		t.Fatal("wrong app guard accepted", rec.Code)
	}
	rec = e.Do(t, http.MethodGet, "/v1/apps/pg-triage-other/debug/regressions/review?"+url.Values{"deployment_id": {dep}, "route": {route}}.Encode(), nil, nil)
	if rec.Code != 404 {
		t.Fatal("cross-app observation visible", rec.Code)
	}
}
