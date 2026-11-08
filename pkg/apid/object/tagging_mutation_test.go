package object

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestTaggingMutationAdmissionAndCustody(t *testing.T) {
	storageControlStores(t, func(t *testing.T, st state.Store) {
		for _, tc := range []struct {
			name, key, body, version    string
			exhausted                   bool
			nativeStatus, status, calls int
			writers                     int64
		}{
			{name: "invalid key", body: `{"tags":{"team":"storage"}}`, status: 400},
			{name: "invalid tags", key: "key", body: `{"tags":{"":"value"}}`, status: 400},
			{name: "unknown version", key: "key", version: uuid.NewString(), body: `{"tags":{}}`, status: 404},
			{name: "request budget", key: "key", body: `{"tags":{}}`, exhausted: true, status: 402},
			{name: "acknowledged", key: "key", body: `{"tags":{}}`, nativeStatus: 200, status: 200, calls: 1},
			{name: "native rejection", key: "key", body: `{"tags":{}}`, nativeStatus: 404, status: 404, calls: 1},
			{name: "invalid acknowledgment", key: "key", body: `{"tags":{}}`, nativeStatus: 204, status: 503, calls: 1, writers: 1},
			{name: "uncertain native write", key: "key", body: `{"tags":{}}`, nativeStatus: 503, status: 503, calls: 1, writers: 1},
		} {
			t.Run(tc.name, func(t *testing.T) {
				calls := 0
				native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					calls++
					w.WriteHeader(tc.nativeStatus)
				}))
				t.Cleanup(native.Close)
				svc, _, acct, app, b := newStorageControlFixture(t, st, native.URL)
				if tc.exhausted {
					if err := svc.CustomerObjectRequestRecorder(b)(t.Context()); err != nil {
						t.Fatal(err)
					}
				}
				r := encryptionControlRequest(acct, app, b, http.MethodPut, tc.body)
				q := url.Values{"key": {tc.key}}
				if tc.version != "" {
					q.Set("version_id", tc.version)
				}
				r.URL.RawQuery = q.Encode()
				w := httptest.NewRecorder()
				svc.ObjectBucketTags(w, r, acct)
				if w.Code != tc.status || calls != tc.calls {
					t.Fatal("unexpected response or native dispatch", w.Code, strings.TrimSpace(w.Body.String()), calls)
				}
				fence, err := st.(state.ObjectBucketWriteFenceStore).AcquireObjectBucketWriteFence(t.Context(), b, uuid.NewString())
				if err != nil || fence.Requests != tc.writers {
					t.Fatal("incorrect capture barrier", fence, err)
				}
			})
		}
	})
}
