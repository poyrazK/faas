package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNormalizeTelemetryUserAgent(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "chrome", raw: "Mozilla/5.0 Chrome/128.0 Safari/537.36", want: "chrome"},
		{name: "edge before chrome", raw: "Mozilla/5.0 Chrome/128.0 Edg/128.0", want: "edge"},
		{name: "bot", raw: "Mozilla/5.0 (compatible; Googlebot/2.1)", want: "bot"},
		{name: "curl", raw: "curl/8.9.0", want: "curl"},
		{name: "empty", raw: "", want: telemetryUnknownDimension},
		{name: "other", raw: "custom-client/1.0", want: telemetryOtherDimension},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeTelemetryUserAgent(tt.raw); got != tt.want {
				t.Fatalf("normalizeTelemetryUserAgent(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestNormalizeTelemetryReferrer(t *testing.T) {
	if got := normalizeTelemetryReferrer("https://Example.COM/path?q=secret#fragment"); got != "example.com" {
		t.Fatalf("referrer host = %q, want example.com", got)
	}
	if got := normalizeTelemetryReferrer(""); got != telemetryNoReferrer {
		t.Fatalf("empty referrer = %q, want %q", got, telemetryNoReferrer)
	}
	if got := normalizeTelemetryReferrer("not a URL"); got != telemetryOtherDimension {
		t.Fatalf("malformed referrer = %q, want %q", got, telemetryOtherDimension)
	}
}

func TestLookupTelemetryCountryWithoutTrustedIP(t *testing.T) {
	h := &Handler{}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if got := h.lookupTelemetryCountry(r); got != telemetryUnknownDimension {
		t.Fatalf("country without trusted XFF = %q, want %q", got, telemetryUnknownDimension)
	}
}

func TestCollapseRequestTelemetryKeepsDimensionsSeparate(t *testing.T) {
	when := time.Date(2026, 9, 7, 9, 30, 0, 0, time.UTC)
	base := RequestTelemetryRow{
		AccountID: uuid.New(), AppID: uuid.New(), DeploymentID: uuid.New(),
		Route: "GET /", Method: http.MethodGet, Status: http.StatusOK,
		LatencyMS: 10, ReceivedAt: when, Count: 1,
		UAFamily: "chrome", ReferrerHost: "example.com", Country: "US",
	}
	other := base
	other.Country = "DE"
	got := collapseRequestTelemetry([]RequestTelemetryRow{base, other})
	if len(got) != 2 {
		t.Fatalf("collapsed rows = %d, want 2 for distinct countries", len(got))
	}
}
