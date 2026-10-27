package profilecapture_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/profilecapture"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/profiling"
	"github.com/onebox-faas/faas/pkg/state"
)

type fakeHost struct {
	mem      *state.MemStore
	app      state.App
	notified []string
}

func (h *fakeHost) LoadApp(w http.ResponseWriter, _ *http.Request, acct state.Account, slug string) (state.App, bool) {
	if slug != h.app.Slug || acct.ID != h.app.AccountID {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "App not found", "the app does not exist"))
		return state.App{}, false
	}
	return h.app, true
}

func (h *fakeHost) Store() state.Store                { return h.mem }
func (h *fakeHost) Logger() *slog.Logger              { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
func (h *fakeHost) Notifier() profilecapture.Notifier { return h }
func (h *fakeHost) SourceProvenance(state.App, state.Deployment) profiling.SourceProvenance {
	return profiling.SourceProvenance{}
}

func (h *fakeHost) Notify(_ context.Context, channel, payload string) error {
	h.notified = append(h.notified, channel+" "+payload)
	return nil
}

func newService(t *testing.T, plan api.Plan, enabled bool) (*profilecapture.Service, *fakeHost, state.Account) {
	t.Helper()
	mem := state.NewMemStore()
	acct, err := mem.CreateAccount(t.Context(), "capture@example.com", plan)
	if err != nil {
		t.Fatal(err)
	}
	app, err := mem.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "capture", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	host := &fakeHost{mem: mem, app: app}
	return &profilecapture.Service{Host: host, Enabled: enabled}, host, acct
}

func serve(svc *profilecapture.Service, acct state.Account, method, target, body string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	wrap := func(h func(http.ResponseWriter, *http.Request, state.Account)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) { h(w, r, acct) }
	}
	mux.HandleFunc("POST /v1/apps/{slug}/profiles/captures", wrap(svc.Create))
	mux.HandleFunc("GET /v1/apps/{slug}/profiles/captures", wrap(svc.List))
	mux.HandleFunc("GET /v1/apps/{slug}/profiles/captures/{id}", wrap(svc.Get))
	mux.HandleFunc("GET /v1/apps/{slug}/profiles/captures/{id}/view", wrap(svc.View))
	mux.HandleFunc("GET /v1/apps/{slug}/profiles/captures/{id}/pprof", wrap(svc.Download))
	mux.HandleFunc("GET /v1/apps/{slug}/profiles/captures/{id}/diff", wrap(svc.Diff))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(method, target, strings.NewReader(body)))
	return rec
}

func TestCaptureGates(t *testing.T) {
	for _, tc := range []struct {
		name    string
		plan    api.Plan
		enabled bool
		slug    string
		body    string
		want    int
	}{
		{"disabled installation", api.PlanPro, false, "capture", `{}`, http.StatusNotFound},
		{"plan without profiling", api.PlanFree, true, "capture", `{}`, http.StatusPaymentRequired},
		{"unknown app", api.PlanPro, true, "missing", `{}`, http.StatusNotFound},
		{"duration out of range", api.PlanPro, true, "capture", `{"duration_seconds":3600}`, http.StatusBadRequest},
		{"unknown kind", api.PlanPro, true, "capture", `{"kinds":["wall"]}`, http.StatusBadRequest},
		{"unknown field", api.PlanPro, true, "capture", `{"seconds":5}`, http.StatusBadRequest},
		{"accepted", api.PlanPro, true, "capture", `{"kinds":["cpu","heap"],"duration_seconds":5}`, http.StatusAccepted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, host, acct := newService(t, tc.plan, tc.enabled)
			rec := serve(svc, acct, http.MethodPost, "/v1/apps/"+tc.slug+"/profiles/captures", tc.body)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.want, rec.Body)
			}
			if wantNotify := tc.want == http.StatusAccepted; (len(host.notified) == 1) != wantNotify {
				t.Fatalf("notifications = %v, want notify %v", host.notified, wantNotify)
			}
		})
	}
}

// TestCaptureCreateProblems pins the problem each store rejection maps to.
// Both account quotas answer 429 quota_exhausted with their limit and
// observed value; a full capture storage also names the bytes (ADR-1155).
func TestCaptureCreateProblems(t *testing.T) {
	full := &state.ProfileCaptureStorageError{Limit: 256 << 20, Used: 254 << 20}
	for _, tc := range []struct {
		name            string
		err             error
		status          int
		code            string
		limit, observed int64
		bytes           bool
	}{
		{"app gone", state.ErrNotFound, http.StatusNotFound, api.CodeNotFound, 0, 0, false},
		{"capture active", state.ErrProfileCaptureActive, http.StatusConflict, api.CodeConflict, 0, 0, false},
		{"hourly quota", state.ErrProfileCaptureQuota, http.StatusTooManyRequests, api.CodeQuotaExhausted,
			api.ProfileCaptureMaxPerAccountHour, api.ProfileCaptureMaxPerAccountHour, false},
		{"storage full", fmt.Errorf("queue profile capture: %w", full), http.StatusTooManyRequests, api.CodeQuotaExhausted,
			full.Limit, full.Used, true},
		{"store failure", errors.New("connection reset"), http.StatusServiceUnavailable, api.CodeCapacity, 0, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := profilecapture.CreateProblem(tc.err)
			if p.Status != tc.status || p.Code != tc.code {
				t.Fatalf("problem = %d %s, want %d %s", p.Status, p.Code, tc.status, tc.code)
			}
			if got := [2]int64{deref(p.Limit), deref(p.Observed)}; got != [2]int64{tc.limit, tc.observed} {
				t.Fatalf("limit, observed = %v, want %d, %d", got, tc.limit, tc.observed)
			}
			if bytes := p.LimitBytes != nil && *p.LimitBytes == tc.limit && p.ObservedBytes != nil && *p.ObservedBytes == tc.observed; bytes != tc.bytes {
				t.Fatalf("limit_bytes, observed_bytes = %v, %v, want set %v", p.LimitBytes, p.ObservedBytes, tc.bytes)
			}
			if tc.bytes && p.DocsURL == "" {
				t.Fatal("storage problem has no docs URL")
			}
		})
	}
}

func deref(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}

func TestCaptureLifecycle(t *testing.T) {
	svc, host, acct := newService(t, api.PlanPro, true)
	rec := serve(svc, acct, http.MethodPost, "/v1/apps/capture/profiles/captures", `{"kinds":["heap"],"duration_seconds":5}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("create = %d: %s", rec.Code, rec.Body)
	}
	var created api.ProfileCapture
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Status != api.ProfileCaptureQueued || created.AppID != host.app.ID {
		t.Fatalf("created = %+v", created)
	}
	if want := db.NotifyProfileCapture + ` {"capture_id":"` + created.ID + `"}`; host.notified[0] != want {
		t.Fatalf("notify = %q, want %q", host.notified[0], want)
	}
	for _, tc := range []struct {
		name, method, target, body string
		want                       int
	}{
		{"second capture while queued", http.MethodPost, "/v1/apps/capture/profiles/captures", `{}`, http.StatusConflict},
		{"list", http.MethodGet, "/v1/apps/capture/profiles/captures", "", http.StatusOK},
		{"get", http.MethodGet, "/v1/apps/capture/profiles/captures/" + created.ID, "", http.StatusOK},
		{"malformed id", http.MethodGet, "/v1/apps/capture/profiles/captures/not-a-uuid", "", http.StatusNotFound},
		{"view before ready", http.MethodGet, "/v1/apps/capture/profiles/captures/" + created.ID + "/view", "", http.StatusConflict},
		{"kind not captured", http.MethodGet, "/v1/apps/capture/profiles/captures/" + created.ID + "/pprof?kind=cpu", "", http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := serve(svc, acct, tc.method, tc.target, tc.body)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.want, rec.Body)
			}
		})
	}
	var list api.ProfileCaptureList
	if err := json.Unmarshal(serve(svc, acct, http.MethodGet, "/v1/apps/capture/profiles/captures", "").Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Captures) != 1 || list.Captures[0].ID != created.ID {
		t.Fatalf("list = %+v", list)
	}
}
