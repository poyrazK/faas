package main_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
)

func TestHTTPBodylessStatusContracts(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path == "/not-modified" {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	for _, status := range []int{http.StatusNoContent, http.StatusNotModified} {
		for _, tc := range []struct {
			name       string
			expectJSON map[string]any
			capture    map[string]string
			valid      bool
		}{
			{"status only", nil, nil, true},
			{"JSON assertion", map[string]any{"/ok": true}, nil, false},
			{"capture", nil, map[string]string{"id": "/id"}, false},
		} {
			t.Run(fmt.Sprintf("%d/%s", status, tc.name), func(t *testing.T) {
				path := "/no-content"
				if status == http.StatusNotModified {
					path = "/not-modified"
				}
				step := main.TestHTTPRequest{Name: "probe", Method: "GET", Path: path, Expect: main.TestHTTPExpect{Status: status, JSON: tc.expectJSON}, Capture: tc.capture}
				err := main.ValidateTestHTTPRequests(main.TestScenario{Requests: []main.TestHTTPRequest{step}})
				if (err == nil) != tc.valid {
					t.Fatalf("validation error: %v", err)
				}
				before := hits.Load()
				_, err = main.RunTestHTTPRequests(t.Context(), server.URL, "run", nil, []main.TestHTTPRequest{step}, map[string]string{})
				if (err == nil) != tc.valid || (!tc.valid && hits.Load() != before) {
					t.Fatalf("runtime error %v, requests sent %d", err, hits.Load()-before)
				}
			})
		}
	}
}

func TestHTTPPathDotSegmentsStayData(t *testing.T) {
	type seenRequest struct{ path, value string }
	seen := make(chan seenRequest, 4)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /users/{id}/orders", func(w http.ResponseWriter, r *http.Request) {
		seen <- seenRequest{r.URL.EscapedPath(), r.PathValue("id")}
		w.WriteHeader(http.StatusOK)
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	for _, tc := range []struct{ name, path, input, escaped, value string }{
		{"single dot", "/users/${steps.first.id}/orders", ".", "/users/%2E/orders", "."},
		{"double dot", "/users/${steps.first.id}/orders", "..", "/users/%2E%2E/orders", ".."},
		{"composed dot", "/users/.${steps.first.id}/orders", ".", "/users/%2E%2E/orders", ".."},
		{"empty fragment", "/users/.${steps.first.id}./orders", "", "/users/%2E%2E/orders", ".."},
		{"normal identifier", "/users/${steps.first.id}/orders", "a.b", "/users/a.b/orders", "a.b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			step := main.TestHTTPRequest{Name: "probe", Method: "GET", Path: tc.path, Expect: main.TestHTTPExpect{Status: 200}}
			_, err := main.RunTestHTTPRequests(t.Context(), server.URL, "run", nil, []main.TestHTTPRequest{step}, map[string]string{"steps.first.id": tc.input})
			if err != nil {
				t.Fatal(err)
			}
			got := <-seen
			if got.path != tc.escaped || got.value != tc.value {
				t.Fatalf("got escaped path %q and value %q", got.path, got.value)
			}
		})
	}
}

func TestHTTPPhaseDeadlineReferenceScopes(t *testing.T) {
	newScenario := func() main.TestScenario {
		s := main.FaultMatrixTestScenario()
		s.Resilience = nil
		s.Steps[0].Requests[0].Capture = map[string]string{"token": "/token"}
		s.Steps[1].Load.ChaosSchedule[1].RequestDeadlines = map[string]main.TestRequestDeadlineSpec{"load-probe": {Within: "1s", MinSamples: 1, Expect: &main.TestHTTPExpect{Status: 200, JSON: map[string]any{"/token": "${steps.baseline-probe.token}"}}}}
		return s
	}
	for _, tc := range []struct {
		name, ref string
		fields    []string
		valid     bool
	}{
		{"earlier stage", "${steps.baseline-probe.token}", nil, true},
		{"unknown capture", "${steps.missing.token}", nil, false},
		{"own capture", "${steps.load-probe.token}", nil, false},
		{"known data", "${data.expected}", []string{"expected"}, true},
		{"unknown data", "${data.expected}", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newScenario()
			s.Steps[1].Requests[0].Capture = map[string]string{"other": "/other"}
			s.Steps[1].Load.ChaosSchedule[1].RequestDeadlines["load-probe"].Expect.JSON["/token"] = tc.ref
			err := main.ValidateTestScenarioSteps(s, tc.fields)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
		})
	}
	for _, tc := range []struct {
		name     string
		sequence []string
		valid    bool
	}{
		{"earlier in stream", []string{"create", "load-probe"}, true},
		{"later in stream", []string{"load-probe", "create"}, false},
		{"other stream", []string{"load-probe"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newScenario()
			create := main.TestHTTPRequest{Name: "create", Method: "GET", Path: "/create", Capture: map[string]string{"id": "/id"}, Expect: main.TestHTTPExpect{Status: 200}}
			other := main.TestHTTPRequest{Name: "other", Method: "GET", Path: "/other", Expect: main.TestHTTPExpect{Status: 200}}
			s.Steps[1].Requests = append(s.Steps[1].Requests, create, other)
			s.Steps[1].Load.ChaosSchedule[1].RequestDeadlines["load-probe"].Expect.JSON["/token"] = "${steps.create.id}"
			otherRequests := []string{"other"}
			if len(tc.sequence) == 1 {
				otherRequests = []string{"create", "other"}
			}
			s.Steps[1].Load.Streams = []main.TestLoadStreamSpec{{Name: "first", Requests: tc.sequence, Rate: 1, VUs: 1}, {Name: "second", Requests: otherRequests, Rate: 1, VUs: 1}}
			err := main.ValidateTestScenarioSteps(s, nil)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
		})
	}
	budget := main.TestRequestDeadlineSpec{Within: "1s", MinSamples: 1, Expect: &main.TestHTTPExpect{Status: 200, JSON: map[string]any{"/token": "${steps.baseline-probe.token}"}}}
	profiles := map[string]main.TestAssertionProfile{"deadline": {RequestDeadlines: map[string]main.TestRequestDeadlineSpec{"load-probe": budget}}}
	if err := main.ValidateTestAssertionProfiles(profiles); err != nil {
		t.Fatalf("reusable profile rejected: %v", err)
	}
	profileScenario := newScenario()
	profileScenario.Steps[1].Load.ChaosSchedule[1].RequestDeadlines = nil
	profileScenario.Steps[1].Load.ChaosSchedule[1].Assertions = []string{"deadline"}
	expanded, err := main.ExpandTestAssertionProfiles(profileScenario, profiles)
	if err != nil {
		t.Fatal(err)
	}
	if err := main.ValidateTestScenarioSteps(expanded, nil); err != nil {
		t.Fatalf("expanded profile rejected: %v", err)
	}
	dataScenario := newScenario()
	dataScenario.Steps[1].Load.ChaosSchedule[1].RequestDeadlines["load-probe"].Expect.JSON["/token"] = "${data.expected}"
	if fields := main.TestHTTPDataFields(dataScenario); len(fields) != 1 || fields[0] != "expected" {
		t.Fatalf("phase-only data field was not discovered: %v", fields)
	}
	for _, status := range []int{204, 304} {
		bodyless := newScenario()
		bodyless.Steps[1].Requests[0].Capture = map[string]string{"id": "/id"}
		deadline := bodyless.Steps[1].Load.ChaosSchedule[1].RequestDeadlines["load-probe"]
		deadline.Expect = &main.TestHTTPExpect{Status: status}
		bodyless.Steps[1].Load.ChaosSchedule[1].RequestDeadlines["load-probe"] = deadline
		if err := main.ValidateTestScenarioSteps(bodyless, nil); err == nil || !strings.Contains(err.Error(), fmt.Sprintf("status %d", status)) {
			t.Fatalf("status %d with a capture was accepted: %v", status, err)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"token":"captured"}`)) }))
	defer server.Close()
	cfg := &main.TestLoadConfig{ChaosSchedule: []main.TestLoadChaosPhase{{RequestDeadlines: map[string]main.TestRequestDeadlineSpec{"load-probe": budget}}}}
	original := main.TestHTTPRequest{Name: "load-probe", Method: "GET", Path: "/", Expect: main.TestHTTPExpect{Status: 200}}
	ctx, cancel, phaseStep := main.PrepareTestRequestDeadline(t.Context(), cfg, 0, original, time.Now())
	defer cancel()
	_, err = main.RunTestHTTPRequests(ctx, server.URL, "run", nil, []main.TestHTTPRequest{phaseStep}, map[string]string{"steps.baseline-probe.token": "captured"})
	if err != nil || len(original.Expect.JSON) != 0 {
		t.Fatalf("phase contract runtime: %v", err)
	}
	_, err = main.RunTestHTTPRequests(ctx, server.URL, "run", nil, []main.TestHTTPRequest{phaseStep}, map[string]string{"steps.baseline-probe.token": "wrong"})
	if err == nil || !strings.Contains(err.Error(), "did not match") {
		t.Fatalf("phase contract did not enforce captured value: %v", err)
	}
}
