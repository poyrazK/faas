// adr: 1524
package apidserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

type recoveryHTTPNotifier struct {
	apidserver.NoopNotifier
	calls atomic.Int32
}

func (n *recoveryHTTPNotifier) Notify(context.Context, string, string) error {
	n.calls.Add(1)
	return errors.New("fixture lost notification")
}

type lostRecoveryReply struct{ header http.Header }

func (r *lostRecoveryReply) Header() http.Header     { return r.header }
func (*lostRecoveryReply) WriteHeader(int)           {}
func (*lostRecoveryReply) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func recoveryHTTPSeed(t *testing.T, store *state.MemStore) (state.App, state.Deployment, state.Instance) {
	t.Helper()
	owner, err := store.CreateAccount(t.Context(), uuid.NewString()+"@recovery-tenant.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: owner.ID, Slug: "recovery-app", RAMMB: 512, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := store.CreateInstance(t.Context(), app.ID, dep.ID, string(state.StateRunning), 512, "", uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	return app, dep, instance
}

func recoveryHTTPRequest(path, key string, cookie *http.Cookie, body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	if cookie != nil {
		r.AddCookie(cookie)
	}
	r.Header.Set("Idempotency-Key", key)
	r.Header.Set("X-Trace-Id", "4bf92f3577b34da6a3ce929d0e0e4736")
	return r
}

func TestOperatorRecoverySubmissionLostReplyAndAPIReplacement(t *testing.T) {
	for _, action := range []string{"force-park", "force-restart", "force-cold-boot"} {
		t.Run(action, func(t *testing.T) {
			_, store, _ := apidserver.NewForceHarness(t, nil)
			app, dep, instance := recoveryHTTPSeed(t, store)
			actor, err := store.AccountByEmail(t.Context(), "ops@example.com")
			if err != nil {
				t.Fatal(err)
			}
			notifier := &recoveryHTTPNotifier{}
			newAPI := func() (*apidserver.Server, *http.Cookie) {
				srv := apidserver.NewServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", notifier).WithAdminAllowlist(actor.Email)
				sid := uuid.NewString()
				if _, err := store.CreateSession(t.Context(), sid, actor.ID, "192.0.2.10", "recovery-submission-test"); err != nil {
					t.Fatal(err)
				}
				token, err := (*srv.Sessions()).IssueWithSessionAndBindingHashAndStepUp(sid, actor.ID, "", time.Now(), false)
				if err != nil {
					t.Fatal(err)
				}
				return srv, &http.Cookie{Name: apidserver.SessionCookie, Value: token}
			}
			srv, cookie := newAPI()
			path, target := "/v1/admin/instances/"+instance.ID+"/"+action, instance.ID
			if action == "force-cold-boot" {
				path, target = "/v1/admin/apps/"+app.Slug+"/"+action, app.Slug
			}
			path += "?confirm=true&reason=incident_42"
			key := "lost-reply-key"
			srv.Handler().ServeHTTP(&lostRecoveryReply{header: make(http.Header)}, recoveryHTTPRequest(path, key, cookie, ""))
			receipt, err := store.SubmitOperatorRecovery(t.Context(), state.OperatorRecoverySubmission{
				ActorID: actor.ID, RequestKey: key, Kind: state.OperatorIntentKind(strings.ReplaceAll(action, "-", "_")), Target: target, Reason: "incident_42",
			})
			if err != nil || !receipt.Replayed {
				t.Fatalf("lost reply was not durable: %+v %v", receipt, err)
			}
			if action == "force-cold-boot" {
				if _, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage}); err != nil {
					t.Fatal(err)
				}
				if receipt.TargetID != dep.ID {
					t.Fatal("cold-boot target changed")
				}
			} else if err := store.UpdateInstanceState(t.Context(), instance.ID, string(state.StateStopped)); err != nil {
				t.Fatal(err)
			}
			replacement, freshCookie := newAPI()
			r := recoveryHTTPRequest(path, key, freshCookie, "")
			r.Header.Set("X-Trace-Id", "11111111111111111111111111111111")
			rec := httptest.NewRecorder()
			replacement.Handler().ServeHTTP(rec, r)
			if rec.Code != http.StatusAccepted || !bytes.Equal(rec.Body.Bytes(), receipt.ResponseBody) {
				t.Fatalf("replacement replay=%d %s", rec.Code, rec.Body.String())
			}
			if notifier.calls.Load() != 1 {
				t.Fatalf("replay notified scheduler again: %d", notifier.calls.Load())
			}
			intent, err := store.ClaimPendingOperatorIntent(t.Context())
			if err != nil || intent.ID != receipt.IntentID || intent.TraceID == nil || *intent.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" {
				t.Fatalf("intent attribution changed: %+v %v", intent, err)
			}
			if _, err := store.ClaimPendingOperatorIntent(t.Context()); !errors.Is(err, state.ErrOperatorIntentNotFound) {
				t.Fatal("retry submitted another intent")
			}
		})
	}
}

func TestOperatorRecoverySubmissionRejectsChangedInputsAndReauthenticates(t *testing.T) {
	srv, store, cookie := apidserver.NewForceHarness(t, nil)
	_, _, instance := recoveryHTTPSeed(t, store)
	base := "/v1/admin/instances/" + instance.ID + "/force-restart"
	h := srv.Handler()
	first := httptest.NewRecorder()
	h.ServeHTTP(first, recoveryHTTPRequest(base+"?confirm=true&reason=incident_42", "bound-key", cookie, ""))
	if first.Code != http.StatusAccepted {
		t.Fatalf("initial request: %d %s", first.Code, first.Body.String())
	}
	actor, err := store.AccountByEmail(t.Context(), "ops@example.com")
	if err != nil {
		t.Fatal(err)
	}
	sid := uuid.NewString()
	if _, err := store.CreateSession(t.Context(), sid, actor.ID, "192.0.2.10", "stale-step-up"); err != nil {
		t.Fatal(err)
	}
	staleToken, err := (*srv.Sessions()).IssueWithSessionAndBindingHashAndStepUp(sid, actor.ID, "", time.Now().Add(-10*time.Minute), false)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, path, body, key string
		status                int
		mutate                func(*http.Request)
	}{
		{"changed_reason", base + "?confirm=true&reason=other", "", "bound-key", 409, nil},
		{"changed_action", "/v1/admin/instances/" + instance.ID + "/force-park?confirm=true&reason=incident_42", "", "bound-key", 409, nil},
		{"changed_target", "/v1/admin/instances/" + uuid.NewString() + "/force-restart?confirm=true&reason=incident_42", "", "bound-key", 409, nil},
		{"no_confirm", base, "", "bound-key", 400, nil},
		{"confirm_false", base + "?confirm=false", "", "bound-key", 400, nil},
		{"duplicate_reason", base + "?confirm=true&reason=a&reason=b", "", "bound-key", 400, nil},
		{"duplicate_confirm", base + "?confirm=true&confirm=false", "", "bound-key", 400, nil},
		{"unknown_query", base + "?confirm=true&ignored=payload", "", "bound-key", 400, nil},
		{"malformed_query", base + "?confirm=true&reason=%ZZ", "", "bound-key", 400, nil},
		{"body", base + "?confirm=true", `{}`, "bound-key", 400, nil},
		{"missing_key", base + "?confirm=true", "", "", 400, nil},
		{"duplicate_key", base + "?confirm=true", "", "bound-key", 400, func(r *http.Request) { r.Header.Add("Idempotency-Key", "another-key") }},
		{"cross_origin_replay", base + "?confirm=true&reason=incident_42", "", "bound-key", 403, func(r *http.Request) {
			r.Header.Set("Origin", "https://attacker.example")
			r.Header.Set("Sec-Fetch-Site", "cross-site")
		}},
		{"anonymous_replay", base + "?confirm=true&reason=incident_42", "", "bound-key", 401, func(r *http.Request) { r.Header.Del("Cookie") }},
		{"stale_step_up_replay", base + "?confirm=true&reason=incident_42", "", "bound-key", 403, func(r *http.Request) {
			r.Header.Del("Cookie")
			r.AddCookie(&http.Cookie{Name: apidserver.SessionCookie, Value: staleToken})
		}},
		{"removed_operator_replay", base + "?confirm=true&reason=incident_42", "", "bound-key", 403, func(*http.Request) { delete(*(*srv.AdminAllowlist()).Emails(), actor.Email) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := recoveryHTTPRequest(tc.path, tc.key, cookie, tc.body)
			if tc.mutate != nil {
				tc.mutate(r)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, r)
			if rec.Code != tc.status {
				t.Fatalf("status=%d want=%d body=%s", rec.Code, tc.status, rec.Body.String())
			}
		})
	}
}

func TestOperatorRecoverySubmissionConcurrentHTTP(t *testing.T) {
	srv, store, cookie := apidserver.NewForceHarness(t, nil)
	_, _, instance := recoveryHTTPSeed(t, store)
	h := srv.Handler()
	path := "/v1/admin/instances/" + instance.ID + "/force-restart?confirm=true"
	var wg sync.WaitGroup
	results := make(chan string, 12)
	for range 12 {
		wg.Go(func() {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, recoveryHTTPRequest(path, "concurrent-key", cookie, ""))
			if rec.Code != http.StatusAccepted {
				t.Errorf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			results <- rec.Body.String()
		})
	}
	wg.Wait()
	close(results)
	first := <-results
	for body := range results {
		if body != first {
			t.Fatal("concurrent HTTP responses diverged")
		}
	}
}

type unavailableRecoverySubmissions struct{ MemStore *state.MemStore }

func (*unavailableRecoverySubmissions) SubmitOperatorRecovery(context.Context, state.OperatorRecoverySubmission) (state.OperatorRecoveryReceipt, error) {
	return state.OperatorRecoveryReceipt{}, errors.New("fixture database unavailable")
}

func TestOperatorRecoverySubmissionStorageFailureDoesNotFailOpen(t *testing.T) {
	srv, store, cookie := apidserver.NewForceHarness(t, nil)
	_, _, instance := recoveryHTTPSeed(t, store)
	*srv.Store() = statetest.NewMem(&unavailableRecoverySubmissions{MemStore: store})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, recoveryHTTPRequest("/v1/admin/instances/"+instance.ID+"/force-restart?confirm=true", "unavailable-key", cookie, ""))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := store.ClaimPendingOperatorIntent(t.Context()); !errors.Is(err, state.ErrOperatorIntentNotFound) {
		t.Fatal("failed storage fell back to an unreceipted intent")
	}
	var problem api.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil || !strings.Contains(problem.Detail, "same Idempotency-Key") {
		t.Fatalf("missing retry guidance: %s", rec.Body.String())
	}
}
