package s3gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 408
func TestGatewayLifecycleValidationAndPermissions(t *testing.T) {
	for _, permission := range []string{state.ObjectBucketPermissionReadWrite, state.ObjectBucketPermissionRead, state.ObjectBucketPermissionWrite} {
		t.Run(permission, func(t *testing.T) {
			st := state.NewMemStore()
			f := newMultipartCopyIntegrationWithProvider(t, st, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Error("configuration contacted provider", r.URL)
				w.WriteHeader(500)
			}), permission)
			f.handler.host = "s3.gregale.dev"
			f.handler.now = func() time.Time { return time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC) }
			body := `<LifecycleConfiguration><Rule><Status>Enabled</Status><Expiration><Days>7</Days></Expiration></Rule></LifecycleConfiguration>`
			send := func(method, query, body string, wrongDigest bool) *httptest.ResponseRecorder {
				t.Helper()
				sum := sha256.Sum256([]byte(body))
				if wrongDigest {
					sum = sha256.Sum256([]byte("tampered"))
				}
				r := signedGatewayRequest(t, method, "https://s3.gregale.dev/assets"+query, []byte(body), hex.EncodeToString(sum[:]))
				w := httptest.NewRecorder()
				f.handler.ServeHTTP(w, r)
				return w
			}
			if permission == state.ObjectBucketPermissionRead {
				for _, method := range []string{"PUT", "DELETE"} {
					if w := send(method, "?lifecycle", body, false); w.Code != 403 {
						t.Fatal(w.Code, w.Body.String())
					}
				}
				if w := send("GET", "?lifecycle", "", false); w.Code != 404 || !strings.Contains(w.Body.String(), "NoSuchLifecycleConfiguration") {
					t.Fatal(w.Code, w.Body.String())
				}
				return
			}
			if w := send("PUT", "?lifecycle", body, true); w.Code != 400 {
				t.Fatal(w.Code, w.Body.String())
			}
			for _, tc := range []struct {
				query, body string
				status      int
			}{
				{"?lifecycle", strings.Replace(body, "<Days>7</Days>", "<Days>7</Days><Days>8</Days>", 1), 400},
				{"?lifecycle", strings.Replace(body, "Expiration", "Transition", -1), 501},
				{"?lifecycle&lifecycle", body, 400}, {"?lifecycle=other", body, 400}, {"?lifecycle&tagging", body, 400},
			} {
				if w := send("PUT", tc.query, tc.body, false); w.Code != tc.status {
					t.Fatal(w.Code, w.Body.String(), tc)
				}
			}
			if p, err := st.GetObjectBucketLifecycle(t.Context(), f.bucket.AccountID, f.bucket.AppID, f.bucket.ID); err != nil || p.Revision != 0 {
				t.Fatal(p, err)
			}
			if w := send("PUT", "?lifecycle&x-id=PutBucketLifecycleConfiguration", body, false); w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			if permission == state.ObjectBucketPermissionWrite {
				if w := send("GET", "?lifecycle", "", false); w.Code != 403 {
					t.Fatal(w.Code, w.Body.String())
				}
			} else {
				f.handler.enabled = func() bool { return false }
				f.handler.registry = &objectstorage.Registry{}
				if w := send("GET", "?lifecycle", "", false); w.Code != 200 || !strings.Contains(w.Body.String(), "<Days>7</Days>") {
					t.Fatal(w.Code, w.Body.String())
				}
			}
			if w := send("DELETE", "?lifecycle", "", false); w.Code != 204 || w.Body.Len() != 0 {
				t.Fatal(w.Code, w.Body.String())
			}
			credentials, err := st.ListObjectS3Credentials(t.Context(), f.bucket.AccountID, f.bucket.ID)
			if err != nil || len(credentials) != 1 {
				t.Fatal(credentials, err)
			}
			if err = st.RevokeObjectS3Credential(t.Context(), f.bucket.AccountID, f.bucket.ID, credentials[0].ID); err != nil {
				t.Fatal(err)
			}
			if w := send("GET", "?lifecycle", "", false); w.Code != 403 {
				t.Fatal("revoked credential read durable policy", w.Code, w.Body.String())
			}
		})
	}
}
