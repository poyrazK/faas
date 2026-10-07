package s3gateway

import (
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 638
func TestBoundVersionURLRejectsSelectorExpansion(t *testing.T) {
	id := uuid.NewString()
	u := &state.ObjectURLCapability{Request: api.ObjectSignRequest{Method: "GET", Key: "key", VersionID: id}}
	for _, tc := range []struct {
		query string
		want  bool
	}{
		{"versionId=" + id, true},
		{"versionId=" + uuid.NewString(), false}, {"", false}, {"versionId=null", false},
		{"versionId=" + id + "&versionId=" + id, false}, {"versionId=" + id + "&versions=", false},
		{"versionId=" + id + "&uploadId=" + uuid.NewString(), false},
	} {
		r := httptest.NewRequest("GET", "https://public.test/assets/key?"+tc.query, nil)
		q, _ := url.ParseQuery(r.URL.RawQuery)
		q.Set("X-Amz-Algorithm", "AWS4-HMAC-SHA256")
		r.URL.RawQuery = q.Encode()
		if got := boundURLQuery(r, u); got != tc.want {
			t.Fatal(tc, got)
		}
	}
	u.Request.VersionID = ""
	if boundURLQuery(httptest.NewRequest("GET", "https://public.test/assets/key?versionId="+id, nil), u) {
		t.Fatal("current read URL gained historical authority")
	}
}
