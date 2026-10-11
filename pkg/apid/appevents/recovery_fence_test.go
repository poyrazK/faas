package appevents

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestEventRecoveryFenceHTTPProblems(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{{state.ErrEventRecoveryChanged, 409, api.CodeEventRecoveryChanged}, {state.ErrInvalidArgument, http.StatusBadRequest, api.CodeValidation}, {nil, 0, ""}} {
		w := httptest.NewRecorder()
		handled := writeEventRoutingReplayError(w, httptest.NewRequest(http.MethodPost, "/", nil), tc.err)
		if tc.err == nil {
			if handled {
				t.Fatal("handled success")
			}
			continue
		}
		if !handled || w.Code != tc.status || !strings.Contains(w.Body.String(), tc.code) {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	}
}
