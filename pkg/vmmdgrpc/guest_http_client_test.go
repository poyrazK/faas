package vmmdgrpc

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGuestHTTPClientReturnsRedirectWithoutFollowing(t *testing.T) {
	var destinationHits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/destination" {
			destinationHits++
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Redirect(w, r, "/destination", http.StatusFound)
	}))
	defer srv.Close()

	resp, err := newGuestHTTPClient(http.DefaultTransport).Get(srv.URL + "/start")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/destination" {
		t.Fatalf("response = %d Location=%q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if destinationHits != 0 {
		t.Fatalf("redirect destination received %d internal requests", destinationHits)
	}
}
