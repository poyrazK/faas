package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSitesServeFlagsAndValidationBeforeNetworking(t *testing.T) {
	// adr: 202610171311 — local preview needs no API/token and cannot expose invalid output.
	calls := 0
	api := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer api.Close()
	t.Setenv("FAAS_API", api.URL)
	t.Setenv("FAAS_TOKEN", "")
	t.Setenv("FAAS_FRONTEND_HOSTING_ENABLED", "1")
	t.Setenv("FAAS_FRONTEND_HOSTING_LOCAL_PREVIEW_ENABLED", "")
	if cmdSites([]string{"serve", "--path", t.TempDir()}) == 0 {
		t.Fatal("default off")
	}
	t.Setenv("FAAS_FRONTEND_HOSTING_LOCAL_PREVIEW_ENABLED", "1")
	for _, args := range [][]string{{"serve", "--path", t.TempDir()}, {"serve", "--port", "-1"}, {"serve", "--port", "65536"}, {"serve", "--host", "0.0.0.0"}, {"serve", "extra"}} {
		if cmdSites(args) == 0 {
			t.Fatal("invalid options accepted", args)
		}
	}
	if calls != 0 {
		t.Fatal("local validation contacted API", calls)
	}
}
func TestSitesServeImmutableSnapshotAndLifecycle(t *testing.T) {
	// adr: 202610171311 — files are frozen before listening and the listener closes on cancellation.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("ORIGINAL"), 0600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := hostingLocalSnapshot(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "index.html"), []byte("CHANGED"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ready := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		done <- serveHostingLocal(ctx, snapshot, 0, func(address string) error { ready <- address; return nil })
	}()
	var address string
	select {
	case address = <-ready:
	case <-time.After(10 * time.Second):
		t.Fatal("preview not ready")
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil || host != "127.0.0.1" {
		t.Fatal("unsafe bind", address, err)
	}
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	response, err := client.Get("http://" + address + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || response.StatusCode != 200 || string(body) != "ORIGINAL" || response.Header.Get("X-Gregale-Local-Preview") != "true" {
		t.Fatal(response.StatusCode, string(body), err)
	}
	request, err := http.NewRequestWithContext(t.Context(), "GET", "http://"+address+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = "evil.example"
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatal("foreign host accepted")
	}
	cancel()
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("preview did not stop")
	}
	connection, err := net.DialTimeout("tcp4", address, time.Second)
	if err == nil {
		_ = connection.Close()
		t.Fatal("preview leaked listener")
	}
	// Output errors must close an already-bound listener as well.
	if err = serveHostingLocal(t.Context(), snapshot, 0, func(string) error { return errors.New("writer failed") }); err == nil {
		t.Fatal("output error swallowed")
	}
}
func TestSitesServeRejectsSymlinksConfigAndUnvalidatedMetadata(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.html"), []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.html"), filepath.Join(dir, "index.html")); err != nil {
		t.Skip(err)
	}
	if _, err := hostingLocalSnapshot(dir, false); err == nil {
		t.Fatal("symlink admitted")
	}
	if err := os.Remove(filepath.Join(dir, "index.html")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("INDEX"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "outside")); err != nil {
		t.Fatal(err)
	}
	if _, err := hostingLocalSnapshot(dir, false); err == nil {
		t.Fatal("directory symlink admitted")
	}
	if err := os.Remove(filepath.Join(dir, "outside")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gregale-hosting.json"), []byte(`{"version":1,"headers":[{"path":"/*","values":{"x-frame-options":"DENY"}}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAAS_FRONTEND_HOSTING_CONFIG_ENABLED", "")
	if _, err := hostingLocalSnapshot(dir, false); err == nil {
		t.Fatal("config flag bypassed")
	}
	t.Setenv("FAAS_FRONTEND_HOSTING_CONFIG_ENABLED", "1")
	snapshot, err := hostingLocalSnapshot(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := snapshot.Handler("127.0.0.1:4173")
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "http://127.0.0.1:4173/gregale-hosting.json", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 404 || strings.Contains(w.Body.String(), "DENY") {
		t.Fatal("config file exposed", w.Code, w.Body)
	}
}

func TestSitesServeReadsRemainInTheBoundRoot(t *testing.T) {
	// adr: 202610171311 — replacing the path after opening cannot swap snapshot policy or bytes.
	parent := t.TempDir()
	dir := filepath.Join(parent, "build")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("ORIGINAL"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gregale-hosting.json"), []byte(`{"version":1,"headers":[{"path":"/*","values":{"x-frame-options":"DENY"}}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	if err = os.Rename(dir, filepath.Join(parent, "retained")); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "index.html"), []byte("REPLACED"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "gregale-hosting.json"), []byte(`{"version":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAAS_FRONTEND_HOSTING_CONFIG_ENABLED", "1")
	config, err := hostingLocalConfig(root)
	if err != nil || len(config.Headers) != 1 || config.Headers[0].Values["x-frame-options"] != "DENY" {
		t.Fatal(config, err)
	}
	data, err := hostingLocalRead(root, "index.html", 8)
	if err != nil || string(data) != "ORIGINAL" {
		t.Fatal(string(data), err)
	}
	if _, err = hostingLocalRead(root, "index.html", 1); err == nil {
		t.Fatal("snapshot read bound ignored")
	}
}
