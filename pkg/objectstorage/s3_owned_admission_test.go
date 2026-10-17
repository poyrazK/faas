package objectstorage

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestOwnedWriteAdmissionHintIsSignedBoundedAndNeverOutcomeProof(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		values []string
		want   error
	}{
		{"ready", 200, []string{"ready"}, nil},
		{"older node", 200, nil, nil},
		{"blocked", 503, []string{"blocked"}, ErrWritePressure},
		{"generic 503", 503, nil, ErrUnavailable},
		{"ambiguous ready", 200, []string{"ready", "blocked"}, ErrUnavailable},
		{"ambiguous blocked", 503, []string{"blocked", "blocked"}, ErrUnavailable},
		{"unexpected success", 200, []string{"blocked"}, ErrUnavailable},
		{"missing bucket", 404, nil, ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodHead || r.URL.Path != "/bucket" || r.Header.Get(OwnedNodeAdmissionHeader) != "put" || r.Header.Get(OwnedNodeAdmissionSizeHeader) != "17" {
					t.Error("unexpected hint request", r.Method, r.URL.Path)
				}
				for _, name := range []string{OwnedNodeAdmissionHeader, OwnedNodeAdmissionSizeHeader} {
					if !strings.Contains(r.Header.Get("Authorization"), strings.ToLower(name)) {
						t.Error("unsigned admission selector", name)
					}
				}
				for _, v := range tc.values {
					w.Header().Add(OwnedNodeAdmissionStatusHeader, v)
				}
				w.WriteHeader(tc.status)
			}))
			defer server.Close()
			config := testBackend()
			config.Endpoint, config.OwnedNodeReceipts = server.URL, true
			p, err := NewS3(config, testCredentials)
			if err != nil {
				t.Fatal(err)
			}
			err = p.(ObjectWriteAdmissionChecker).CheckObjectWriteAdmission(t.Context(), "bucket", "put", 17)
			if !errors.Is(err, tc.want) || calls.Load() != 1 {
				t.Fatal("wrong hint or automatic retry", err, calls.Load())
			}
			if errors.Is(err, ErrWriteRejected) || ProviderWriteDefinitelyRejected(p, 503) {
				t.Fatal("hint became write rejection proof")
			}
		})
	}
}

func TestOrdinaryS3WriteAdmissionDoesNotCallProvider(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("ordinary S3 received owned-node selector") }))
	defer server.Close()
	config := testBackend()
	config.Endpoint = server.URL
	p, err := NewS3(config, testCredentials)
	if err != nil {
		t.Fatal(err)
	}
	if err = p.(ObjectWriteAdmissionChecker).CheckObjectWriteAdmission(t.Context(), "bucket", "put", 1); err != nil {
		t.Fatal(err)
	}
}
