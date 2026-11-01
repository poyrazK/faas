package s3gateway

import (
	"crypto/md5" // #nosec G501 -- S3 Content-MD5 fixture.
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func signedBulkDelete(t *testing.T, body string) *http.Request {
	t.Helper()
	payload := []byte(body)
	sum := sha256.Sum256(payload)
	r := signedGatewayRequest(t, http.MethodPost, "https://s3.gregale.dev/assets?delete=", payload, hex.EncodeToString(sum[:]))
	md5sum := md5.Sum(payload) // #nosec G401 -- S3 Content-MD5 fixture.
	r.Header.Set("Content-MD5", base64.StdEncoding.EncodeToString(md5sum[:]))
	return r
}

// Bug hunt 33: the restore fence was folded into the kill switch, which
// leaves cleanup deletes open, so a key the restore un-revoked could still
// delete objects, and permanently delete versions through a bulk delete.
func TestGatewayRestoreFenceRefusesEveryRequest(t *testing.T) {
	h, _, p := newGatewayTestHandler(t, state.ObjectBucketPermissionReadWrite, func(*http.Request) (*http.Response, error) {
		t.Fatal("a fenced gateway reached the provider")
		return nil, nil
	})
	h.enabled = func() bool { return false }
	h.fenced = func() bool { return true }
	for name, r := range map[string]*http.Request{
		"delete object":       signedGatewayRequest(t, http.MethodDelete, "https://s3.gregale.dev/assets/key", nil, "UNSIGNED-PAYLOAD"),
		"bulk delete":         signedBulkDelete(t, "<Delete><Object><Key>key</Key></Object></Delete>"),
		"bulk delete version": signedBulkDelete(t, "<Delete><Object><Key>key</Key><VersionId>1b4e28ba-2fa1-41d2-883f-0016d3cca427</VersionId></Object></Delete>"),
		"remove lifecycle":    signedGatewayRequest(t, http.MethodDelete, "https://s3.gregale.dev/assets?lifecycle=", nil, "UNSIGNED-PAYLOAD"),
		"read object":         signedGatewayRequest(t, http.MethodGet, "https://s3.gregale.dev/assets/key", nil, "UNSIGNED-PAYLOAD"),
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("%s while fenced = %d %s, want 503", name, w.Code, w.Body.String())
		}
	}
	if len(p.deleted) != 0 {
		t.Fatalf("fenced gateway deleted %v", p.deleted)
	}
}

// The kill switch keeps cleanup deletes, but a bulk delete that names a
// version is refused like DELETE ?versionId.
func TestGatewayKillSwitchRefusesBulkVersionDeletes(t *testing.T) {
	h, _, p := newGatewayTestHandler(t, state.ObjectBucketPermissionReadWrite, func(*http.Request) (*http.Response, error) {
		t.Fatal("DeleteObjects must use the provider abstraction")
		return nil, nil
	})
	h.enabled = func() bool { return false }
	w := httptest.NewRecorder()
	h.ServeHTTP(w, signedBulkDelete(t, "<Delete><Object><Key>key</Key><VersionId>1b4e28ba-2fa1-41d2-883f-0016d3cca427</VersionId></Object></Delete>"))
	if w.Code != http.StatusServiceUnavailable || len(p.deleted) != 0 {
		t.Fatalf("bulk version delete while disabled = %d, deleted %v; want 503 and nothing deleted", w.Code, p.deleted)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, signedBulkDelete(t, "<Delete><Object><Key>key</Key></Object></Delete>"))
	if w.Code != http.StatusOK || len(p.deleted) != 1 {
		t.Fatalf("bulk cleanup delete while disabled = %d %s, deleted %v; want it to run", w.Code, w.Body.String(), p.deleted)
	}
}
