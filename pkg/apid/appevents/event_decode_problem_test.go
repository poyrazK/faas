package appevents

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
)

// A valid event over the JSON body cap is a 413 naming the cap, not
// "invalid JSON body" (H10-69).
func TestEventDecodeProblemNamesTheBodyCap(t *testing.T) {
	big := `{"source":"h10.test","type":"probe.big","data":{"blob":"` + strings.Repeat("x", 2<<20) + `"}}`
	for _, tc := range []struct {
		name       string
		body       string
		wantStatus int
		want       string
	}{
		{name: "oversized", body: big, wantStatus: http.StatusRequestEntityTooLarge, want: "1048576-byte cap"},
		{name: "malformed", body: `{not json`, wantStatus: http.StatusBadRequest, want: "invalid JSON body"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/v1/events:publish", strings.NewReader(tc.body))
			var req api.PublishEventRequest
			err := httpkit.DecodeJSON(r, &req)
			if err == nil {
				t.Fatal("DecodeJSON accepted the body")
			}
			problem := eventDecodeProblem(r, err)
			if problem.Status != tc.wantStatus || !strings.Contains(problem.Detail, tc.want) {
				t.Fatalf("problem = %d %q, want %d containing %q", problem.Status, problem.Detail, tc.wantStatus, tc.want)
			}
		})
	}
}
