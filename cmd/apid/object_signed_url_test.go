package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/s3gateway"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

type signedURLNative struct {
	mu             sync.Mutex
	headers        http.Header
	writes, keys   int
	lost, disabled bool
}

type signedURLResponse struct {
	StatusCode int
	Header     http.Header
	Data       string
}

type signedURLBody struct {
	reader io.Reader
	before func()
}

func (b *signedURLBody) Read(p []byte) (int, error) {
	if b.before != nil {
		before := b.before
		b.before = nil
		before()
	}
	return b.reader.Read(p)
}

func (f *signedURLNative) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("X-Amz-Target") == "TrentService.DescribeKey" {
		f.keys++
		if f.disabled {
			w.WriteHeader(400)
			_, _ = io.WriteString(w, `{"__type":"DisabledException"}`)
			return
		}
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		_, _ = fmt.Fprintf(w, `{"KeyMetadata":{"Arn":%q,"AWSAccountId":"111122223333","KeyId":"abcd8987-12d6-45ad-a4bc-d384c10d9149","Enabled":true,"KeyState":"Enabled","KeyUsage":"ENCRYPT_DECRYPT","KeySpec":"SYMMETRIC_DEFAULT","KeyManager":"CUSTOMER"}}`, journalNativeKMSKey)
		return
	}
	if r.Method == http.MethodPut {
		f.writes++
		f.headers = r.Header.Clone()
		_, _ = io.Copy(io.Discard, r.Body)
		if f.lost {
			w.WriteHeader(200)
			return
		}
	}
	if f.headers == nil {
		w.WriteHeader(404)
		return
	}
	for name, values := range f.headers {
		if strings.HasPrefix(strings.ToLower(name), "x-amz-") || name == "Content-Type" {
			w.Header()[name] = append([]string(nil), values...)
		}
	}
	w.Header().Set("ETag", `"signed-url"`)
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		w.Header().Set("Content-Length", "3")
	}
	if r.Method == http.MethodGet {
		_, _ = io.WriteString(w, "abc")
	}
}

// adr: 557
func TestObjectSignedURLE2EMem(t *testing.T) {
	e := setup(t, api.PlanPro)
	signedURLE2E(t, e.s, e.store, e.acct, e.key, nil)
}
func TestObjectSignedURLE2EPG(t *testing.T) {
	e := setupPGHandler(t, api.PlanPro)
	signedURLE2E(t, e.s, e.store, e.acct, e.key, e.pool)
}

func signedURLE2E(t *testing.T, s *server, st state.Store, acct state.Account, bearer string, pool *pgxpool.Pool) {
	identity, teardown := withTestIdentities(t)
	defer teardown()
	native := &signedURLNative{}
	upstream := httptest.NewServer(http.HandlerFunc(native.serve))
	defer upstream.Close()
	_, b, backend, _ := seedEncryptionJournalStorage(t, s, st, acct, upstream.URL)
	if _, err := st.UpsertRuntimeConfig(t.Context(), state.RuntimeConfigUpdate{Key: runtimeConfigS3, Scope: state.RuntimeConfigScopeGlobal, DesiredValue: boolJSON(true), ApplyMode: state.RuntimeConfigApplyHot, Reason: "signed URL test"}); err != nil {
		t.Fatal(err)
	}
	if err := s.runtimeConfig.reconcile(t.Context(), st); err != nil {
		t.Fatal(err)
	}
	var gateway http.Handler
	public := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { gateway.ServeHTTP(w, r) }))
	defer public.Close()
	s.objectStorage.PublicEndpoint = public.URL
	h, err := s3gateway.New(s3gateway.Config{Registry: s.objectStorage, Store: st.(s3gateway.Store), RequestMetrics: st.(state.ObjectStorageProviderUsageStore), SpoolDir: t.TempDir(), OpenSecret: func(blob []byte) (string, error) {
		ns, plain, e := secretbox.OpenBytes(identity, blob)
		if e != nil {
			return "", fmt.Errorf("open URL credential: %w", e)
		}
		if ns != s3gateway.CredentialSecretNamespace {
			return "", errors.New("invalid URL credential namespace")
		}
		return string(plain), nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	gateway = h
	apiHandler := s.handler()
	issue := func(req api.ObjectSignRequest) api.ObjectSignedRequest {
		t.Helper()
		body, e := json.Marshal(req)
		if e != nil {
			t.Fatal(e)
		}
		r := httptest.NewRequest(http.MethodPost, "/v1/apps/encrypted-journal/buckets/"+b.ID+"/signed-url", strings.NewReader(string(body)))
		r.Header.Set("Authorization", "Bearer "+bearer)
		w := httptest.NewRecorder()
		apiHandler.ServeHTTP(w, r)
		var out api.ObjectSignedRequest
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatal("issue URL", w.Code, w.Body.String())
		}
		if !strings.HasPrefix(out.URL, public.URL+"/assets/") || strings.Contains(w.Body.String(), journalNativeKMSKey) || strings.Contains(w.Body.String(), "physical") {
			t.Fatal("native identity escaped")
		}
		return out
	}
	send := func(out api.ObjectSignedRequest, mutate func(*http.Request)) signedURLResponse {
		t.Helper()
		var body io.Reader
		if out.Method == http.MethodPut {
			body = strings.NewReader("abc")
		}
		r, e := http.NewRequest(out.Method, out.URL, body)
		if e != nil {
			t.Fatal(e)
		}
		for name, value := range out.Headers {
			r.Header.Set(name, value)
		}
		if mutate != nil {
			mutate(r)
		}
		response, e := public.Client().Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer func() { _ = response.Body.Close() }()
		data, e := io.ReadAll(response.Body)
		if e != nil {
			t.Fatal(e)
		}
		return signedURLResponse{StatusCode: response.StatusCode, Header: response.Header.Clone(), Data: string(data)}
	}
	size := int64(3)
	request := api.ObjectSignRequest{Method: "PUT", Key: "file +%ü", SizeBytes: &size, ContentType: "text/html", Metadata: map[string]string{"Color": "blue"}, Encryption: &api.ObjectEncryption{Algorithm: "aws:kms", KeyID: backend.Encryption.Keys[0].Reference}}
	put := issue(request)
	if put.UploadID == "" {
		t.Fatal("PUT receipt missing")
	}
	bad := send(put, func(r *http.Request) { r.Header.Set("X-Amz-Meta-Extra", "unsigned") })
	if bad.StatusCode != 403 {
		t.Fatal("unsigned metadata accepted", bad.StatusCode)
	}
	response := send(put, nil)
	if response.StatusCode != 200 || response.Header.Get("X-Amz-Server-Side-Encryption-Aws-Kms-Key-Id") != request.Encryption.KeyID {
		t.Fatal("owned upload acknowledgment", response.StatusCode, response.Header)
	}
	for range 2 {
		if response = send(put, nil); response.StatusCode != 200 || response.Header.Get("ETag") != `"signed-url"` {
			t.Fatal("settled URL replay", response.StatusCode)
		}
	}
	get := issue(api.ObjectSignRequest{Method: "GET", Key: request.Key})
	response = send(get, nil)
	if response.StatusCode != 200 || response.Data != "abc" || response.Header.Get("X-Amz-Server-Side-Encryption-Aws-Kms-Key-Id") != request.Encryption.KeyID {
		t.Fatal("branded encrypted read", response.StatusCode, response.Data)
	}
	if strings.Contains(fmt.Sprint(response.Header), journalNativeKMSKey) {
		t.Fatal("read exposed native key")
	}
	if response.Header.Get("Content-Disposition") != "attachment" || response.Header.Get("Content-Type") != "application/octet-stream" {
		t.Fatal("control-issued GET lost download policy")
	}
	if response = send(get, func(r *http.Request) { r.Method = http.MethodHead }); response.StatusCode != http.StatusForbidden {
		t.Fatal("GET URL permitted a different method", response.StatusCode)
	}
	head := issue(api.ObjectSignRequest{Method: "HEAD", Key: request.Key})
	response = send(head, nil)
	if response.StatusCode != http.StatusOK || response.Data != "" || response.Header.Get("Content-Length") != "3" || response.Header.Get("Content-Type") != "text/html" || response.Header.Get("X-Amz-Server-Side-Encryption-Aws-Kms-Key-Id") != request.Encryption.KeyID || strings.Contains(fmt.Sprint(response.Header), journalNativeKMSKey) {
		t.Fatal("branded encrypted HEAD", response.StatusCode, response.Header)
	}
	// A lost native ACK retains the single attempt; retries never rewrite.
	native.mu.Lock()
	native.lost = true
	native.mu.Unlock()
	request.Key = "lost"
	lost := issue(request)
	if response = send(lost, nil); response.StatusCode != 503 {
		t.Fatal("lost acknowledgment accepted", response.StatusCode)
	}
	if response = send(lost, nil); response.StatusCode != 409 {
		t.Fatal("uncertain URL replayed", response.StatusCode)
	}
	u, err := url.Parse(lost.URL)
	if err != nil {
		t.Fatal(err)
	}
	access := strings.Split(u.Query().Get("X-Amz-Credential"), "/")[0]
	c, _, err := st.(state.ObjectS3CredentialStore).ResolveObjectS3Credential(t.Context(), access)
	if err != nil {
		t.Fatal(err)
	}
	native.mu.Lock()
	native.disabled = true
	native.mu.Unlock()
	if pool != nil {
		if _, err = pool.Exec(t.Context(), `UPDATE object_upload_completions SET recovery_retry_at=now()-interval '1 second' WHERE id=$1`, lost.UploadID); err != nil {
			t.Fatal(err)
		}
		s.store = state.NewPgStore(pool)
	} else {
		st.(*state.MemStore).SetClockForTest(func() time.Time { return time.Now().Add(2 * time.Minute) })
	}
	if err = s.reconcileObjectUploads(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	done, err := s.store.(state.ObjectTrackedUploadStore).GetObjectUploadReceipt(t.Context(), acct.ID, b.AppID, "", c.ID, lost.UploadID)
	if err != nil || done.Status != "completed" || done.ETag != `"signed-url"` {
		t.Fatal("URL recovery", done, err)
	}
	if response = send(lost, nil); response.StatusCode != 200 {
		t.Fatal("recovered URL replay", response.StatusCode)
	}
	// Change the grant while the authenticated body is being staged. The
	// gateway must stop before another native KMS probe or PUT.
	scoped, hash, _ := api.GenerateAPIKey()
	issuer, err := st.CreateAPIKey(t.Context(), acct.ID, hash, "URL writer", []string{api.ScopeStorageWrite})
	if err != nil {
		t.Fatal(err)
	}
	accessStore := st.(state.ObjectBucketAccessStore)
	if _, err = accessStore.SetObjectBucketAccessGrant(t.Context(), acct.ID, b.ID, issuer.ID, state.ObjectBucketPermissionWrite); err != nil {
		t.Fatal(err)
	}
	bearer = scoped
	request.Key = "revoked-during-staging"
	revoked := issue(request)
	body := &signedURLBody{reader: strings.NewReader("abc")}
	r := httptest.NewRequest(http.MethodPut, revoked.URL, body)
	r.ContentLength = 3
	for name, value := range revoked.Headers {
		r.Header.Set(name, value)
	}
	body.before = func() {
		if e := accessStore.DeleteObjectBucketAccessGrant(r.Context(), acct.ID, b.ID, issuer.ID); e != nil {
			t.Error(e)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatal("revoked staged URL reached provider", w.Code, w.Body.String())
	}
	native.mu.Lock()
	defer native.mu.Unlock()
	if native.writes != 2 || native.keys != 2 {
		t.Fatal("URL replay or recovery repeated a native mutation", native.writes, native.keys)
	}
	if got := put.Headers["Content-Length"]; got != strconv.FormatInt(size, 10) {
		t.Fatal("missing byte binding", got)
	}
}
