package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Workload S3 credentials on the guest's loopback endpoint
// (ADR-20261122191423 slice 2). AWS SDKs read
// AWS_CONTAINER_CREDENTIALS_FULL_URI, which project apply points here for a
// service with a bucket grant. Each request is served from a per-bucket cache
// until five minutes before the credential expires; then the instance trades
// a fresh gregale:bindings token for a new credential at apid. No static key
// is ever written into the app's environment.

const (
	workloadObjectCredentialsPath     = api.WorkloadObjectCredentialsPath
	workloadBindingsAudience          = "gregale:bindings"
	workloadObjectCredentialsMargin   = 5 * time.Minute
	workloadObjectCredentialsMaxBytes = 16 << 10
	defaultWorkloadAPIURL             = "https://api.gregale.dev"
)

var (
	workloadAPIURLMu sync.RWMutex
	workloadAPIURL   = defaultWorkloadAPIURL
)

// rememberWorkloadAPIURL keeps the workload's GREGALE_API_URL (stamped by
// schedd) so the credential exchange talks to the same API as the app.
func rememberWorkloadAPIURL(env []string) {
	for _, kv := range env {
		if value, ok := strings.CutPrefix(kv, "GREGALE_API_URL="); ok && strings.HasPrefix(value, "https://") {
			workloadAPIURLMu.Lock()
			workloadAPIURL = strings.TrimRight(value, "/")
			workloadAPIURLMu.Unlock()
		}
	}
}

func currentWorkloadAPIURL() string {
	workloadAPIURLMu.RLock()
	defer workloadAPIURLMu.RUnlock()
	return workloadAPIURL
}

type identityFetcher func(audience string) (workloadIdentityGuestResponse, error)

type workloadObjectCredentials struct {
	identity identityFetcher
	client   *http.Client
	now      func() time.Time

	mu    sync.Mutex
	cache map[string]cachedObjectCredential
}

type cachedObjectCredential struct {
	body       []byte
	expiration time.Time
}

func newWorkloadObjectCredentials(identity identityFetcher, client *http.Client, now func() time.Time) *workloadObjectCredentials {
	return &workloadObjectCredentials{identity: identity, client: client, now: now, cache: map[string]cachedObjectCredential{}}
}

func (c *workloadObjectCredentials) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeIdentityGuestError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	bucket := r.URL.Query().Get("bucket")
	if len(bucket) > 128 {
		writeIdentityGuestError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if cached, ok := c.cache[bucket]; ok && c.now().Add(workloadObjectCredentialsMargin).Before(cached.expiration) {
		writeObjectCredential(w, cached.body)
		return
	}
	body, status, code := c.fetch(r, bucket)
	if code != "" {
		writeIdentityGuestError(w, status, code)
		return
	}
	writeObjectCredential(w, body)
}

// fetch exchanges a bindings token for a credential at apid.
func (c *workloadObjectCredentials) fetch(r *http.Request, bucket string) ([]byte, int, string) {
	token, err := c.identity(workloadBindingsAudience)
	if err != nil || token.AccessToken == "" {
		return nil, http.StatusServiceUnavailable, "identity_unavailable"
	}
	reqBody, _ := json.Marshal(api.ObjectWorkloadCredentialRequest{Bucket: bucket})
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, currentWorkloadAPIURL()+"/v1/workload/object-storage/credentials", bytes.NewReader(reqBody))
	if err != nil {
		return nil, http.StatusServiceUnavailable, "credentials_unavailable"
	}
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, http.StatusServiceUnavailable, "credentials_unavailable"
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, workloadObjectCredentialsMaxBytes))
	if err != nil {
		return nil, http.StatusServiceUnavailable, "credentials_unavailable"
	}
	switch {
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusBadRequest:
		return nil, resp.StatusCode, "bucket_not_granted"
	case resp.StatusCode != http.StatusOK:
		return nil, http.StatusServiceUnavailable, "credentials_unavailable"
	}
	var parsed api.ObjectWorkloadCredentialResponse
	if json.Unmarshal(body, &parsed) != nil || parsed.AccessKeyID == "" || parsed.SecretAccessKey == "" || parsed.Expiration.IsZero() {
		return nil, http.StatusServiceUnavailable, "credentials_unavailable"
	}
	c.cache[bucket] = cachedObjectCredential{body: body, expiration: parsed.Expiration}
	return body, 0, ""
}

func writeObjectCredential(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(body)
}
