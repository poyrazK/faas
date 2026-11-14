package s3gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestHeaderRequestRefusesUnsignedAmzMetadata(t *testing.T) {
	for _, header := range []string{"X-Amz-Tagging", "X-Amz-Meta-Owner"} {
		t.Run(header, func(t *testing.T) {
			contacted := 0
			h, _, p := newGatewayTestHandler(t, state.ObjectBucketPermissionReadWrite, func(r *http.Request) (*http.Response, error) {
				contacted++
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Etag": {`"etag"`}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
			})
			body := []byte("hello")
			digest := sha256.Sum256(body)
			r := signedGatewayRequest(t, http.MethodPut, "https://s3.gregale.dev/assets/file.txt", body, hex.EncodeToString(digest[:]))
			value := "attacker"
			if header == "X-Amz-Tagging" {
				value = "owner=attacker"
			}
			r.Header.Set(header, value)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "AccessDenied") || !strings.Contains(w.Body.String(), "not signed") {
				t.Fatalf("unsigned %s added after signing = %d %s", header, w.Code, w.Body.String())
			}
			if contacted != 0 || len(p.presignRequests) != 0 {
				t.Fatal("unsigned metadata reached the provider")
			}
		})
	}
}
