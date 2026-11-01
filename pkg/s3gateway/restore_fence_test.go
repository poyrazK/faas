package s3gateway

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261025111015 — after a restore, keys revoked since the restore
// point are live again until the timeline is accepted. A closed fence
// answers every request 503 before it resolves a key, so none of them can
// delete a version or change a bucket setting, whatever s3_enabled says.
func TestGatewayRestoreFenceRefusesEveryRequest(t *testing.T) {
	versionDelete := []byte(`<Delete><Object><Key>key</Key><VersionId>` + uuid.NewString() + `</VersionId></Object></Delete>`)
	for _, tc := range []struct {
		name, method, path string
		body               []byte
		unsigned           bool
	}{
		{name: "delete object", method: http.MethodDelete, path: "/assets/key"},
		{name: "delete versions", method: http.MethodPost, path: "/assets?delete", body: versionDelete},
		{name: "abort multipart", method: http.MethodDelete, path: "/assets/key?uploadId=" + uuid.NewString()},
		{name: "delete tags", method: http.MethodDelete, path: "/assets/key?tagging"},
		{name: "remove encryption", method: http.MethodDelete, path: "/assets?encryption"},
		{name: "read lifecycle", method: http.MethodGet, path: "/assets?lifecycle"},
		{name: "remove lifecycle", method: http.MethodDelete, path: "/assets?lifecycle"},
		{name: "read notifications", method: http.MethodGet, path: "/assets?notification"},
		{name: "replace notifications", method: http.MethodPut, path: "/assets?notification", body: []byte(`<NotificationConfiguration/>`)},
		{name: "write receipt", method: http.MethodGet, path: "/assets/key?gregale-upload-id=" + uuid.NewString()},
		{name: "read retention", method: http.MethodGet, path: "/assets/key?retention"},
		{name: "list objects", method: http.MethodGet, path: "/assets?list-type=2"},
		{name: "read object", method: http.MethodGet, path: "/assets/key"},
		{name: "write object", method: http.MethodPut, path: "/assets/key", body: []byte("data")},
		{name: "cors preflight", method: http.MethodOptions, path: "/assets/key", unsigned: true},
		{name: "upload grant", method: http.MethodPut, path: "/assets/key?" + UploadGrantQueryParameter + "=" + strings.Repeat("a", 43), body: []byte("data"), unsigned: true},
	} {
		for _, killSwitch := range []string{"on", "off"} {
			t.Run(tc.name+"/s3_enabled "+killSwitch, func(t *testing.T) {
				h, store, p := newGatewayTestHandler(t, state.ObjectBucketPermissionReadWrite, func(*http.Request) (*http.Response, error) {
					t.Error("fenced request reached the provider")
					return nil, errors.New("fenced")
				})
				h.uploadGrants = true
				h.enabled = func() bool { return killSwitch == "on" }
				h.fenceOpen = func() bool { return false }
				var r *http.Request
				if tc.unsigned {
					r = httptest.NewRequest(tc.method, "https://s3.gregale.dev"+tc.path, strings.NewReader(string(tc.body)))
					r.Header.Set("Origin", "https://console.example.test")
					r.Header.Set("Access-Control-Request-Method", http.MethodDelete)
				} else {
					r = signedGatewayRequest(t, tc.method, "https://s3.gregale.dev"+tc.path, tc.body, "UNSIGNED-PAYLOAD")
				}
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "<Code>ServiceUnavailable</Code>") {
					t.Fatalf("fenced %s %s = %d %s, want 503", tc.method, tc.path, w.Code, w.Body.String())
				}
				if store.resolves != 0 || len(store.admitted) != 0 || store.touched != 0 || len(p.deleted) != 0 || len(p.abortedUploads) != 0 {
					t.Fatalf("fenced request resolved a key or changed state: resolves=%d admitted=%v deleted=%v aborted=%v", store.resolves, store.admitted, p.deleted, p.abortedUploads)
				}
			})
		}
	}
}

// The kill switch and the restore fence are two states: s3_enabled off still
// lets a key delete (it was made for cleanup), a closed fence does not.
func TestGatewayKillSwitchAndRestoreFenceStayDistinct(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		enabled, fenceOpen     bool
		wantDelete, wantListed int
	}{
		{"serving", true, true, http.StatusNoContent, http.StatusOK},
		{"kill switch", false, true, http.StatusNoContent, http.StatusServiceUnavailable},
		{"restore fence", true, false, http.StatusServiceUnavailable, http.StatusServiceUnavailable},
		{"both", false, false, http.StatusServiceUnavailable, http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, _, p := newGatewayTestHandler(t, state.ObjectBucketPermissionReadWrite, func(*http.Request) (*http.Response, error) {
				t.Error("unexpected provider URL request")
				return nil, errors.New("unexpected")
			})
			h.enabled = func() bool { return tc.enabled }
			h.fenceOpen = func() bool { return tc.fenceOpen }
			w := httptest.NewRecorder()
			h.ServeHTTP(w, signedGatewayRequest(t, http.MethodDelete, "https://s3.gregale.dev/assets/key", nil, "UNSIGNED-PAYLOAD"))
			if w.Code != tc.wantDelete || (tc.wantDelete == http.StatusNoContent) != (len(p.deleted) == 1) {
				t.Fatalf("DELETE = %d %s, deleted %v; want %d", w.Code, w.Body.String(), p.deleted, tc.wantDelete)
			}
			w = httptest.NewRecorder()
			h.ServeHTTP(w, signedGatewayRequest(t, http.MethodGet, "https://s3.gregale.dev/assets?list-type=2", nil, "UNSIGNED-PAYLOAD"))
			if w.Code != tc.wantListed {
				t.Fatalf("list = %d %s; want %d", w.Code, w.Body.String(), tc.wantListed)
			}
		})
	}
}
