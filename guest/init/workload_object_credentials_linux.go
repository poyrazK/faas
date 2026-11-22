package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

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

// workloadObjectCredentialsTimeout bounds one exchange with apid, so a hung
// connection fails the request instead of stalling the SDK.
const workloadObjectCredentialsTimeout = 10 * time.Second

type workloadObjectCredentials struct {
	identity   identityFetcher
	client     *http.Client
	now        func() time.Time
	generation func() uint64

	// mu guards cache and gen only; fetches run outside it, one per bucket.
	mu     sync.Mutex
	cache  map[string]cachedObjectCredential
	gen    uint64
	flight singleflight.Group
}

type cachedObjectCredential struct {
	body       []byte
	expiration time.Time
}

type objectCredentialResult struct {
	body   []byte
	status int
	code   string
}

// newWorkloadObjectCredentials serves credentials through client (nil: a
// dedicated client with workloadObjectCredentialsTimeout). generation is the
// guest's restore generation: a restored snapshot drops cached credentials,
// which belong to the instance that took the snapshot, and the client's
// pooled connections, which did not survive the restore.
func newWorkloadObjectCredentials(identity identityFetcher, client *http.Client, now func() time.Time, generation func() uint64) *workloadObjectCredentials {
	if client == nil {
		client = &http.Client{Timeout: workloadObjectCredentialsTimeout, Transport: http.DefaultTransport.(*http.Transport).Clone()}
	}
	return &workloadObjectCredentials{identity: identity, client: client, now: now, generation: generation,
		cache: map[string]cachedObjectCredential{}, gen: generation()}
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
	if body, ok := c.cached(bucket); ok {
		writeObjectCredential(w, body)
		return
	}
	v, _, _ := c.flight.Do(bucket, func() (any, error) {
		if body, ok := c.cached(bucket); ok {
			return objectCredentialResult{body: body}, nil
		}
		body, status, code := c.fetch(r, bucket)
		return objectCredentialResult{body: body, status: status, code: code}, nil
	})
	result := v.(objectCredentialResult)
	if result.code != "" {
		writeIdentityGuestError(w, result.status, result.code)
		return
	}
	writeObjectCredential(w, result.body)
}

// cached returns bucket's credential while it is valid for more than the
// refresh margin. A restore since the last call empties the cache first.
func (c *workloadObjectCredentials) cached(bucket string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if gen := c.generation(); gen != c.gen {
		c.gen = gen
		c.cache = map[string]cachedObjectCredential{}
		c.client.CloseIdleConnections()
	}
	cached, ok := c.cache[bucket]
	if !ok || !c.now().Add(workloadObjectCredentialsMargin).Before(cached.expiration) {
		return nil, false
	}
	return cached.body, true
}

func (c *workloadObjectCredentials) store(bucket string, body []byte, expiration time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache[bucket] = cachedObjectCredential{body: body, expiration: expiration}
}

// fetch exchanges a bindings token for a credential at apid.
func (c *workloadObjectCredentials) fetch(r *http.Request, bucket string) ([]byte, int, string) {
	token, err := c.identity(workloadBindingsAudience)
	if err != nil || token.AccessToken == "" {
		return nil, http.StatusServiceUnavailable, "identity_unavailable"
	}
	reqBody, _ := json.Marshal(api.ObjectWorkloadCredentialRequest{Bucket: bucket})
	req, err := http.NewRequestWithContext(context.WithoutCancel(r.Context()), http.MethodPost, currentWorkloadAPIURL()+"/v1/workload/object-storage/credentials", bytes.NewReader(reqBody))
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
	c.store(bucket, body, parsed.Expiration)
	return body, 0, ""
}

func writeObjectCredential(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(body)
}
