package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type problemHTMLResponseRecorder struct {
	*httptest.ResponseRecorder
	request *http.Request
}

func (r *problemHTMLResponseRecorder) ProblemHTMLRequest() *http.Request {
	return r.request
}

func TestWriteProblem_BrowserHTMLByStatus(t *testing.T) {
	for _, status := range []int{
		http.StatusNotFound,
		http.StatusServiceUnavailable,
		http.StatusPaymentRequired,
		http.StatusTooManyRequests,
	} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			recorder := &problemHTMLResponseRecorder{
				ResponseRecorder: httptest.NewRecorder(),
				request:          httptest.NewRequest(http.MethodGet, "https://api.example.test", nil),
			}
			recorder.request.Header.Set("Accept", "text/html,application/xhtml+xml")
			WriteProblem(recorder, NewProblem(status, "platform_failure", "Failure", "secret.example.test/customer-app"))

			if got := recorder.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
				t.Fatalf("Content-Type = %q, want text/html", got)
			}
			if recorder.Code != status {
				t.Fatalf("status = %d, want %d", recorder.Code, status)
			}
			body := recorder.Body.String()
			if !strings.Contains(body, `<meta name="faas-error-code" content="platform_failure">`) {
				t.Error("HTML is missing the machine-readable error meta tag")
			}
			if !strings.Contains(body, `data-faas-error-code="platform_failure"`) {
				t.Error("HTML is missing the machine-readable data attribute")
			}
			if strings.Contains(body, "secret.example.test") || strings.Contains(body, "customer-app") {
				t.Error("HTML must not expose problem detail or customer data")
			}
		})
	}
}

func TestWriteProblem_BrowserAcceptNegotiation(t *testing.T) {
	for _, tc := range []struct {
		name     string
		accept   string
		wantHTML bool
		wantVary bool
	}{
		{name: "explicit html", accept: "text/html", wantHTML: true, wantVary: true},
		{name: "weighted html", accept: "application/json, text/html;q=0.8", wantHTML: true, wantVary: true},
		{name: "zero quality", accept: "text/html;q=0", wantHTML: false},
		{name: "wildcard only", accept: "*/*", wantHTML: false},
		{name: "json", accept: "application/json", wantHTML: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := &problemHTMLResponseRecorder{
				ResponseRecorder: httptest.NewRecorder(),
				request:          httptest.NewRequest(http.MethodGet, "https://api.example.test", nil),
			}
			recorder.request.Header.Set("Accept", tc.accept)
			WriteProblem(recorder, NewProblem(http.StatusNotFound, "not_found", "Not found", "hidden detail"))

			body := recorder.Body.String()
			if got := strings.HasPrefix(recorder.Header().Get("Content-Type"), "text/html"); got != tc.wantHTML {
				t.Errorf("HTML content type = %v, want %v", got, tc.wantHTML)
			}
			if tc.wantHTML && !strings.Contains(body, `data-faas-error-code="not_found"`) {
				t.Error("HTML response missing error code")
			}
			if !tc.wantHTML {
				if got := recorder.Header().Get("Content-Type"); got != "application/problem+json" {
					t.Errorf("Content-Type = %q, want application/problem+json", got)
				}
				var problem Problem
				if err := json.Unmarshal(recorder.Body.Bytes(), &problem); err != nil {
					t.Fatalf("JSON problem decode: %v", err)
				}
				if problem.Code != "not_found" {
					t.Errorf("problem code = %q, want not_found", problem.Code)
				}
			}
			if got := recorder.Header().Get("Vary") == "Accept"; got != tc.wantVary {
				t.Errorf("Vary: Accept = %v, want %v", got, tc.wantVary)
			}
		})
	}
}
