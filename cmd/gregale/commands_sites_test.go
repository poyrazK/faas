package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestStaticHostingManifestRejectsLinks(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>ok</html>"), 0600); err != nil {
		t.Fatal(err)
	}
	manifest, err := staticHostingManifest(dir, true)
	if err != nil || len(manifest.Files) != 1 || !manifest.SPAFallback {
		t.Fatalf("manifest %+v %v", manifest, err)
	}
	if err := os.Symlink("index.html", filepath.Join(dir, "linked.html")); err != nil {
		t.Fatal(err)
	}
	if _, err := staticHostingManifest(dir, false); err == nil {
		t.Fatal("accepted symlink")
	}
}

func TestHostingUploadDoesNotForwardPlatformCredentialsOrRedirect(t *testing.T) {
	var redirected bool
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected = true; w.WriteHeader(200) }))
	defer target.Close()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Error("platform credentials reached object storage")
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != "site" || r.ContentLength != 4 {
			t.Errorf("upload body %q, length %d", body, r.ContentLength)
		}
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "index.html")
	if err := os.WriteFile(path, []byte("site"), 0600); err != nil {
		t.Fatal(err)
	}
	client := server.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	err := uploadHostingFile(context.Background(), client, path, api.HostingFile{Path: "index.html", Size: 4, ContentType: "text/html"}, api.HostingUpload{URL: server.URL + "?secret=must-not-print"})
	if err == nil || redirected || strings.Contains(err.Error(), "must-not-print") {
		t.Fatalf("redirect or secret exposure: %v", err)
	}
}

func TestSitesCommandDefaultOff(t *testing.T) {
	t.Setenv("FAAS_FRONTEND_HOSTING_ENABLED", "")
	if status := cmdSites([]string{"list"}); status == 0 {
		t.Fatal("internal CLI enabled by default")
	}
}
