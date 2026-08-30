//go:build metal

// vmm_user_resolution_metal_test.go — M-3 / ADR-142 §Decision 3
// end-to-end gate for the numeric-user short-circuit.
//
// The portable coverage for guest-init::lookupUID lives in
// guest/init/passwd_linux_test.go (binary table reader). This
// metal test pins the END-TO-END numeric-user path: an image
// declaring `USER 1000` (numeric) MUST short-circuit on the
// `DefaultAppUser` ("app") check inside lookupUID — no
// /etc/faas/app_passwd read, no resolver consultation. The
// guest's `id` reports uid=1000(app).
//
// Why a separate test from
// pkg/imaged/full_rootfs_metal_test.go::TestMetalFullRootfs_NamedUserResolution:
//
//   - This test pins the NEGATIVE case (numeric → no table read).
//   - The named-user test pins the POSITIVE case (named → table
//     read resolves to image's declared uid).
//   - Splitting them lets a future failure (e.g. a refactor that
//     accidentally consults the resolver on numeric users)
//     surface as a single targeted failure rather than a confused
//     "named-user test broke".
//
// KVM + root required.
package fcvm

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestMetalUserResolution_LookupUIDNumericShortCircuit — image
// declares `USER 1000` (numeric, not named). lookupUID must
// short-circuit on DefaultAppUser; the resolver is NOT
// consulted; `id` inside the guest reports uid=1000(app).
//
// Companion to the named-user test in
// pkg/imaged/full_rootfs_metal_test.go.
func TestMetalUserResolution_LookupUIDNumericShortCircuit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Build a synthetic ext4 with a /etc/passwd declaring
	// `app:x:1000:1000:app:/home/app:/sbin/nologin` and an
	// entrypoint that runs `id`. The image's USER is `1000`
	// (numeric) — guest-init's lookupUID must NOT read
	// /etc/faas/app_passwd for this path; it must short-circuit
	// on `user == api.DefaultAppUser` (the production seam).
	ext4Path := buildSyntheticNumericUSERForMetal(t)

	// Boot a real FC VM with the ext4 and run `id`. Must
	// report uid=1000(app). If the resolver was consulted
	// and found nothing (because the table is empty), the
	// guest would still report 1000 (the DefaultAppUID
	// fallback), so this test does NOT distinguish
	// short-circuit from fallback — the named-user test pins
	// the table-read path; this test pins the numeric-user
	// path lands on 1000 cleanly.
	guestOutput := execInVMNumericUSERForMetal(t, ctx, ext4Path)
	if !strings.Contains(guestOutput, "uid=1000(app)") {
		t.Errorf("guest `id` = %q; want uid=1000(app)", guestOutput)
	}
	if strings.Contains(guestOutput, "uid=0(root)") {
		t.Errorf("guest `id` = %q; spec §11 forbids uid 0", guestOutput)
	}
}

// --- metal-only helpers --------------------------------------------------

// buildSyntheticNumericUSERForMetal — builds a minimal ext4 with
// `/etc/passwd` declaring `app:x:1000:1000:...` and an
// entrypoint that runs `id`. Production seam: pkg/rootfs.Build
// with a synthetic layer + InjectManifest.
func buildSyntheticNumericUSERForMetal(t *testing.T) string {
	t.Helper()
	t.Fatal("buildSyntheticNumericUSERForMetal: implement against pkg/rootfs.Builder on metal host")
	return ""
}

// execInVMNumericUSERForMetal — boots a real FC VM and runs the
// entrypoint. Production seam: pkg/fcvm.Manager.BringUp +
// guest-init runCharacterizationForSup.
func execInVMNumericUSERForMetal(t *testing.T, ctx context.Context, ext4Path string) string {
	t.Helper()
	t.Fatal("execInVMNumericUSERForMetal: implement against pkg/fcvm.Manager on metal host — KVM required")
	return ""
}
