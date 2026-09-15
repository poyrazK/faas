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

// HasStagedBuilderBase reports whether this host already carries a real
// builder base.
func HasStagedBuilderBase() bool {
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
	if HasStagedBuilderBase() {
		t.Logf("e2etest: host has a real builder base at %s; not overriding it with the stub registry",
			StagedBuilderBasePath())
		return
	}
	t.Setenv("FAAS_TEST_BUILDER_BASE_REF", stubRef)
}
