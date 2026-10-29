package oci

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	testECRHost      = "123456789012.dkr.ecr.eu-west-1.amazonaws.com"
	testAccessKeyID  = "AKIAIOSFODNN7EXAMPLE"
	testSecretKey    = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
	testECRPassword  = "ecr-exchanged-password"
	testBasicPayload = `{"schemaVersion":2}`
)

// basicRegistry answers every request without the expected Basic
// credential with a Basic challenge, as Amazon ECR does, and records the
// Authorization values it saw.
type basicRegistry struct {
	srv        *httptest.Server
	user, pass string
	mu         sync.Mutex
	seen       []string
}

func newBasicRegistry(t *testing.T, user, pass string) *basicRegistry {
	t.Helper()
	b := &basicRegistry{user: user, pass: pass}
	b.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b.mu.Lock()
		b.seen = append(b.seen, r.Header.Get("Authorization"))
		b.mu.Unlock()
		if u, p, ok := r.BasicAuth(); !ok || u != b.user || p != b.pass {
			w.Header().Set("Www-Authenticate", `Basic realm="https://`+testECRHost+`/",service="ecr.amazonaws.com"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
		_, _ = fmt.Fprint(w, testBasicPayload)
	}))
	t.Cleanup(b.srv.Close)
	return b
}

func (b *basicRegistry) client() *RegistryClient {
	return NewRegistryClient(WithHTTPClient(b.srv.Client()), WithEndpoint("https", strings.TrimPrefix(b.srv.URL, "https://")))
}

func (b *basicRegistry) authorizations() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.seen...)
}

// Amazon ECR answers with a Basic challenge and no Bearer realm; the client
// used to ask it for a token and fail every pull (ADR-20261025124445).
func TestRegistryAnswersBasicChallengeWithCredential(t *testing.T) {
	reg := newBasicRegistry(t, "AWS", "token-password")
	ref := testECRHost + "/app:latest"
	digest, err := reg.client().PullDigestWithAuth(context.Background(), ref, &BasicAuth{Username: "AWS", Password: "token-password"})
	if err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(testBasicPayload))); digest != want {
		t.Fatalf("digest = %q, want %q", digest, want)
	}
	if _, err := reg.client().PullDigest(context.Background(), ref); err == nil {
		t.Fatal("an anonymous pull answered a Basic challenge")
	}
}

func TestRegistryNeverSendsBasicCredentialOverHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, _, ok := r.BasicAuth(); ok {
			t.Error("credential sent over plain HTTP")
		}
		w.Header().Set("Www-Authenticate", `Basic realm="registry"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	c := NewRegistryClient(WithEndpoint("http", strings.TrimPrefix(srv.URL, "http://")))
	_, err := c.PullDigestWithAuth(context.Background(), "registry.example/app:latest", &BasicAuth{Username: "u", Password: "p"})
	if err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("err = %v, want an https refusal", err)
	}
}

// fakeECRAPI serves GetAuthorizationToken and records each call.
type fakeECRAPI struct {
	srv   *httptest.Server
	calls atomic.Int32
	fail  atomic.Bool
}

func newFakeECRAPI(t *testing.T, expiresAt time.Time) *fakeECRAPI {
	t.Helper()
	api := &fakeECRAPI{}
	api.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api.calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		if got := r.Header.Get("X-Amz-Target"); got != ecrGetAuthorizationTokenTarget {
			t.Errorf("X-Amz-Target = %q", got)
		}
		authz := r.Header.Get("Authorization")
		if !strings.HasPrefix(authz, "AWS4-HMAC-SHA256 Credential="+testAccessKeyID+"/") || !strings.Contains(authz, "/eu-west-1/ecr/aws4_request") {
			t.Errorf("request not signed for ecr in eu-west-1: %q", authz)
		}
		if strings.Contains(authz, testSecretKey) || strings.Contains(string(body), testSecretKey) {
			t.Error("secret key sent to the ECR API")
		}
		if string(body) != `{"registryIds":["123456789012"]}` {
			t.Errorf("body = %s", body)
		}
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		if api.fail.Load() {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprintf(w, `{"__type":"UnrecognizedClientException","message":"The security token included in the request is invalid. %s"}`, testSecretKey)
			return
		}
		token := base64.StdEncoding.EncodeToString([]byte("AWS:" + testECRPassword))
		_, _ = fmt.Fprintf(w, `{"authorizationData":[{"authorizationToken":%q,"expiresAt":%d,"proxyEndpoint":"https://%s"}]}`, token, expiresAt.Unix(), testECRHost)
	}))
	t.Cleanup(api.srv.Close)
	return api
}

// An IAM access key stored for an ECR registry is exchanged for ECR's
// 12-hour password; the secret key never reaches the registry, and the
// password is reused until shortly before it expires.
func TestECRAccessKeyIsExchangedForAPassword(t *testing.T) {
	reg := newBasicRegistry(t, "AWS", testECRPassword)
	api := newFakeECRAPI(t, time.Now().Add(12*time.Hour))
	c := reg.client()
	c.ecrAPIBase = api.srv.URL
	key := &BasicAuth{Username: testAccessKeyID, Password: testSecretKey}
	for range 2 {
		if _, err := c.PullDigestWithAuth(context.Background(), testECRHost+"/app:latest", key); err != nil {
			t.Fatal(err)
		}
	}
	if api.calls.Load() != 1 {
		t.Fatalf("GetAuthorizationToken calls = %d, want 1 (cached)", api.calls.Load())
	}
	for _, authz := range reg.authorizations() {
		if strings.Contains(authz, base64.StdEncoding.EncodeToString([]byte(testAccessKeyID+":"+testSecretKey))) {
			t.Fatal("the IAM key was sent to the registry")
		}
	}
}

func TestECRExchangeRenewsBeforeExpiry(t *testing.T) {
	reg := newBasicRegistry(t, "AWS", testECRPassword)
	api := newFakeECRAPI(t, time.Now().Add(10*time.Minute)) // inside the renewal window
	c := reg.client()
	c.ecrAPIBase = api.srv.URL
	key := &BasicAuth{Username: testAccessKeyID, Password: testSecretKey}
	for range 2 {
		if _, err := c.PullDigestWithAuth(context.Background(), testECRHost+"/app:latest", key); err != nil {
			t.Fatal(err)
		}
	}
	if api.calls.Load() != 2 {
		t.Fatalf("GetAuthorizationToken calls = %d, want 2", api.calls.Load())
	}
}

func TestECRExchangeFailureNamesTheAWSErrorWithoutTheKey(t *testing.T) {
	reg := newBasicRegistry(t, "AWS", testECRPassword)
	api := newFakeECRAPI(t, time.Now().Add(12*time.Hour))
	api.fail.Store(true)
	c := reg.client()
	c.ecrAPIBase = api.srv.URL
	_, err := c.ResolveImage(context.Background(), testECRHost+"/app:latest", &BasicAuth{Username: testAccessKeyID, Password: testSecretKey})
	if err == nil || !strings.Contains(err.Error(), "UnrecognizedClientException") {
		t.Fatalf("err = %v, want the AWS error type", err)
	}
	if strings.Contains(err.Error(), testSecretKey) {
		t.Fatalf("error carries the secret key: %v", err)
	}
	if len(reg.authorizations()) != 0 {
		t.Fatal("the registry was called after a failed exchange")
	}
}

// Credentials that are not an IAM key for an ECR host pass through: an ECR
// password the customer refreshes, or a key-shaped username elsewhere.
func TestRegistryAuthLeavesOtherCredentials(t *testing.T) {
	api := newFakeECRAPI(t, time.Now().Add(12*time.Hour))
	c := NewRegistryClient()
	c.ecrAPIBase = api.srv.URL
	for _, tc := range []struct {
		registry string
		auth     *BasicAuth
	}{
		{testECRHost, &BasicAuth{Username: "AWS", Password: "pw"}},
		{"ghcr.io", &BasicAuth{Username: testAccessKeyID, Password: testSecretKey}},
		{testECRHost, &BasicAuth{Username: "ASIAIOSFODNN7EXAMPLE", Password: testSecretKey}},
		{testECRHost, nil},
	} {
		got, err := c.registryAuth(context.Background(), Reference{Registry: tc.registry, Repository: "app"}, tc.auth)
		if err != nil || got != tc.auth {
			t.Errorf("%s %+v: got %+v, %v; want unchanged", tc.registry, tc.auth, got, err)
		}
	}
	if api.calls.Load() != 0 {
		t.Fatalf("GetAuthorizationToken calls = %d, want 0", api.calls.Load())
	}
}
