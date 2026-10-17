// adr: 1195
package apidserver_test

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestOperationTimelineRoute(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "timeline@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := store.CreateAccount(ctx, "timeline-foreign@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	key := func(account string, scopes []string) string {
		t.Helper()
		raw, hash, err := api.GenerateAPIKey()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.CreateAPIKey(ctx, account, hash, "timeline", scopes); err != nil {
			t.Fatal(err)
		}
		return raw
	}
	read, other := key(acct.ID, []string{api.ScopeAppsRead}), key(foreign.ID, api.ScopesAdminOnly)
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "timeline-exports", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	sibling, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "timeline-sibling", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	def, err := store.PutOperationDefinition(ctx, state.OperationDefinition{AccountID: acct.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{AppID: app.ID, Scope: dep.Scope, DeploymentID: dep.ID, Spec: api.OperationDefinitionSpec{Name: "export", Method: "POST", Path: "/exports", Owner: api.OperationOwnerPlatformTenant, ProgressStages: []string{"generating"}, InputSchema: []byte(`{"type":"object"}`), OutputSchema: []byte(`{"type":"object"}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	alice, _, err := store.CreatePlatformTenant(ctx, acct.ID, "alice", "Alice", 100)
	if err != nil {
		t.Fatal(err)
	}
	token, prefix, hash, err := api.GeneratePlatformTenantAccessToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreatePlatformTenantAccessToken(ctx, state.PlatformTenantAccessTokenInput{AccountID: acct.ID, TenantID: alice.ID, Name: "customer", Prefix: prefix, TokenHash: hash, Scopes: []string{api.ScopePlatformTenantOperationsRead}, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	admit := func(idempotency string) state.Operation {
		t.Helper()
		op, _, err := store.AdmitOperation(ctx, state.OperationAdmission{AccountID: acct.ID, DefinitionID: def.ID, PlatformTenantID: alice.ID, IdempotencyKey: idempotency, Input: []byte(`{}`)})
		if err != nil {
			t.Fatal(err)
		}
		return op
	}
	op, sister := admit("timeline"), admit("timeline-sister")
	instance, err := store.CreateInstance(ctx, app.ID, dep.ID, "stopped", 128, uuid.NewString(), "")
	if err != nil {
		t.Fatal(err)
	}
	inv, err := store.ClaimInvocationWithCap(ctx, op.CurrentInvocationID, instance.ID, 60, 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.StampOperationExecutionAttempt(ctx, inv.ID, instance.ID, inv.Attempts); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteKeyedInvocation(ctx, inv.ID, inv.Attempts, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}

	srv := apidserver.NewServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", apidserver.NoopNotifier{})
	server := httptest.NewServer(srv.Handler())
	t.Cleanup(server.Close)
	path := "/v1/apps/" + app.Slug + "/operations/" + op.ID + "/timeline"
	get := func(target, bearer string, code int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest("GET", target, nil)
		req.Header.Set("Authorization", "Bearer "+bearer)
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)
		if w.Code != code {
			t.Fatalf("GET %s: HTTP %d want %d: %s", target, w.Code, code, w.Body.String())
		}
		return w
	}

	whole := get(path+"?limit=100", read, 200)
	if whole.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("timeline is cacheable")
	}
	var page api.OperationTimelineResponse
	if err := json.Unmarshal(whole.Body.Bytes(), &page); err != nil || page.OperationID != op.ID || page.State != api.OperationSucceeded || page.NextCursor != "" {
		t.Fatalf("timeline page = %+v %v", page, err)
	}
	var attempt *api.OperationTimelineAttempt
	for _, e := range page.Entries {
		if e.Kind == api.OperationTimelineKindAttempt {
			attempt = e.Attempt
		}
	}
	if attempt == nil || attempt.Outcome != api.OperationAttemptSucceeded || attempt.Logs == nil || attempt.Logs.InstanceID != instance.ID {
		t.Fatalf("attempt entry = %+v in %s", attempt, whole.Body.String())
	}
	if strings.Contains(whole.Body.String(), `"input"`) {
		t.Fatal("timeline exposes operation input")
	}

	client := api.NewClient(server.URL, read).SetCompletionCache(nil)
	var paged []api.OperationTimelineEntry
	cursor := ""
	for range 50 {
		r, err := client.GetOperationTimeline(ctx, app.Slug, op.ID, 1, cursor)
		if err != nil || len(r.Entries) > 1 {
			t.Fatalf("client page: %+v %v", r, err)
		}
		paged = append(paged, r.Entries...)
		if r.NextCursor == "" {
			break
		}
		if len(r.NextCursor) > api.OperationHistoryCursorMaxBytes {
			t.Fatalf("cursor is %d bytes", len(r.NextCursor))
		}
		cursor = r.NextCursor
	}
	wantJSON, _ := json.Marshal(page.Entries)
	gotJSON, _ := json.Marshal(paged)
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("paged timeline differs:\n%s\n%s", gotJSON, wantJSON)
	}

	first := get(path+"?limit=1", read, 200)
	var firstPage api.OperationTimelineResponse
	if err := json.Unmarshal(first.Body.Bytes(), &firstPage); err != nil || firstPage.NextCursor == "" {
		t.Fatalf("first page: %+v %v", firstPage, err)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(firstPage.NextCursor)
	padded := base64.RawURLEncoding.EncodeToString([]byte(" " + string(raw)))
	sisterPath := "/v1/apps/" + app.Slug + "/operations/" + sister.ID + "/timeline"
	for _, target := range []string{
		path + "?cursor=" + url.QueryEscape("not base64!"),
		path + "?cursor=" + padded,
		sisterPath + "?cursor=" + firstPage.NextCursor,
		path + "?limit=0",
		path + "?limit=101",
		path + "?limit=1&limit=2",
		path + "?after=1",
		path + "?cursor=" + strings.Repeat("A", api.OperationHistoryCursorMaxBytes+1),
	} {
		get(target, read, 400)
	}
	get(path, other, 404)
	get("/v1/apps/"+sibling.Slug+"/operations/"+op.ID+"/timeline", read, 404)
	get("/v1/apps/"+app.Slug+"/operations/"+uuid.NewString()+"/timeline", read, 404)
	get(path, token, 403)
}
