package api

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestGlobalAppAttachmentSDKRefusesCredentialRedirect(t *testing.T) {
	var redirected atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/recipient" {
			redirected.Add(1)
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Redirect(w, r, "/recipient", http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	client := NewClient(srv.URL, "directory-key").SetCompletionCache(nil)
	client.HTTPClient().CheckRedirect = func(*http.Request, []*http.Request) error { return nil }
	_, err := client.AttachGlobalAppTarget(t.Context(), "global-shop", "eu-fsn", AttachGlobalAppTargetRequest{RegionalAppSlug: "regional-shop", RegionalAPIKey: "regional-key"})
	if err == nil || redirected.Load() != 0 {
		t.Fatalf("redirect: err=%v recipient requests=%d", err, redirected.Load())
	}
}
