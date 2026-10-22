//go:build metal

package fcvm

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 192 — production apps carry app env (env.json) as well as the
// resolver, and schedd stamps every wake's env with that wake's placement
// identity. After a vmmd restart, the first restore of a reused capture must
// learn it, and the next restore — a new instance — must skip the mount.
func TestMetalRestoreLearnsReusedCaptureWithAppEnv(t *testing.T) {
	r := newPrefetchMetalRig(t, "", "", 256, "", false)
	capture := newRestoreBreakdownCapture(t)
	defer capture.restore()
	ctx := context.Background()
	wakes := 0
	withEnv := func(req *WakeRequest) {
		wakes++
		req.APIEnvEntries = []APIEnvEntry{{Key: "APP_MODE", Value: "production"}, {Key: "LOG_LEVEL", Value: "info"},
			{Key: api.PlatformInstanceIDEnv, Value: fmt.Sprintf("instance-%d", wakes)},
			{Key: api.PlatformNodeIDEnv, Value: fmt.Sprintf("node-%d", wakes%2)}}
	}

	// The capture the rig made has no env.json: this restore writes it, and
	// the park captures a drive that holds it.
	if row := preBootSkipRow(t, r, capture, withEnv); row["pre_boot_files_written"] == 0 {
		t.Fatalf("restore adding app env wrote nothing: %v", row)
	}
	if _, err := r.m.Park(ctx, "prefetch-metal", r.spec); err != nil {
		t.Fatal(err)
	}
	t.Log(describeCapturedPreBootFiles(t, strings.TrimSuffix(r.memPath, "mem")+"drive"))

	r.vmm.preBoot = newPreBootLedger() // vmmd restart
	first := preBootSkipRow(t, r, capture, withEnv)
	if err := r.m.Destroy(ctx, "prefetch-metal"); err != nil {
		t.Fatal(err)
	}
	second := preBootSkipRow(t, r, capture, withEnv)
	t.Logf("after restart: first restore skipped=%d written=%d; second restore skipped=%d written=%d",
		first["stage_pre_boot_files_skipped"], first["pre_boot_files_written"],
		second["stage_pre_boot_files_skipped"], second["pre_boot_files_written"])
	if second["stage_pre_boot_files_skipped"] != 1 {
		t.Fatalf("second restore of an unchanged capture with app env did not skip: first %v second %v", first, second)
	}
}

// describeCapturedPreBootFiles reports owner, mode and size of the pre-boot
// files on a captured drive, never their contents.
func describeCapturedPreBootFiles(t *testing.T, drive string) string {
	t.Helper()
	mnt := t.TempDir()
	if out, err := exec.Command("mount", "-o", "loop,ro", drive, mnt).CombinedOutput(); err != nil {
		return fmt.Sprintf("mount captured drive: %v %s", err, out)
	}
	defer func() { _ = exec.Command("umount", mnt).Run() }()
	var b strings.Builder
	for _, p := range []string{apiEnvPath, serviceDiscoveryResolverPath} {
		target, err := stagedDrivePath(mnt, p)
		if err != nil {
			fmt.Fprintf(&b, "%s: %v; ", p, err)
			continue
		}
		info, err := os.Lstat(target)
		if err != nil {
			fmt.Fprintf(&b, "%s: %v; ", p, err)
			continue
		}
		st := info.Sys().(*syscall.Stat_t)
		fmt.Fprintf(&b, "%s: mode %v uid %d gid %d size %d; ", filepath.Base(p), info.Mode(), st.Uid, st.Gid, info.Size())
	}
	return b.String()
}
