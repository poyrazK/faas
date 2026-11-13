package main_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

func TestHTTPContentTypeParameters(t *testing.T) {
	cases := []struct {
		name, expected, actual string
		pass                   bool
	}{
		{"matching parameter", "application/json; profile=v2", "application/json; profile=v2", true},
		{"mismatched parameter", "application/json; profile=v2", "application/json; profile=v1", false},
		{"missing parameter", "application/json; profile=v2", "application/json", false},
		{"extra parameter", "application/json", "application/json; charset=utf-8", true},
		{"quoted parameter", `application/json; profile="a;b"`, `application/json; profile="a;b"; charset=utf-8`, true},
		{"case insensitive charset", "application/json; charset=utf-8", "application/json; charset=UTF-8", true},
		{"different charset", "application/json; charset=utf-8", "application/json; charset=ascii", false},
		{"different type", "application/json", "text/plain", false},
		{"malformed type", "application/json", "application/json; charset", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", tc.actual)
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()
			step := main.TestHTTPRequest{Name: "probe", Method: "GET", Path: "/", Expect: main.TestHTTPExpect{Status: 200, ContentType: tc.expected}}
			evidence, err := main.RunTestHTTPRequests(t.Context(), server.URL, "run", nil, []main.TestHTTPRequest{step}, nil)
			if (err == nil) != tc.pass || len(evidence) != 1 || evidence[0].Passed != tc.pass {
				t.Fatalf("expect %q, actual %q: evidence=%+v error=%v", tc.expected, tc.actual, evidence, err)
			}
		})
	}
}

func TestHTTPHeaderValuesValidatedWithoutDisclosure(t *testing.T) {
	for _, tc := range []struct {
		name, value string
		valid       bool
	}{
		{"newline", "secret\nvalue", false},
		{"carriage return", "secret\rvalue", false},
		{"nul", "secret\x00value", false},
		{"delete", "secret\x7fvalue", false},
		{"tab", "safe\tvalue", true},
		{"utf8", "café", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			step := main.TestHTTPRequest{Name: "probe", Method: "GET", Path: "/", Headers: map[string]string{"X-Probe": tc.value}, Expect: main.TestHTTPExpect{Status: 200}}
			err := main.ValidateTestHTTPRequests(main.TestScenario{Requests: []main.TestHTTPRequest{step}})
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
			if err != nil && strings.Contains(err.Error(), "secret") {
				t.Fatal("header value disclosed", err)
			}
		})
	}
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits.Add(1); w.WriteHeader(200) }))
	defer server.Close()
	for _, tc := range []struct {
		name     string
		step     main.TestHTTPRequest
		captures map[string]string
		env      []string
	}{
		{"capture", main.TestHTTPRequest{Name: "probe", Method: "GET", Path: "/", Headers: map[string]string{"X-Probe": "${steps.first.token}"}, Expect: main.TestHTTPExpect{Status: 200}}, map[string]string{"steps.first.token": "secret\nvalue"}, nil},
		{"consumer key", main.TestHTTPRequest{Name: "probe", Method: "GET", Path: "/", As: "buyer", Expect: main.TestHTTPExpect{Status: 200}}, nil, []string{"GREGALE_TEST_CONSUMER_BUYER_KEY=secret\nvalue"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			evidence, err := main.RunTestHTTPRequests(t.Context(), server.URL, "run", tc.env, []main.TestHTTPRequest{tc.step}, tc.captures)
			if err == nil || len(evidence) != 1 || evidence[0].Passed || strings.Contains(err.Error(), "secret") || strings.Contains(evidence[0].Error, "secret") {
				t.Fatalf("invalid value accepted or disclosed: %+v %v", evidence, err)
			}
		})
	}
	if hits.Load() != 0 {
		t.Fatalf("sent %d invalid requests", hits.Load())
	}
}

func TestHTTPHeadResponseContracts(t *testing.T) {
	for _, tc := range []struct {
		name    string
		expect  main.TestHTTPExpect
		capture map[string]string
		valid   bool
	}{
		{"status and type", main.TestHTTPExpect{Status: 200, ContentType: "application/json"}, nil, true},
		{"json assertion", main.TestHTTPExpect{Status: 200, JSON: map[string]any{"/ok": true}}, nil, false},
		{"capture", main.TestHTTPExpect{Status: 200}, map[string]string{"token": "/token"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			step := main.TestHTTPRequest{Name: "probe", Method: "HEAD", Path: "/", Expect: tc.expect, Capture: tc.capture}
			err := main.ValidateTestHTTPRequests(main.TestScenario{Requests: []main.TestHTTPRequest{step}})
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
			if !tc.valid {
				var hits atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits.Add(1); w.WriteHeader(200) }))
				defer server.Close()
				_, err = main.RunTestHTTPRequests(t.Context(), server.URL, "run", nil, []main.TestHTTPRequest{step}, nil)
				if err == nil || hits.Load() != 0 {
					t.Fatalf("invalid HEAD was sent: hits=%d error=%v", hits.Load(), err)
				}
			}
		})
	}
}

func TestHTTPHeadPhaseDeadlineContract(t *testing.T) {
	scenario := main.FaultMatrixTestScenario()
	scenario.Resilience = nil
	scenario.Steps[1].Requests[0].Method = "HEAD"
	phase := &scenario.Steps[1].Load.ChaosSchedule[1]
	phase.RequestDeadlines = map[string]main.TestRequestDeadlineSpec{"load-probe": {Within: "1s", MinSamples: 1, Expect: &main.TestHTTPExpect{Status: 200, JSON: map[string]any{"/ok": true}}}}
	err := main.ValidateTestScenarioSteps(scenario, nil)
	if err == nil || !strings.Contains(err.Error(), "HEAD") {
		t.Fatalf("phase HEAD JSON assertion accepted: %v", err)
	}
	phase.RequestDeadlines = map[string]main.TestRequestDeadlineSpec{"load-probe": {Within: "1s", MinSamples: 1, Expect: &main.TestHTTPExpect{Status: 200}}}
	if err := main.ValidateTestScenarioSteps(scenario, nil); err != nil {
		t.Fatalf("valid HEAD deadline rejected: %v", err)
	}
}
