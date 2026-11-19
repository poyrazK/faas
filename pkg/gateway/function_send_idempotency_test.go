package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// A retried send() with the same Idempotency-Key answers the invocation it
// already accepted, whatever per-call transport metadata the retry carries.
func TestFunctionSendIdempotentRetryReturnsTheSameInvocation(t *testing.T) {
	testFunctionSendIdempotentRetry(t, func(t *testing.T) functionSendFixture { return newFunctionSendFixture(t, api.PlanPro) })
}

// The retry compares against the stored row, so Postgres's round trip of the
// payload and headers must not read as another request.
func TestFunctionSendIdempotentRetryReturnsTheSameInvocationPG(t *testing.T) {
	testFunctionSendIdempotentRetry(t, func(t *testing.T) functionSendFixture {
		pool := pgtest.OpenMigrated(t)
		if err := db.MigrateUp(t.Context(), pool); err != nil {
			t.Fatal(err)
		}
		return newFunctionSendFixtureWith(t, state.NewPgStore(pool), api.PlanPro)
	})
}

func testFunctionSendIdempotentRetry(t *testing.T, fixture func(*testing.T) functionSendFixture) {
	for _, body := range []string{"", `{"email":"a@b"}`} {
		t.Run("body="+body, func(t *testing.T) {
			f := fixture(t)
			deployments := f.deploy(t, "production")
			send := func(requestID string) *httptest.ResponseRecorder {
				t.Helper()
				req := httptest.NewRequest(http.MethodPost, "http://shop-users.internal/", strings.NewReader(body))
				if body != "" {
					req.Header.Set("Content-Type", "application/json")
				}
				req.Header.Set(api.FunctionCallHeader, "1")
				req.Header.Set(api.FunctionInvokeHeader, api.FunctionInvokeAsync)
				req.Header.Set("Idempotency-Key", "send-retry-1")
				req.Header.Set(api.RequestIDHeader, requestID)
				req.Header.Set("Traceparent", "00-"+strings.Repeat(requestID[:1], 32)+"-"+strings.Repeat("b", 16)+"-01")
				w := httptest.NewRecorder()
				if !NewFunctionSendAdmitter(f.store)(w, req,
					ServiceCaller{AppID: f.caller.ID, AccountID: f.account.ID, DeploymentID: deployments[f.caller.ID].ID},
					ServiceTarget{AppID: f.callee.ID}, "/users") {
					t.Fatal("send not admitted")
				}
				return w
			}
			first, again := send("1first"), send("2again")
			var a, b api.AsyncInvokeResponse
			if first.Code != http.StatusAccepted || json.Unmarshal(first.Body.Bytes(), &a) != nil {
				t.Fatalf("first send = %d %s", first.Code, first.Body)
			}
			if again.Code != http.StatusAccepted || json.Unmarshal(again.Body.Bytes(), &b) != nil || b.ID != a.ID {
				t.Fatalf("retried send = %d %s, want the accepted invocation %s", again.Code, again.Body, a.ID)
			}
		})
	}
}
