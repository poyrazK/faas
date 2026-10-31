package profilecapture_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/pprof/profile"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/profilecapture"
	"github.com/onebox-faas/faas/pkg/profiling"
	"github.com/onebox-faas/faas/pkg/state"
)

func heapBlob(t *testing.T, fn string, bytes int64) []byte {
	t.Helper()
	f := &profile.Function{ID: 1, Name: fn, Filename: "app.js"}
	loc := &profile.Location{ID: 1, Line: []profile.Line{{Function: f, Line: 3}}}
	raw, err := profiling.EncodeCapture(&profile.Profile{
		SampleType: []*profile.ValueType{{Type: "space", Unit: "bytes"}},
		PeriodType: &profile.ValueType{Type: "space", Unit: "bytes"}, Period: 1,
		Sample:   []*profile.Sample{{Location: []*profile.Location{loc}, Value: []int64{bytes}}},
		Location: []*profile.Location{loc}, Function: []*profile.Function{f},
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// readyCapture stores one finished capture through the schedd queue path.
func readyCapture(t *testing.T, host *fakeHost, acct state.Account, kinds []string, at time.Time, blobs ...state.ProfileCaptureBlob) api.ProfileCapture {
	t.Helper()
	c := api.ProfileCapture{ID: uuid.NewString(), AppID: host.app.ID, Status: api.ProfileCaptureQueued, Kinds: kinds,
		DurationSeconds: 10, Profiles: []api.ProfileCaptureProfile{}, CreatedAt: at, ExpiresAt: at.Add(api.ProfileCaptureRetention)}
	if err := host.mem.CreateProfileCapture(t.Context(), acct.ID, host.app.ID, c, api.MustLimitsFor(api.PlanPro).Profiling.CaptureStorageBytes); err != nil {
		t.Fatal(err)
	}
	claimed, err := host.mem.ClaimProfileCapture(t.Context(), at)
	if err != nil || claimed.ID != c.ID {
		t.Fatalf("claim = %+v, %v", claimed, err)
	}
	result := api.ProfileCapture{Status: api.ProfileCaptureReady, Processes: 1, Profiles: []api.ProfileCaptureProfile{}}
	if err := host.mem.FinishProfileCapture(t.Context(), c.ID, result, blobs, at.Add(12*time.Second)); err != nil {
		t.Fatal(err)
	}
	got, err := host.mem.GetProfileCapture(t.Context(), acct.ID, host.app.ID, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestCaptureDiff(t *testing.T) {
	svc, host, acct := newService(t, api.PlanPro, true)
	now := time.Now().UTC()
	base := readyCapture(t, host, acct, []string{api.ProfileKindHeap}, now.Add(-time.Hour),
		state.ProfileCaptureBlob{Kind: api.ProfileKindHeap, ProcessID: "7", Profile: heapBlob(t, "grow", 1000)})
	cand := readyCapture(t, host, acct, []string{api.ProfileKindCPU, api.ProfileKindHeap}, now,
		state.ProfileCaptureBlob{Kind: api.ProfileKindHeap, ProcessID: "7", Profile: heapBlob(t, "grow", 5000)})
	cpuOnly := readyCapture(t, host, acct, []string{api.ProfileKindCPU}, now.Add(-30*time.Minute))

	rec := serve(svc, acct, http.MethodGet, "/v1/apps/capture/profiles/captures/"+cand.ID+"/diff?base="+base.ID, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("diff = %d: %s", rec.Code, rec.Body)
	}
	var diff api.ProfileCaptureDiff
	if err := json.Unmarshal(rec.Body.Bytes(), &diff); err != nil {
		t.Fatal(err)
	}
	if diff.Kind != api.ProfileKindHeap || diff.BaselineTotal != 1000 || diff.CandidateTotal != 5000 || diff.Delta != 4000 {
		t.Fatalf("diff = %+v", diff)
	}
	if len(diff.Functions) != 1 || diff.Functions[0].Name != "grow" || diff.Functions[0].Delta != 4000 || diff.Baseline.ID != base.ID {
		t.Fatalf("functions = %+v", diff.Functions)
	}
	for _, tc := range []struct {
		name, target string
		want         int
	}{
		{"missing base", "/diff", http.StatusBadRequest},
		{"self as base", "/diff?base=" + cand.ID, http.StatusBadRequest},
		{"unknown base", "/diff?base=" + uuid.NewString(), http.StatusBadRequest},
		{"no shared kind", "/diff?base=" + cpuOnly.ID + "&kind=heap", http.StatusBadRequest},
		{"kind not in base", "/diff?base=" + base.ID + "&kind=cpu", http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := serve(svc, acct, http.MethodGet, "/v1/apps/capture/profiles/captures/"+cand.ID+tc.target, "")
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.want, rec.Body)
			}
		})
	}
}

func TestCaptureDiffRejectsUnreadyBase(t *testing.T) {
	svc, host, acct := newService(t, api.PlanPro, true)
	cand := readyCapture(t, host, acct, []string{api.ProfileKindHeap}, time.Now().UTC().Add(-time.Hour),
		state.ProfileCaptureBlob{Kind: api.ProfileKindHeap, ProcessID: "7", Profile: heapBlob(t, "grow", 10)})
	rec := serve(svc, acct, http.MethodPost, "/v1/apps/capture/profiles/captures", `{"kinds":["heap"],"duration_seconds":5}`)
	var queued api.ProfileCapture
	if err := json.Unmarshal(rec.Body.Bytes(), &queued); err != nil {
		t.Fatal(err)
	}
	rec = serve(svc, acct, http.MethodGet, "/v1/apps/capture/profiles/captures/"+cand.ID+"/diff?base="+queued.ID, "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
}

func TestComparableAndCommonKind(t *testing.T) {
	heap := api.ProfileCapture{ID: "a", Status: api.ProfileCaptureReady, Kinds: []string{api.ProfileKindHeap}}
	both := api.ProfileCapture{ID: "b", Status: api.ProfileCaptureReady, Kinds: []string{api.ProfileKindCPU, api.ProfileKindHeap}}
	cpu := api.ProfileCapture{ID: "c", Status: api.ProfileCaptureReady, Kinds: []string{api.ProfileKindCPU}}
	failed := api.ProfileCapture{ID: "d", Status: api.ProfileCaptureFailed, Kinds: []string{api.ProfileKindHeap}}
	if got := profilecapture.CommonKind(both, cpu); got != api.ProfileKindCPU {
		t.Fatalf("CommonKind(both, cpu) = %q", got)
	}
	if got := profilecapture.CommonKind(both, heap); got != api.ProfileKindHeap {
		t.Fatalf("CommonKind(both, heap) = %q", got)
	}
	got := profilecapture.Comparable(heap, []api.ProfileCapture{heap, both, cpu, failed})
	if len(got) != 1 || got[0].ID != "b" {
		t.Fatalf("Comparable = %+v", got)
	}
}
