package s3gateway

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsv4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCopySourceVersionParsing(t *testing.T) {
	id := uuid.NewString()
	key := "path /+%世界?versionId=literal#name"
	for _, tc := range []struct {
		value, key, version string
		valid               bool
	}{
		{"assets/" + url.PathEscape(key), key, "", true},
		{"/assets/" + url.PathEscape(key) + "?versionId=" + id, key, id, true},
		{url.PathEscape("assets/"+key) + "?versionId=null", key, "null", true},
		{"assets/key?versionId=" + id + "&versionId=null", "", "", false},
		{"assets/key?versionId=", "", "", false},
		{"assets/key?versionId=" + id + "&extra=1", "", "", false},
		{"assets/key?other=" + id, "", "", false},
		{"assets/key?versionId=%xx", "", "", false},
		{"assets/%xx", "", "", false},
		{"assets/key?versionId=null#fragment", "", "", false},
	} {
		t.Run(tc.value, func(t *testing.T) {
			source, err := parseCopySource(tc.value)
			if (err == nil) != tc.valid || tc.valid && (source.Bucket != "assets" || source.Key != tc.key || source.VersionID != tc.version) {
				t.Fatal(source, err)
			}
		})
	}
}

func TestSelectedCopyRequiresProviderCapability(t *testing.T) {
	h, _, p := newGatewayTestHandler(t, state.ObjectBucketPermissionReadWrite, func(*http.Request) (*http.Response, error) {
		t.Fatal("unsupported version copy reached provider")
		return nil, nil
	})
	r := signedCopyTestRequest(t)
	r.Header.Set("X-Amz-Copy-Source", "assets/source?versionId="+uuid.NewString())
	if err := awsv4.NewSigner(func(o *awsv4.SignerOptions) { o.DisableURIPathEscaping = true }).SignHTTP(t.Context(), aws.Credentials{AccessKeyID: testAccess, SecretAccessKey: testSecret}, r, "UNSIGNED-PAYLOAD", "s3", "us-east-1", h.now()); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNotImplemented || len(p.copyRequests) != 0 {
		t.Fatal(w.Code, w.Body.String(), p.copyRequests)
	}
}
