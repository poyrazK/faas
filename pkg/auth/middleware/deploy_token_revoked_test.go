package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Prod hunt r26: a revoked deploy token in CI was told to "create a new one
// with `gregale key create`", an account-wide key. The deploy-token refusal
// keeps the API key codes and names the deploy-token commands.
func TestRevokedOrExpiredDeployTokenNamesDeployTokenCommands(t *testing.T) {
	plain, _, err := api.GenerateDeployToken()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		err  error
		code string
	}{
		{state.ErrAPIKeyRevoked, api.CodeAPIKeyRevoked},
		{state.ErrAPIKeyExpired, api.CodeAPIKeyExpired},
	} {
		t.Run(tc.code, func(t *testing.T) {
			mw := newMWWith(t, deployTokenAuthn{fakeAuthn: newFakeAuthn(), err: tc.err},
				&fakeSessions{err: state.ErrNotFound}, &fakeLookups{getErr: state.ErrNotFound})
			w := httptest.NewRecorder()
			req := mkRequest("GET", "/v1/apps/demo", map[string]string{"Authorization": "Bearer " + plain}, nil)
			mw.RequireLimited(func(http.ResponseWriter, *http.Request, state.Account) {
				t.Fatal("handler ran for a refused deploy token")
			})(w, req.Clone(context.Background()))
			body := w.Body.String()
			if w.Code != http.StatusUnauthorized || !strings.Contains(body, tc.code) ||
				!strings.Contains(body, "gregale app deploy-token create") || strings.Contains(body, "gregale key create") {
				t.Fatalf("status %d body %s", w.Code, body)
			}
		})
	}
}
