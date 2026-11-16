package secretd

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/oci"
)

// A registry reached over https must not be able to point its bearer realm at
// a cleartext endpoint: the token request carries the app's reusable Basic
// credential (Codex finding, 2026-11-16).
func TestRegistryAuthorizeRefusesCleartextRealmWithCredential(t *testing.T) {
	var hits atomic.Int32
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{"token":"t","expires_in":300}`))
	}))
	defer plain.Close()

	f := newRegistryFetcher(plain.Client(), time.Now)
	challenge := `Bearer realm="` + plain.URL + `/token",service="reg",scope="repository:team/app:pull"`
	_, _, err := f.authorize(t.Context(), challenge, &oci.BasicAuth{Username: "robot", Password: "s3cret"})
	if err == nil {
		t.Fatal("authorize accepted an http bearer realm for a credentialed request")
	}
	if hits.Load() != 0 {
		t.Fatalf("credential was sent to the cleartext realm %d time(s)", hits.Load())
	}

	for _, realm := range []string{"ftp://example.test/token", "/token", "https:///token"} {
		if _, _, err := f.authorize(t.Context(), `Bearer realm="`+realm+`"`, &oci.BasicAuth{Username: "robot", Password: "s3cret"}); err == nil {
			t.Errorf("authorize accepted realm %q", realm)
		}
	}
}
