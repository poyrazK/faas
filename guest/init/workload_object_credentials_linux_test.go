package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestWorkloadObjectCredentialsCachesUntilNearExpiry(t *testing.T) {
	now := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
	issued := 0
	var gotAuth, gotBucket string
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		issued++
		gotAuth = r.Header.Get("Authorization")
		var req api.ObjectWorkloadCredentialRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		gotBucket = req.Bucket
		_ = json.NewEncoder(w).Encode(api.ObjectWorkloadCredentialResponse{AccessKeyID: "GRGWAAAAAAAAAAAAAAAA", SecretAccessKey: "s",
			Token: "t", Expiration: now.Add(time.Hour), Bucket: "assets"})
	}))
	defer apiServer.Close()
	rememberWorkloadAPIURL([]string{"GREGALE_API_URL=" + strings.Replace(apiServer.URL, "http://", "https://", 1)})
	defer rememberWorkloadAPIURL([]string{"GREGALE_API_URL=" + defaultWorkloadAPIURL})
	// The test server is plain HTTP; route the https URL to it.
	client := &http.Client{Transport: rewriteToHTTP{}}
	audiences := []string{}
	identity := func(audience string) (workloadIdentityGuestResponse, error) {
		audiences = append(audiences, audience)
		return workloadIdentityGuestResponse{AccessToken: "eyJ.binding.token"}, nil
	}
	creds := newWorkloadObjectCredentials(identity, client, func() time.Time { return now })

	get := func(query string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		creds.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, workloadObjectCredentialsPath+query, nil))
		return rec
	}
	if rec := get("?bucket=assets"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"AccessKeyId":"GRGWAAAAAAAAAAAAAAAA"`) {
		t.Fatalf("first fetch: %d %s", rec.Code, rec.Body)
	}
	if gotAuth != "Bearer eyJ.binding.token" || gotBucket != "assets" || audiences[0] != workloadBindingsAudience {
		t.Fatalf("exchange: auth %q bucket %q audiences %v", gotAuth, gotBucket, audiences)
	}
	now = now.Add(50 * time.Minute)
	get("?bucket=assets")
	if issued != 1 {
		t.Fatalf("cached credential refetched: %d", issued)
	}
	now = now.Add(6 * time.Minute)
	get("?bucket=assets")
	if issued != 2 {
		t.Fatalf("credential near expiry not refreshed: %d", issued)
	}
}

func TestWorkloadObjectCredentialsRefusals(t *testing.T) {
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer apiServer.Close()
	rememberWorkloadAPIURL([]string{"GREGALE_API_URL=" + strings.Replace(apiServer.URL, "http://", "https://", 1)})
	defer rememberWorkloadAPIURL([]string{"GREGALE_API_URL=" + defaultWorkloadAPIURL})
	ok := func(string) (workloadIdentityGuestResponse, error) {
		return workloadIdentityGuestResponse{AccessToken: "eyJ.t"}, nil
	}
	down := func(string) (workloadIdentityGuestResponse, error) {
		return workloadIdentityGuestResponse{}, errors.New("vsock")
	}
	client := &http.Client{Transport: rewriteToHTTP{}}
	for name, tc := range map[string]struct {
		identity identityFetcher
		method   string
		want     int
	}{
		"not granted":    {ok, http.MethodGet, http.StatusForbidden},
		"identity down":  {down, http.MethodGet, http.StatusServiceUnavailable},
		"method not GET": {ok, http.MethodPost, http.StatusMethodNotAllowed},
	} {
		rec := httptest.NewRecorder()
		newWorkloadObjectCredentials(tc.identity, client, time.Now).ServeHTTP(rec, httptest.NewRequest(tc.method, workloadObjectCredentialsPath, nil))
		if rec.Code != tc.want {
			t.Errorf("%s: %d, want %d", name, rec.Code, tc.want)
		}
	}
}

func TestRememberWorkloadAPIURLAcceptsHTTPSOnly(t *testing.T) {
	defer rememberWorkloadAPIURL([]string{"GREGALE_API_URL=" + defaultWorkloadAPIURL})
	rememberWorkloadAPIURL([]string{"GREGALE_API_URL=http://evil.example"})
	if currentWorkloadAPIURL() != defaultWorkloadAPIURL {
		t.Fatalf("plain http accepted: %s", currentWorkloadAPIURL())
	}
	rememberWorkloadAPIURL([]string{"GREGALE_API_URL=https://api.example/"})
	if currentWorkloadAPIURL() != "https://api.example" {
		t.Fatalf("api url = %s", currentWorkloadAPIURL())
	}
}

type rewriteToHTTP struct{}

func (rewriteToHTTP) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.URL.Scheme = "http"
	return http.DefaultTransport.RoundTrip(r)
}
