// adr: 259 — static browser clients inherit the release selected for their document.
package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsBrowserDocumentNavigation(t *testing.T) {
	tests := []struct {
		name   string
		method string
		accept string
		dest   string
		want   bool
	}{
		{name: "document fetch metadata", method: http.MethodGet, dest: "document", want: true},
		{name: "html accept fallback", method: http.MethodGet, accept: "text/html,application/xhtml+xml", want: true},
		{name: "fetch metadata rejects html fetch", method: http.MethodGet, accept: "text/html", dest: "empty", want: false},
		{name: "json request", method: http.MethodGet, accept: "application/json", want: false},
		{name: "non-get navigation", method: http.MethodPost, accept: "text/html", dest: "document", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(tt.method, "https://app.example/", nil)
			if tt.accept != "" {
				r.Header.Set("Accept", tt.accept)
			}
			if tt.dest != "" {
				r.Header.Set("Sec-Fetch-Dest", tt.dest)
			}
			if got := isBrowserDocumentNavigation(r); got != tt.want {
				t.Fatalf("isBrowserDocumentNavigation() = %v, want %v", got, tt.want)
			}
		})
	}
}
