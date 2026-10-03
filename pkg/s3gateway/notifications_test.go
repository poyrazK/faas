package s3gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 410
func TestGatewayNotificationsValidationAndPermissions(t *testing.T) {
	for _, permission := range []string{state.ObjectBucketPermissionReadWrite, state.ObjectBucketPermissionRead, state.ObjectBucketPermissionWrite} {
		t.Run(permission, func(t *testing.T) {
			st := state.NewMemStore()
			f := newMultipartCopyIntegrationWithProvider(t, st, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Error("intent contacted provider", r.URL)
				w.WriteHeader(500)
			}), permission)
			f.handler.host = "s3.gregale.dev"
			f.handler.now = func() time.Time { return time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC) }
			arn := "arn:gregale:lambda:" + f.bucket.Region + ":" + uuid.MustParse(f.bucket.AccountID).String() + ":function:" + uuid.MustParse(f.bucket.AppID).String()
			body := `<NotificationConfiguration><CloudFunctionConfiguration><Id>images</Id><CloudFunction>` + arn + `</CloudFunction><Event>s3:ObjectCreated:Put</Event></CloudFunctionConfiguration></NotificationConfiguration>`
			send := func(method, path, body string, badDigest bool) *httptest.ResponseRecorder {
				sum := sha256.Sum256([]byte(body))
				if badDigest {
					sum = sha256.Sum256([]byte("tampered"))
				}
				r := signedGatewayRequest(t, method, "https://s3.gregale.dev/"+path, []byte(body), hex.EncodeToString(sum[:]))
				w := httptest.NewRecorder()
				f.handler.ServeHTTP(w, r)
				return w
			}
			if permission == state.ObjectBucketPermissionRead {
				if w := send("PUT", "assets?notification", body, false); w.Code != 403 {
					t.Fatal(w.Code, w.Body.String())
				}
				if w := send("GET", "assets?notification", "", false); w.Code != 200 {
					t.Fatal(w.Code, w.Body.String())
				}
				return
			}
			if w := send("PUT", "assets?notification", body, true); w.Code != 400 {
				t.Fatal(w.Code, w.Body.String())
			}
			for _, path := range []string{"assets?notification&notification", "assets?notification=other", "assets?notification&tagging", "assets/key?notification"} {
				if w := send("PUT", path, body, false); w.Code != 400 {
					t.Fatal(path, w.Code, w.Body.String())
				}
			}
			if w := send("PUT", "assets?notification", `<NotificationConfiguration><TopicConfiguration/></NotificationConfiguration>`, false); w.Code != 501 {
				t.Fatal(w.Code, w.Body.String())
			}
			if w := send("PUT", "other?notification", body, false); w.Code != 404 {
				t.Fatal(w.Code, w.Body.String())
			}
			if w := send("PUT", "assets?notification&x-id=PutBucketNotificationConfiguration", body, false); w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			f.handler.enabled = func() bool { return false }
			f.handler.registry = &objectstorage.Registry{}
			want := 200
			if permission == state.ObjectBucketPermissionWrite {
				want = 403
			}
			if w := send("GET", "assets?notification", "", false); w.Code != want || want == 200 && !strings.Contains(w.Body.String(), arn) {
				t.Fatal(w.Code, w.Body.String())
			}
			if w := send("PUT", "assets?notification", body, false); w.Code != 503 {
				t.Fatal(w.Code, w.Body.String())
			}
			if w := send("PUT", "assets?notification", `<NotificationConfiguration/>`, false); w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			credentials, err := st.ListObjectS3Credentials(t.Context(), f.bucket.AccountID, f.bucket.ID)
			if err != nil || len(credentials) != 1 {
				t.Fatal(credentials, err)
			}
			if err = st.RevokeObjectS3Credential(t.Context(), f.bucket.AccountID, f.bucket.ID, credentials[0].ID); err != nil {
				t.Fatal(err)
			}
			if w := send("GET", "assets?notification", "", false); w.Code != 403 {
				t.Fatal("revoked credential", w.Code, w.Body.String())
			}
		})
	}
}
