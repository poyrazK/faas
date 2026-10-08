// adr: 570 — preserve structured compute-node admission refusals.
package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/grpcerr"
)

func TestNodeAdmissionRefusalDoesNotReplayApplicationOrEvictPlacement(t *testing.T) {
	for _, code := range []string{api.CodeConcurrencyThrottled, api.CodeHTTPAdmissionUnavailable} {
		t.Run(code, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "http://example.test/work", nil)
			w := httptest.NewRecorder()
			attempts := 0
			policy := testPolicy()
			err := grpcerr.ToStatus(api.NewProblem(api.StatusForCode(code), code, "Refused", "Before guest execution"))
			runWithRetry(w, r, Target{InstanceID: "live"}, policy,
				func(Target) { t.Fatal("capacity refusal evicted placement") },
				func(dst http.ResponseWriter, _ *http.Request, _ Target) {
					attempts++
					if !writeNodeAdmissionRefusal(dst, err) {
						t.Fatal("unrecognized node refusal")
					}
				},
				func() (Target, bool) { t.Fatal("capacity refusal repicked"); return Target{}, false }, nil)
			if attempts != 1 || w.Code != api.StatusForCode(code) || !strings.Contains(w.Body.String(), code) {
				t.Fatalf("attempts=%d, response=%d %s", attempts, w.Code, w.Body.String())
			}
		})
	}
}

func TestNodeAdmissionResponseRetainsStructuredCap(t *testing.T) {
	w := httptest.NewRecorder()
	err := grpcerr.ToStatus(api.NewProblem(http.StatusTooManyRequests, api.CodeConcurrencyThrottled, "Busy", "full").WithLimit(4, 5))
	if !writeNodeAdmissionRefusal(w, err) {
		t.Fatal("unrecognized refusal")
	}
	resp := w.Result()
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"limit":4`) || !strings.Contains(string(body), `"observed":5`) || resp.Header.Get("Retry-After") != "1" {
		t.Fatalf("response %s", body)
	}
}
