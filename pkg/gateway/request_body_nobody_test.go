package gateway

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// A bodiless request keeps http.NoBody through the plan cap, so body
// admission and the forwarder take their no-body fast paths.
func TestPlanRequestBodyLimitKeepsNoBody(t *testing.T) {
	app := App{Plan: api.PlanPro}
	bodiless := httptest.NewRequest(http.MethodGet, "/", nil)
	bodiless.Body = http.NoBody
	if applyPlanRequestBodyLimit(httptest.NewRecorder(), bodiless, app) {
		t.Fatal("a bodiless request was refused")
	}
	if bodiless.Body != http.NoBody {
		t.Fatalf("plan cap wrapped http.NoBody in %T", bodiless.Body)
	}
	if admitRequestBodyBudgeted(httptest.NewRecorder(), bodiless, 1<<20, time.Second, nil, "owner") || bodiless.Body != http.NoBody {
		t.Fatalf("admission replaced http.NoBody with %T", bodiless.Body)
	}

	upload := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("payload"))
	if applyPlanRequestBodyLimit(httptest.NewRecorder(), upload, app) {
		t.Fatal("an in-limit upload was refused")
	}
	if got := fmt.Sprintf("%T", upload.Body); !strings.Contains(got, "maxBytesReader") {
		t.Fatalf("an upload is not capped: body is %s", got)
	}
}

// An empty chunked upload is admitted as no body, replayable as no body.
func TestAdmittedEmptyBodyIsNoBody(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(""))
	r.ContentLength, r.TransferEncoding = -1, []string{"chunked"}
	if admitRequestBodyBudgeted(httptest.NewRecorder(), r, 1<<20, time.Second, nil, "owner") {
		t.Fatal("an empty upload was refused")
	}
	if r.Body != http.NoBody || r.ContentLength != 0 || r.TransferEncoding != nil {
		t.Fatalf("empty upload admitted as %T, length %d, encoding %v", r.Body, r.ContentLength, r.TransferEncoding)
	}
	replay, err := r.GetBody()
	if err != nil || replay != http.NoBody {
		t.Fatalf("replay of an empty upload = %T, %v", replay, err)
	}

	r = httptest.NewRequest(http.MethodPost, "/", strings.NewReader("payload"))
	if admitRequestBodyBudgeted(httptest.NewRecorder(), r, 1<<20, time.Second, nil, "owner") {
		t.Fatal("an in-limit upload was refused")
	}
	if body, err := io.ReadAll(r.Body); err != nil || string(body) != "payload" || r.ContentLength != 7 {
		t.Fatalf("admitted upload = %q (length %d), %v", body, r.ContentLength, err)
	}
}
