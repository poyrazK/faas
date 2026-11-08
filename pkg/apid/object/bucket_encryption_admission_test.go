package object

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

func encryptionControlRequest(acct state.Account, app state.App, b state.ObjectBucket, method, body string) *http.Request {
	r := httptest.NewRequest(method, "https://api.test/encryption", strings.NewReader(body))
	r.SetPathValue("slug", app.Slug)
	r.SetPathValue("bucket", b.ID)
	return r.WithContext(authmw.WithPrincipal(r.Context(), acct, nil, nil))
}

// adr: 627
func TestAPIEncryptionRejectsNewIntentAtRequestCeiling(t *testing.T) {
	storageControlStores(t, func(t *testing.T, st state.Store) {
		svc, _, acct, app, b := newStorageControlFixture(t, st, "http://127.0.0.1:1")
		if err := svc.CustomerObjectRequestRecorder(b)(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := svc.CustomerObjectRequestRecorder(b)(t.Context()); !errors.Is(err, state.ErrObjectBudget) {
			t.Fatal("budget control", err)
		}
		w := httptest.NewRecorder()
		svc.PutObjectBucketEncryption(w, encryptionControlRequest(acct, app, b, http.MethodPut, `{"encryption":{"algorithm":"AES256"}}`), acct)
		j, err := st.(state.ObjectBucketEncryptionStore).GetObjectBucketEncryption(t.Context(), acct.ID, app.ID, b.ID)
		if err != nil || w.Code != http.StatusPaymentRequired || j.Revision != 0 || j.State != "ready" {
			t.Fatal("over-budget intent accepted", w.Code, w.Body.String(), j, err)
		}
	})
}

func TestAPIEncryptionAcceptedWorkAndCleanupContinueAtCeiling(t *testing.T) {
	storageControlStores(t, func(t *testing.T, st state.Store) {
		nativeCalls := 0
		native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			nativeCalls++
			if r.Method != http.MethodGet || !r.URL.Query().Has("encryption") {
				t.Error("unexpected native operation", r.Method, r.URL)
			}
			_, _ = io.WriteString(w, `<ServerSideEncryptionConfiguration><Rule><ApplyServerSideEncryptionByDefault><SSEAlgorithm>AES256</SSEAlgorithm></ApplyServerSideEncryptionByDefault></Rule></ServerSideEncryptionConfiguration>`)
		}))
		t.Cleanup(native.Close)
		svc, host, acct, app, b := newStorageControlFixture(t, st, native.URL)
		w := httptest.NewRecorder()
		svc.PutObjectBucketEncryption(w, encryptionControlRequest(acct, app, b, http.MethodPut, `{"encryption":{"algorithm":"AES256"}}`), acct)
		if w.Code != 202 || nativeCalls != 0 {
			t.Fatal("intent was not accepted without native dispatch", w.Code, w.Body.String(), nativeCalls)
		}
		if err := svc.CustomerObjectRequestRecorder(b)(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := svc.ReconcileObjectBucketEncryption(t.Context(), nil); err != nil {
			t.Fatal(err)
		}
		j, err := st.(state.ObjectBucketEncryptionStore).GetObjectBucketEncryption(t.Context(), acct.ID, app.ID, b.ID)
		if err != nil || j.State != "ready" || j.Encryption.Selection.Algorithm != "AES256" || nativeCalls != 1 {
			t.Fatal("accepted work stranded", j, err, nativeCalls)
		}
		host.enabled = false
		for range 2 {
			w = httptest.NewRecorder()
			svc.DeleteObjectBucketEncryption(w, encryptionControlRequest(acct, app, b, http.MethodDelete, ""), acct)
			if w.Code != 202 {
				t.Fatal("cleanup blocked at ceiling", w.Code, w.Body.String())
			}
			if err = svc.ReconcileObjectBucketEncryption(t.Context(), nil); err != nil {
				t.Fatal(err)
			}
		}
		j, err = st.(state.ObjectBucketEncryptionStore).GetObjectBucketEncryption(t.Context(), acct.ID, app.ID, b.ID)
		usage, usageErr := st.(state.ObjectStorageAccountingStore).ObjectUsage(t.Context(), acct.ID, time.Now())
		if err != nil || usageErr != nil || j.State != "ready" || !j.Encryption.Empty() || j.Revision != 2 || nativeCalls != 3 || state.SummarizeObjectUsage(usage, host.registry.Accounting, time.Now()).RequestCount != 4 {
			t.Fatal(j, err, usageErr, nativeCalls, usage)
		}
	})
}
