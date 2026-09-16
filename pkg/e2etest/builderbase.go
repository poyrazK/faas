package e2etest

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// StagedBuilderBasePath returns the ext4 imaged stages the builder base to on
// this host, mirroring cmd/imaged's builderBasePathFromEnv.
func StagedBuilderBasePath() string {
	if v := strings.TrimSpace(os.Getenv("FAAS_BUILDER_BASE_PATH")); v != "" {
		return v
	}
	root := os.Getenv("FAAS_STORAGE_ROOT")
	if root == "" {
		root = "/srv/fc"
	}
	return filepath.Join(root, "base", "runner-builder-"+runtime.GOARCH+".ext4")
}

// HasRealBuilderBase reports whether imaged can obtain a real builder base on
// this host, by either route it supports.
//
// A local-backend host keeps the base as a file on disk. An OCI-backend host
// keeps NO file under /srv/fc/base at all — imaged resolves the production ref
// through the registry and the read-through blob cache, exactly as
// run-native-e2e.sh's own pre-flight documents when it skips the file check
// for FAAS_STORAGE_BACKEND=oci.
//
// Checking only for the file therefore reports "no base" on precisely the host
// that has one. faas-acceptance-1 runs the OCI backend, so the first version of
// this check was false there and the stub override still applied, leaving the
// 14 validate-base-ext4 failures in place.
func HasRealBuilderBase() bool {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("FAAS_STORAGE_BACKEND")), "oci") {
		return true
	}
	info, err := os.Stat(StagedBuilderBasePath())
	return err == nil && info.Mode().IsRegular() && info.Size() > 0
}

// OverrideBuilderBase points imaged at stubRef — but only on a host with no
// builder base of its own.
//
// The override exists so Lima and credential-less CI can avoid the production
// ghcr.io base, which 403s for anonymous pulls. Ten metal tests applied it
// unconditionally, which breaks any host that DOES have a real base: imaged
// validates the staged ext4's contents and a stub cannot satisfy them —
//
//	imaged: validate base ext4 "base/runner-builder-amd64.ext4": required path
//	/usr/local/bin/faas-guest-init missing from ...: File not found by ext2_lookup
//
// — so imaged exits at boot and every deployment in those tests times out in
// `building`. On faas-acceptance-1 that was 14 failures across the deploy,
// wake, streaming and security phases. TestBuildMetal sets no override, uses
// the node's real base, and built fine throughout; that contrast is what
// identified the override as the cause rather than the base.
//
// A stub cannot be made to satisfy the check either: the required paths are
// railpack, buildctl, runc and guest-init, and source-deploy tests really do
// boot a builder microVM from this ext4. Prefer the host's real base whenever
// there is one.
func OverrideBuilderBase(t *testing.T, stubRef string) {
	t.Helper()
	if HasRealBuilderBase() {
		t.Logf("e2etest: host can resolve a real builder base (backend=%q, path=%s); "+
			"not overriding it with the stub registry",
			envOrLocalBackend(), StagedBuilderBasePath())
		return
	}
	t.Setenv("FAAS_TEST_BUILDER_BASE_REF", stubRef)
}

func envOrLocalBackend() string {
	if v := strings.TrimSpace(os.Getenv("FAAS_STORAGE_BACKEND")); v != "" {
		return v
	}
	return "local"
}

// OverrideDeployBase points imaged's runtime ("deploy") base at stubRef — but
// only on a host that cannot resolve a real one.
//
// Exactly the same defect as OverrideBuilderBase, one variable over, and it
// only became visible once the builder-base override stopped firing first.
// imaged validates every base it stages, and the runtime base has its own
// required paths:
//
//	imaged: reconcile assigned minimal base: imaged: stage runtime base
//	(.../onebox-faas/deploy-base:latest → base/base-amd64.ext4): imaged:
//	validate base ext4 "base/base-amd64.ext4": required path /bin/busybox
//	missing from ...: File not found by ext2_lookup
//
// The stub deploy-base is a single synthetic layer and has no /bin/busybox,
// /sbin/init, /bin/sh or /etc/passwd, so imaged exits at boot and every
// deployment in these tests times out in `building` — the same 14 failures,
// re-attributed from the builder base to the runtime base.
func OverrideDeployBase(t *testing.T, stubRef string) {
	t.Helper()
	if HasRealBuilderBase() {
		t.Logf("e2etest: host can resolve real bases (backend=%q); not overriding the runtime base",
			envOrLocalBackend())
		return
	}
	t.Setenv("FAAS_TEST_DEPLOY_BASE_REF", stubRef)
}
