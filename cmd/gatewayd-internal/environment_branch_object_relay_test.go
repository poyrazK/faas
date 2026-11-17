// adr: 20261031193635
package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

type objectRelayTestStore struct {
	policies map[string]state.EnvironmentBranchEgressPolicy
	keys     map[string]string // access key -> environment
	err      error
}

func (s *objectRelayTestStore) EnvironmentBranchEgress(_ context.Context, deploymentID string) (state.EnvironmentBranchEgressPolicy, error) {
	if s.err != nil {
		return state.EnvironmentBranchEgressPolicy{}, s.err
	}
	policy, ok := s.policies[deploymentID]
	if !ok {
		return state.EnvironmentBranchEgressPolicy{}, state.ErrNotFound
	}
	return policy, nil
}

func (s *objectRelayTestStore) EnvironmentBranchDatabaseEndpoints(context.Context, string) ([]state.EnvironmentBranchDatabaseEndpoint, error) {
	return nil, nil
}

func (s *objectRelayTestStore) EnvironmentBranchObjectCredentialAllowed(_ context.Context, environmentID, accessKeyID string) (bool, error) {
	return s.keys[accessKeyID] == environmentID, nil
}

// Slice 6c: the relay forwards only a contained branch guest's request signed
// with one of its own branch's keys, unchanged and with the Host it signed.
func TestEnvironmentBranchObjectRelay(t *testing.T) {
	var forwarded []*http.Request
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded = append(forwarded, r)
		_, _ = io.WriteString(w, "object")
	}))
	t.Cleanup(upstream.Close)
	store := &objectRelayTestStore{
		policies: map[string]state.EnvironmentBranchEgressPolicy{
			"dep-contained": {Mode: state.EnvironmentBranchEgressContained, EnvironmentID: "env-branch"},
			"dep-simulated": {Mode: state.EnvironmentBranchEgressSimulated, EnvironmentID: "env-branch"},
			"dep-live":      {Mode: state.EnvironmentBranchEgressLive, EnvironmentID: "env-live"},
		},
		keys: map[string]string{"GRGBRANCHKEY": "env-branch", "GRGLIVEKEY": "env-live"},
	}
	callers := map[string]string{"10.100.0.10:5000": "dep-contained", "10.100.0.11:5000": "dep-simulated", "10.100.0.12:5000": "dep-live", "10.100.0.13:5000": "dep-gone"}
	resolve := func(_ context.Context, remote string) (string, string, error) {
		if remote == "10.100.0.99:5000" {
			return "", "", errors.New("lookup failed")
		}
		return "app", callers[remote], nil
	}
	relay, err := newEnvironmentBranchObjectRelay(resolve, store, strings.TrimPrefix(upstream.URL, "http://"), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	header := func(key string) string {
		return "AWS4-HMAC-SHA256 Credential=" + key + "/20261107/us-east-1/s3/aws4_request, SignedHeaders=host;x-amz-date, Signature=abc"
	}
	request := func(remote, target, authorization string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, target, nil)
		r.Host = "objects.gregale:10085"
		r.RemoteAddr = remote
		if authorization != "" {
			r.Header.Set("Authorization", authorization)
		}
		return r
	}

	for name, r := range map[string]*http.Request{
		"contained, header key": request("10.100.0.10:5000", "http://objects.gregale:10085/assets/a.txt", header("GRGBRANCHKEY")),
		"simulated, presigned":  request("10.100.0.11:5000", "http://objects.gregale:10085/assets/a.txt?X-Amz-Credential=GRGBRANCHKEY%2F20261107%2Fus-east-1%2Fs3%2Faws4_request&X-Amz-Signature=abc", ""),
	} {
		forwarded = nil
		w := httptest.NewRecorder()
		relay.ServeHTTP(w, r)
		if w.Code != http.StatusOK || w.Body.String() != "object" || len(forwarded) != 1 {
			t.Fatalf("%s: %d %s forwarded=%d", name, w.Code, w.Body.String(), len(forwarded))
		}
		if forwarded[0].Host != "objects.gregale:10085" || forwarded[0].URL.Path != "/assets/a.txt" {
			t.Fatalf("%s: forwarded host=%q path=%q, want the signed host and path", name, forwarded[0].Host, forwarded[0].URL.Path)
		}
	}

	both := request("10.100.0.10:5000", "http://objects.gregale:10085/assets/a.txt?X-Amz-Credential=GRGBRANCHKEY%2Fx", header("GRGBRANCHKEY"))
	for name, tc := range map[string]struct {
		r      *http.Request
		status int
		code   string
	}{
		"parent or production key":  {request("10.100.0.10:5000", "http://objects.gregale:10085/prod/a.txt", header("GRGLIVEKEY")), http.StatusForbidden, "InvalidAccessKeyId"},
		"unsigned request":          {request("10.100.0.10:5000", "http://objects.gregale:10085/assets/a.txt", ""), http.StatusForbidden, "AccessDenied"},
		"upload grant":              {request("10.100.0.10:5000", "http://objects.gregale:10085/assets/a.txt?gregale-upload=grant", header("GRGBRANCHKEY")), http.StatusForbidden, "AccessDenied"},
		"header and query keys":     {both, http.StatusForbidden, "AccessDenied"},
		"live branch caller":        {request("10.100.0.12:5000", "http://objects.gregale:10085/assets/a.txt", header("GRGLIVEKEY")), http.StatusForbidden, "AccessDenied"},
		"unknown caller":            {request("10.100.0.50:5000", "http://objects.gregale:10085/assets/a.txt", header("GRGBRANCHKEY")), http.StatusForbidden, "AccessDenied"},
		"deployment gone":           {request("10.100.0.13:5000", "http://objects.gregale:10085/assets/a.txt", header("GRGBRANCHKEY")), http.StatusForbidden, "AccessDenied"},
		"caller lookup failed":      {request("10.100.0.99:5000", "http://objects.gregale:10085/assets/a.txt", header("GRGBRANCHKEY")), http.StatusServiceUnavailable, "ServiceUnavailable"},
		"malformed authorization":   {request("10.100.0.10:5000", "http://objects.gregale:10085/assets/a.txt", "AWS4-HMAC-SHA256 Signature=abc"), http.StatusForbidden, "AccessDenied"},
		"other signature algorithm": {request("10.100.0.10:5000", "http://objects.gregale:10085/assets/a.txt", "AWS GRGBRANCHKEY:abc"), http.StatusForbidden, "AccessDenied"},
	} {
		forwarded = nil
		w := httptest.NewRecorder()
		relay.ServeHTTP(w, tc.r)
		if w.Code != tc.status || !strings.Contains(w.Body.String(), "<Code>"+tc.code+"</Code>") || len(forwarded) != 0 {
			t.Errorf("%s: %d %s forwarded=%d, want %d %s and nothing forwarded", name, w.Code, w.Body.String(), len(forwarded), tc.status, tc.code)
		}
	}

	store.err = errors.New("database down")
	w := httptest.NewRecorder()
	relay.ServeHTTP(w, request("10.100.0.10:5000", "http://objects.gregale:10085/assets/a.txt", header("GRGBRANCHKEY")))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("store failure = %d, want 503", w.Code)
	}
}

func TestEnvironmentBranchObjectRelayUpstreamMustBeLoopback(t *testing.T) {
	resolve := func(context.Context, string) (string, string, error) { return "", "", nil }
	for _, upstream := range []string{"10.0.0.5:8084", "s3.example:8084", "8084"} {
		if _, err := newEnvironmentBranchObjectRelay(resolve, &objectRelayTestStore{}, upstream, slog.New(slog.DiscardHandler)); err == nil {
			t.Errorf("upstream %q accepted", upstream)
		}
	}
	if _, err := newEnvironmentBranchObjectRelay(resolve, &objectRelayTestStore{}, "", slog.New(slog.DiscardHandler)); err != nil {
		t.Fatalf("default upstream: %v", err)
	}
}
