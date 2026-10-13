// adr: 151
package s3gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestParsePathTreatsTrailingSlashAsBucket(t *testing.T) {
	cases := []struct {
		path              string
		bucket, key       string
		hasBucket, hasKey bool
		wantErr           bool
	}{
		{path: "/", hasBucket: false},
		{path: "/assets", bucket: "assets", hasBucket: true},
		{path: "/assets/", bucket: "assets", hasBucket: true},
		{path: "/assets/photos/a.jpg", bucket: "assets", key: "photos/a.jpg", hasBucket: true, hasKey: true},
		{path: "/assets/photos/", bucket: "assets", key: "photos/", hasBucket: true, hasKey: true},
		{path: "//k", wantErr: true},
	}
	for _, tc := range cases {
		bucket, key, hasBucket, hasKey, err := parsePath(tc.path)
		if (err != nil) != tc.wantErr || bucket != tc.bucket || key != tc.key || hasBucket != tc.hasBucket || hasKey != tc.hasKey {
			t.Errorf("parsePath(%q) = %q %q %v %v err=%v", tc.path, bucket, key, hasBucket, hasKey, err)
		}
	}
}

// TestGatewayListsWithTrailingSlashBucketPath pins H9-132: AWS SDK for
// JavaScript v3 sends ListObjectsV2 as GET /bucket/?list-type=2 in path style,
// which the gateway rejected as 400 InvalidURI.
func TestGatewayListsWithTrailingSlashBucketPath(t *testing.T) {
	handler, _, provider := newGatewayTestHandler(t, state.ObjectBucketPermissionRead, func(*http.Request) (*http.Response, error) {
		t.Fatal("listing must use provider capability")
		return nil, nil
	})
	provider.objects = objectstorage.ObjectPage{
		Items: []objectstorage.Object{{Key: "r1/a.bin", Size: 4, LastModified: time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)}},
	}
	recorder := httptest.NewRecorder()
	request := signedGatewayRequest(t, http.MethodGet, "https://s3.gregale.dev/assets/?list-type=2&prefix=r1%2F&x-id=ListObjectsV2", nil, "UNSIGNED-PAYLOAD")
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "<Key>r1/a.bin</Key>") {
		t.Fatalf("trailing-slash listing = %d %s", recorder.Code, recorder.Body.String())
	}
}
