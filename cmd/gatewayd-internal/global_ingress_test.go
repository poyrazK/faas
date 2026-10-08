package main

// adr: 784
import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/globaldirectory"
	"github.com/onebox-faas/faas/pkg/runtimequota"
)

func TestGlobalIngressWiringRequiresUnboundScopeAndVerifiedFeed(t *testing.T) {
	ctx := context.Background()
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	h, stop, err := configureGlobalIngress(ctx, &Config{}, "", nil, next)
	if err != nil {
		t.Fatal(err)
	}
	stop()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "https://local.example.com/", nil))
	if w.Code != http.StatusTeapot {
		t.Fatal("disabled ingress changed routing")
	}
	dir := t.TempDir()
	cfg := &Config{AppsDomain: "local.example.com", GlobalIngressDirectoryID: uuid.NewString(), GlobalIngressAppsDomain: "global.example.com", GlobalIngressManifestPath: filepath.Join(dir, "routes.json"), GlobalIngressTrustBundlePath: filepath.Join(dir, "trust.json")}
	if _, _, err := configureGlobalIngress(ctx, cfg, cfg.AppsDomain, nil, next); err == nil {
		t.Fatal("missing database scope accepted")
	}
	if _, _, err := configureGlobalIngress(ctx, cfg, cfg.AppsDomain, func(context.Context) error { return globaldirectory.ErrBoundCell }, next); !errors.Is(err, globaldirectory.ErrBoundCell) {
		t.Fatal("bound regional cell accepted", err)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtimequota.WriteTrustBundle(cfg.GlobalIngressTrustBundlePath, []runtimequota.Anchor{{Role: runtimequota.Directory, IssuerID: cfg.GlobalIngressDirectoryID, Public: pub}}); err != nil {
		t.Fatal(err)
	}
	signer, err := runtimequota.NewSigner(cfg.GlobalIngressDirectoryID, priv)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UnixMicro()
	snapshot := runtimequota.IngressSnapshot{DirectoryID: cfg.GlobalIngressDirectoryID, Generation: 1, IssuedAtMicros: now, ExpiresAtMicros: now + api.GlobalIngressLeaseDuration.Microseconds(), RoutingPolicy: api.GlobalRoutingPolicyAllSelected}
	e, err := signer.SignIngressSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.GlobalIngressManifestPath, raw, 0o640); err != nil {
		t.Fatal(err)
	}
	var checked bool
	h, stop, err = configureGlobalIngress(ctx, cfg, cfg.AppsDomain, func(context.Context) error { checked = true; return nil }, next)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if !checked {
		t.Fatal("database scope was not inspected")
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "https://"+uuid.NewString()+".global.example.com/v1/invocations:dispatch_batch", nil))
	if w.Code != http.StatusNotFound {
		t.Fatal("global app escaped to private mux", w.Code)
	}
	if err := os.WriteFile(cfg.GlobalIngressManifestPath, []byte("invalid"), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, _, err := configureGlobalIngress(ctx, cfg, cfg.AppsDomain, func(context.Context) error { return nil }, next); err == nil {
		t.Fatal("startup accepted unverified feed")
	}
}

func TestGlobalIngressTOMLRejectsPartialConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.toml")
	if err := os.WriteFile(path, []byte("global_ingress_apps_domain = \"global.example.com\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("partial ingress config accepted")
	}
}

func TestGlobalIngressRejectsEffectiveAppsDomainOverlap(t *testing.T) {
	cfg := &Config{AppsDomain: "local.example.com", GlobalIngressDirectoryID: uuid.NewString(), GlobalIngressAppsDomain: "global.example.com", GlobalIngressManifestPath: "/run/faas/routes.json", GlobalIngressTrustBundlePath: "/etc/faas/trust.json"}
	checked := false
	_, _, err := configureGlobalIngress(context.Background(), cfg, "global.example.com", func(context.Context) error { checked = true; return nil }, http.NotFoundHandler())
	if err == nil || checked {
		t.Fatal("effective domain overlap reached database scope or handler installation", err)
	}
}
