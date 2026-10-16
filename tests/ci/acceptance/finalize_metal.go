//go:build metal

package acceptance

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/onebox-faas/faas/pkg/fcvm/leakcheck"
)

// Finalize removes only fixture-owned schema/files after the native leak gate.
// Run under the acceptance host lock, after all fixture daemons have stopped.
// It never destroys native resources to make the assertion pass.
func Finalize(ctx context.Context, dsn, dir string) error {
	if os.Getenv(OptInEnv) != "1" || runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || os.Geteuid() != 0 {
		return errors.New("CI acceptance finalization requires the dedicated native host")
	}
	marker, err := os.Lstat("/etc/faas/ci-acceptance-host")
	if err != nil || !marker.Mode().IsRegular() || marker.Mode().Perm()&0o022 != 0 {
		return errors.New("CI acceptance host marker unavailable")
	}
	owner, ok := marker.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != 0 {
		return errors.New("CI acceptance host marker must be root-owned")
	}
	kvm, err := os.Stat("/dev/kvm")
	if err != nil || kvm.Mode()&os.ModeCharDevice == 0 {
		return errors.New("CI acceptance finalization requires native KVM")
	}
	j, err := ReadJournal(dir)
	if err != nil {
		return err
	}
	if !j.Cleanup {
		return errors.New("CI acceptance intent/storage cleanup is incomplete")
	}
	if j.HarnessDir != "" && (filepath.Dir(j.HarnessDir) != dir || !strings.HasPrefix(filepath.Base(j.HarnessDir), "harness-")) {
		return errors.New("CI acceptance native files are outside their owned directory")
	}
	if leaks := leakcheck.Zero(); len(leaks) != 0 {
		return errors.New("CI acceptance native leakcheck failed; recovery authority retained")
	}
	j.NativeLeaks = true
	if err := WriteJournal(dir, j); err != nil {
		return err
	}
	if err := DropSchema(ctx, dsn, j); err != nil {
		return err
	}
	j.SchemaGone = true
	if j.HarnessDir != "" {
		if err := os.RemoveAll(j.HarnessDir); err != nil {
			return errors.New("CI acceptance native file retirement failed")
		}
	}
	for _, name := range []string{"host.age", "host.age.pub", "githubd.sock"} {
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return errors.New("CI acceptance fixture key retirement failed")
		}
	}
	j.FilesGone = true
	return WriteJournal(dir, j)
}
