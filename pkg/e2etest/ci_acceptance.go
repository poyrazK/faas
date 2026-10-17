package e2etest

import (
	"os"
	"path/filepath"
	"testing"
)

// Native CI keeps its daemon resource journals and images after failed drain.
// Ordinary e2e tests keep the existing t.TempDir lifecycle. This is exclusively
// a test harness option; no production unit supplies either setting.
func harnessTempDir(t *testing.T) string {
	t.Helper()
	if os.Getenv("GREGALE_CI_NATIVE_ACCEPTANCE") != "1" {
		return t.TempDir()
	}
	root := os.Getenv("GREGALE_CI_ACCEPTANCE_EVIDENCE")
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || !filepath.IsAbs(root) || info.Mode().Perm()&0o077 != 0 {
		t.Fatal("native CI requires a private, absolute retained evidence directory")
	}
	dir, err := os.MkdirTemp(root, "harness-")
	if err != nil {
		t.Fatal("create retained native CI harness directory")
	}
	return dir
}

func nativeGuestDNS(which Which) bool {
	return which&VMMD != 0 && os.Getenv("FAAS_TEST_KERNEL") != "" &&
		(which&Builderd != 0 || os.Getenv("GREGALE_CI_NATIVE_ACCEPTANCE") == "1")
}
