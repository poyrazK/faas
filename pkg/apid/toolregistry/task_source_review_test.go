package toolregistry_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/toolregistry"
	"github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/tasktoolsource"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

func TestHostedTaskSourceOwnerMemory(t *testing.T) {
	s := state.NewMemStore()
	exerciseHostedSourceOwner(t, s, func() state.Store { return s })
}

func TestHostedTaskSourceOwnerPostgres(t *testing.T) {
	p := pgtest.OpenMigrated(t)
	exerciseHostedSourceOwner(t, state.NewPgStore(p), func() state.Store { return state.NewPgStore(p) })
}

func exerciseHostedSourceOwner(t *testing.T, s state.Store, reopen func() state.Store) {
	t.Helper()
	f := newHostedSourceFixture(t, s)
	ctx := t.Context()
	key := func(label string, scopes ...string) state.APIKey {
		t.Helper()
		hash := sha256.Sum256([]byte(f.account.ID + label))
		k, err := s.CreateAPIKey(ctx, f.account.ID, hash[:], label, scopes)
		if err != nil {
			t.Fatal(err)
		}
		return k
	}
	owner := key("source-owner", api.ScopeAdmin)
	agentOwner := key("source-agent-admin", api.ScopeAdmin, api.ScopeAgent)
	login := key(api.MCPLoginKeyLabel, api.ScopeAdmin)
	other := key("other-source-publisher", api.ScopeAgent, api.ScopeAppsRead, api.ScopeDeployWrite)
	call := func(method, id, suffix string, in any, k *state.APIKey) *httptest.ResponseRecorder {
		body, _ := json.Marshal(in)
		r := httptest.NewRequestWithContext(ctx, method, "/v1/task-tool-source-intents/"+id+suffix, bytes.NewReader(body))
		r.SetPathValue("id", id)
		r.Header.Set("Idempotency-Key", "must-not-cache-review")
		r = r.WithContext(middleware.WithPrincipal(ctx, f.account, k, nil))
		w := httptest.NewRecorder()
		f.executor.HostedTaskSourceIntent()(w, r, f.account)
		return w
	}
	decode := func(w *httptest.ResponseRecorder, want string) api.HostedTaskSourceIntent {
		t.Helper()
		var r api.HostedTaskSourceIntent
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &r) != nil || tasktoolsource.ValidateRecord(r, f.account.ID, r.ID) != nil || r.ReviewStatus != want || r.ReadyToApply || r.BindingsVerified || !r.OwnerAuthorizationRequired {
			t.Fatalf("source owner review %s: %d %s", want, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "SOURCE_SENTINEL_MUST_STAY_PRIVATE") || strings.Contains(w.Body.String(), "ciphertext") {
			t.Fatal("review response exposed retained source")
		}
		return r
	}
	id := toolpublication.Digest([]byte(f.account.ID + ":owner"))
	pending := decode(call("PUT", id, "", f.request, &f.key), "pending")
	in := api.HostedTaskSourceDecision{IntentSHA256: pending.IntentSHA256, ReviewSHA256: pending.ReviewSHA256, Decision: "approve"}
	for _, forbidden := range []state.APIKey{f.key, agentOwner, login} {
		if w := call("PUT", id, "/decision", in, &forbidden); w.Code == 200 {
			t.Fatal("agent or MCP-login credential approved retained source")
		}
	}
	if w := call("POST", id, "/check", api.HostedTaskSourceCheck{ReviewSHA256: pending.ReviewSHA256}, &other); w.Code != 404 {
		t.Fatalf("another requester checked review: %d", w.Code)
	}
	for _, field := range []string{"intent", "review"} {
		bad := in
		if field == "intent" {
			bad.IntentSHA256 = strings.Repeat("f", 64)
		} else {
			bad.ReviewSHA256 = strings.Repeat("f", 64)
		}
		if w := call("PUT", id, "/decision", bad, &owner); w.Code != 409 {
			t.Fatal("owner decision was not bound to both digests", field, w.Code)
		}
	}
	journal := s.(state.HostedTaskSourceIntentStore)
	for _, forbidden := range []state.APIKey{f.key, agentOwner, login, other} {
		if _, err := journal.ChangeHostedTaskSourceIntent(ctx, f.account.ID, id, "decide", in, state.HostedTaskSourceOwner{KeyID: forbidden.ID}); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("native decision accepted a non-owner actor", err)
		}
	}
	if _, err := journal.ChangeHostedTaskSourceIntent(ctx, f.account.ID, id, "decide", in, state.HostedTaskSourceOwner{Session: true, KeyID: owner.ID}); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("native decision accepted ambiguous session authority", err)
	}
	spoof := map[string]any{"intent_sha256": in.IntentSHA256, "review_sha256": in.ReviewSHA256, "decision": "approve", "session": true}
	if w := call("PUT", id, "/decision", spoof, &owner); w.Code != 400 {
		t.Fatal("decision accepted client-controlled session authority", w.Code)
	}
	current := f.executor
	f.executor = toolregistry.NewPrivateExecutor(s, toolregistry.PrivateExecutionOptions{})
	if w := call("PUT", id, "/decision", in, &owner); w.Code != 503 {
		t.Fatal("disabled source retention allowed approval", w.Code)
	}
	f.executor.Close()
	f.executor = current
	private, err := journal.GetHostedTaskSourceIntent(ctx, f.account.ID, id)
	if err != nil {
		t.Fatal(err)
	}
	approved := decode(call("PUT", id, "/decision", in, &owner), "approved")
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			w := call("PUT", id, "/decision", in, &owner)
			var got api.HostedTaskSourceIntent
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || got.DecidedAt == nil || !got.DecidedAt.Equal(*approved.DecidedAt) || !got.ExpiresAt.Equal(pending.ExpiresAt) {
				t.Error("concurrent decision replay renewed approval", w.Code)
			}
		})
	}
	wg.Wait()
	after, err := journal.GetHostedTaskSourceIntent(ctx, f.account.ID, id)
	if err != nil || !bytes.Equal(private.Ciphertext, after.Ciphertext) || string(private.Record.Review) != string(after.Record.Review) {
		t.Fatal("owner decision changed retained source", err)
	}
	decode(call("POST", id, "/check", api.HostedTaskSourceCheck{ReviewSHA256: pending.ReviewSHA256}, &f.key), "approved")
	app, err := s.AppByID(ctx, f.review.Apps[0].Review.AppID)
	if err != nil {
		t.Fatal(err)
	}
	oldRAM, changedRAM := app.RAMMB, app.RAMMB+128
	if _, err := s.UpdateApp(ctx, app.ID, state.UpdateAppParams{RAMMB: &changedRAM}); err != nil {
		t.Fatal(err)
	}
	decode(call("POST", id, "/check", api.HostedTaskSourceCheck{ReviewSHA256: pending.ReviewSHA256}, &owner), "stale")
	if _, err := s.UpdateApp(ctx, app.ID, state.UpdateAppParams{RAMMB: &oldRAM}); err != nil {
		t.Fatal(err)
	}
	decode(call("PUT", id, "/decision", in, &owner), "stale")
	// Restoring one setting can still advance native policy revisions. A new
	// review must capture all current pins; the first review stays stale.
	for i := range f.review.Apps {
		current, err := s.(state.ReleaseReviewStore).ReadReleaseReview(ctx, f.review.Apps[i].Review.AppID, api.DefaultEnvScope)
		if err != nil {
			t.Fatal(err)
		}
		f.review.Apps[i].Review = current
	}
	f.review.Fingerprint = tasktoolsource.Fingerprint(f.review)
	f.request.Review, _ = json.Marshal(f.review)
	id2 := toolpublication.Digest([]byte(f.account.ID + ":cancel"))
	second := decode(call("PUT", id2, "", f.request, &f.key), "pending")
	in2 := api.HostedTaskSourceDecision{IntentSHA256: second.IntentSHA256, ReviewSHA256: second.ReviewSHA256, Decision: "approve"}
	decode(call("PUT", id2, "/decision", in2, &owner), "approved")
	if _, err := s.MarkAPIKeyRevoked(ctx, f.account.ID, f.key.ID); err != nil {
		t.Fatal(err)
	}
	f.executor.Close()
	f.executor = toolregistry.NewPrivateExecutor(s, toolregistry.PrivateExecutionOptions{})
	t.Cleanup(f.executor.Close)
	in2.Decision = "revoke"
	decode(call("PUT", id2, "/decision", in2, &owner), "revoked")
	if w := call("GET", id2, "", nil, &f.key); w.Code == 200 {
		t.Fatal("cached revoked requester retained inspection authority")
	}
	if _, err := s.MarkAPIKeyRevoked(ctx, f.account.ID, owner.ID); err != nil {
		t.Fatal(err)
	}
	if w := call("PUT", id2, "/decision", in2, &owner); w.Code == 200 {
		t.Fatal("cached revoked owner retained decision authority")
	}
	f.executor.Close()
	f.executor = toolregistry.NewPrivateExecutor(reopen(), toolregistry.PrivateExecutionOptions{SourceRetentionEnabled: true})
	t.Cleanup(f.executor.Close)
	decode(call("GET", id, "", nil, nil), "stale")
	decode(call("GET", id2, "", nil, nil), "revoked")
}

// A lock wait must not preserve transaction-start authority past key expiry.
func TestHostedTaskSourceOwnerExpiryAfterLockWait(t *testing.T) {
	for _, target := range []string{"account", "owner"} {
		t.Run(target, func(t *testing.T) { exerciseHostedSourceOwnerExpiry(t, target) })
	}
}
func exerciseHostedSourceOwnerExpiry(t *testing.T, target string) {
	t.Helper()
	p := pgtest.OpenMigrated(t)
	s := state.NewPgStore(p)
	f := newHostedSourceFixture(t, s)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	id := toolpublication.Digest([]byte(f.account.ID + ":expiry"))
	if w := f.call(t, "PUT", id, f.request, &f.key); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	row, err := s.GetHostedTaskSourceIntent(ctx, f.account.ID, id)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte(f.account.ID + ":expiring-owner"))
	owner, err := s.CreateAPIKey(ctx, f.account.ID, hash[:], "expiring-source-owner", []string{api.ScopeAdmin})
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(2 * time.Second)
	if _, err := p.Exec(ctx, "UPDATE api_keys SET expires_at=$2 WHERE id=$1::uuid", owner.ID, expires); err != nil {
		t.Fatal(err)
	}
	blocker, err := p.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback(ctx) }()
	lockSQL, lockID, queryName := "SELECT id FROM accounts WHERE id=$1::uuid FOR UPDATE", f.account.ID, "LockToolPublicationAccount"
	if target == "owner" {
		lockSQL, lockID, queryName = "SELECT id FROM api_keys WHERE id=$1::uuid FOR UPDATE", owner.ID, "LockHostedTaskSourceOwnerKey"
	}
	if _, err := blocker.Exec(ctx, lockSQL, lockID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := s.ChangeHostedTaskSourceIntent(ctx, f.account.ID, id, "decide", api.HostedTaskSourceDecision{IntentSHA256: row.Record.IntentSHA256, ReviewSHA256: row.Record.ReviewSHA256, Decision: "approve"}, state.HostedTaskSourceOwner{KeyID: owner.ID})
		done <- err
	}()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		var waiting bool
		if err := p.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%'||$2||'%' AND xact_start<$1)", expires, queryName).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-tick.C:
		}
	}
	timer := time.NewTimer(time.Until(expires) + 20*time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	case <-timer.C:
	}
	if err := blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("expired owner approved after lock wait: %v", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	retained, err := s.GetHostedTaskSourceIntent(ctx, f.account.ID, id)
	if err != nil || retained.Record.ReviewStatus != "pending" || retained.Record.DecidedAt != nil {
		t.Fatal("failed owner authority changed review", err)
	}
}
