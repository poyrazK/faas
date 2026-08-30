//go:build metal

// full_rootfs_metal_test.go — M-3 / ADR-141+142 acceptance gates.
//
// These tests drive the full FC VM boot path against three
// real-world images that the two-drive path rejects today:
//
//  1. distroless/static-debian12 — single-layer image with no
//     /bin/sh. Spec §11 forbids landing on uid 0 in the guest.
//     Acceptance: full-rootfs auto-dispatch on Hobby, cold-boot
//     succeeds, `whoami` reports `nonroot` (uid 65532).
//
//  2. alpine:latest — multi-arch image-index, top-most-wins
//     /etc/passwd merge walk. Acceptance: full-rootfs
//     auto-dispatch, cold-boot, `cat /etc/os-release` reports
//     alpine, `id` reports uid=0(root) (alpine's /etc/passwd
//     declares root as default USER).
//
//  3. Synthetic image declaring `USER node` (Uname="node",
//     Uid=0). Acceptance: `id` inside the guest reports uid 999
//     (the alpine /etc/passwd value for the `node` user), NOT
//     1000 (DefaultAppUID) and NOT 0 (root).
//
//  4. Two-drive customer unaffected — a `FROM runner-*` image
//     continues to take the two-drive path with drive0 attached
//     (no drive0+vda replacement). Pins the load-bearing
//     constraint that M-3 does NOT regress today's customer base.
//
// All four tests require:
//   - KVM access (/dev/kvm)
//   - root (for netns/jailer)
//   - network access to public registries (gcr.io, docker.io)
//
// They run under `make metal-lima` on M3+ Macs via Lima nested
// virt, or on bare-metal x86_64 control-plane nodes (the §14
// source of truth per CLAUDE.md "Developing the metal side").
// CI gates commit 10 — a green local pass on macOS is necessary,
// not sufficient.
//
// Why metal (not portable):
//   - BuildFullRootfs runs mkfs.ext4 on a staging dir (host mkfs
//     binary required; some macOS dev hosts lack it).
//   - The full FC VM boot path uses /dev/kvm which macOS does
//     not provide.
//   - Pulling public images over the network is non-deterministic;
//     portable CI runs against fake registries.
//
// The portable equivalents (pkg/rootfs/passwd_table_test.go +
// guest/init/passwd_linux_test.go) pin the data-shape and
// guest-init reader; these four tests pin the END-TO-END
// guest identity.
package imaged

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestMetalFullRootfs_DistrolessStaticDebian12 — auto-dispatch on
// Hobby, full-rootfs build, cold boot, `whoami` reports
// `nonroot` (uid 65532).
//
// This is the §14 acceptance gate for M-3's primary value
// proposition: every public-registry image (distroless, alpine,
// scratch) deploys as a Gregale app. A green pass here means a
// `faas deploy --image gcr.io/distroless/static-debian12` on a
// Hobby plan cold-boots and reports the expected uid inside the
// guest.
func TestMetalFullRootfs_DistrolessStaticDebian12(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Step 1: pull the distroless image via imaged's full OCI
	// pull path (multi-arch walk + manifest resolution +
	// config pull). On amd64 host (production x86_64 control
	// plane), the walker selects linux/amd64.
	imageRef := "gcr.io/distroless/static-debian12:latest"
	layers, manifest := pullImageForMetal(t, ctx, imageRef)
	if len(layers) == 0 {
		t.Fatalf("pullImageForMetal(%q) returned no layers", imageRef)
	}

	// Step 2: LayersAboveBase MUST return ErrLayersNotAboveBase
	// (distroless has no runner-* ancestor) — the typed sentinel
	// is the dispatch signal.
	_, _, err := aboveBaseForMetal(t, ctx, layers, manifest, "node22")
	if err == nil {
		t.Fatalf("aboveBaseForMetal(%q) succeeded; want ErrLayersNotAboveBase on distroless", imageRef)
	}
	if !strings.Contains(err.Error(), "layers above base") {
		t.Fatalf("aboveBaseForMetal(%q) error = %v; want ErrLayersNotAboveBase", imageRef, err)
	}

	// Step 3: dispatchFullRootfs must resolve to buildFullRootfsLayer
	// on Hobby (auto-dispatch). The full-rootfs build assembles
	// an ext4 from ALL distroless layers.
	result := buildFullRootfsForMetal(t, ctx, layers, manifest)
	if result.ContentBytes == 0 {
		t.Fatalf("buildFullRootfsForMetal(%q) returned 0-byte ext4", imageRef)
	}

	// Step 4: boot a real FC VM with the produced ext4 as
	// drive0+vda (no drive1 overlay — full-rootfs shape).
	// Verify: `whoami` reports `nonroot` (uid 65532 from
	// distroless's /etc/passwd). Spec §11 forbids uid 0 in the
	// guest.
	guestOutput := execInVMForMetal(t, ctx, result.ImagePath, "whoami")
	if !strings.Contains(guestOutput, "nonroot") {
		t.Errorf("guest `whoami` = %q; want `nonroot` (uid 65532)", guestOutput)
	}
}

// TestMetalFullRootfs_AlpineLatest — multi-arch manifest-list
// resolution + auto-dispatch on Hobby.
//
// alpine:latest is a multi-arch image-index (linux/amd64 +
// linux/arm64 + linux/386 + ...). M-3 commit 3's walker must
// select the linux/amd64 descriptor on amd64 hosts. The
// resulting full-rootfs ext4 lands alpine's /etc/os-release
// in the guest; `cat /etc/os-release` reports `Alpine Linux`.
func TestMetalFullRootfs_AlpineLatest(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	imageRef := "alpine:latest"
	layers, manifest := pullImageForMetal(t, ctx, imageRef)
	if len(layers) == 0 {
		t.Fatalf("pullImageForMetal(%q) returned no layers", imageRef)
	}
	// M-3 commit 3's walker must have selected linux/amd64 (or
	// linux/arm64 on Lima). On a bare-metal amd64 box, the
	// resolved manifest must report the amd64 descriptor.
	if manifest.Architecture != "amd64" {
		t.Errorf("alpine:latest resolved to arch %q; want amd64 on production hosts",
			manifest.Architecture)
	}

	// Auto-dispatch on Hobby (alpine is NOT a runner-* descendant).
	result := buildFullRootfsForMetal(t, ctx, layers, manifest)
	if result.ContentBytes == 0 {
		t.Fatalf("buildFullRootfsForMetal(%q) returned 0-byte ext4", imageRef)
	}

	// Cold-boot + cat /etc/os-release. Alpine's release file
	// starts with `NAME="Alpine Linux"`.
	guestOutput := execInVMForMetal(t, ctx, result.ImagePath, "cat /etc/os-release")
	if !strings.Contains(guestOutput, "Alpine") {
		t.Errorf("guest `cat /etc/os-release` = %q; want 'Alpine Linux'", guestOutput)
	}

	// Alpine's /etc/passwd declares root as the default USER,
	// so `id` reports uid=0(root) — different from distroless's
	// `nonroot`. Both shapes must land cleanly; the resolver is
	// only consulted when hdr.Uid=0+Uname non-empty.
	idOutput := execInVMForMetal(t, ctx, result.ImagePath, "id")
	if !strings.Contains(idOutput, "uid=0(root)") {
		t.Errorf("alpine guest `id` = %q; want uid=0(root)", idOutput)
	}
}

// TestMetalFullRootfs_NamedUserResolution — synthetic image
// declaring `USER node` (Uname="node", Uid=0). The distroless
// /etc/passwd has a `node` entry at uid 999; the resolver must
// land the guest on uid 999, NOT 1000 (DefaultAppUID) and NOT
// 0 (root).
//
// Pins the END-TO-END named-user resolution: builder writes the
// /etc/faas/app_passwd binary table (commit 7), guest-init reads
// it at boot (commit 8). Spec §11 forbids landing on uid 0.
func TestMetalFullRootfs_NamedUserResolution(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Step 1: build a synthetic tar layer that:
	//   - declares USER node (hdr.Uname="node", hdr.Uid=0)
	//   - ships a /etc/passwd with `node:x:999:999::/home/node:/sbin/nologin`
	//   - includes /bin/echo + a tiny entrypoint script.
	layers := buildSyntheticUserNodeLayersForMetal(t)
	manifest := manifestForSyntheticUserNode()

	// Step 2: BuildFullRootfs grows the resolver from the
	// synthetic /etc/passwd (commit 7 merge walk). The
	// ApplyLayerGzWithResolver call threads the resolver so the
	// entrypoint script lands under uid 999.
	result := buildFullRootfsForMetal(t, ctx, layers, manifest)
	if result.ContentBytes == 0 {
		t.Fatalf("buildFullRootfsForMetal returned 0-byte ext4")
	}

	// Step 3: boot a real FC VM; `id` inside the guest must
	// report uid=999(node), proving the resolver threaded
	// through layer-apply AND the binary passwd table landed
	// in /etc/faas/app_passwd.
	guestOutput := execInVMForMetal(t, ctx, result.ImagePath, "id")
	if !strings.Contains(guestOutput, "uid=999(node)") {
		t.Errorf("guest `id` = %q; want uid=999(node)", guestOutput)
	}
	if strings.Contains(guestOutput, "uid=0(root)") {
		t.Errorf("guest `id` = %q; landed on uid=0(root); spec §11 forbids", guestOutput)
	}
	if strings.Contains(guestOutput, "uid=1000(app)") {
		t.Errorf("guest `id` = %q; resolver did not thread (still on DefaultAppUID 1000)", guestOutput)
	}
}

// TestMetalFullRootfs_TwoDriveCustomerUnaffected — a `FROM
// runner-*` image continues to take the two-drive path (drive0
// shared base + drive1 per-app overlay). Pins the load-bearing
// constraint that M-3 does NOT regress today's customer base.
//
// The runner-* base is built by imaged.EnsureBaseExt4 and lives
// at /srv/fc/base/runner-node22.ext4 (or equivalent). A green
// pass here means M-3's auto-dispatch did not silently flatten
// any two-drive customer onto the full-rootfs path.
func TestMetalFullRootfs_TwoDriveCustomerUnaffected(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Use the locally-built runner-node22 image (the canonical
	// M-6 two-drive base) as the test fixture. On a fresh box
	// this image must already be staged; the test FAILS rather
	// than SKIPs on missing fixture so the CI catches a missing
	// base build.
	const runnerRef = "ghcr.io/onebox-faas/runner-node22:latest"
	layers, manifest := pullImageForMetal(t, ctx, runnerRef)
	if len(layers) == 0 {
		t.Fatalf("pullImageForMetal(%q) returned no layers; EnsureBaseExt4 may be missing", runnerRef)
	}

	// LayersAboveBase MUST succeed (runner-* is the base) —
	// the typed sentinel MUST NOT fire.
	above, diffs, err := aboveBaseForMetal(t, ctx, layers, manifest, "node22")
	if err != nil {
		t.Fatalf("aboveBaseForMetal(%q) error = %v; two-drive customer must NOT trip the typed sentinel",
			runnerRef, err)
	}
	if len(diffs) == 0 {
		t.Errorf("above-base diffs = 0; runner-* must declare at least one above-base layer")
	}
	_ = above // kept for future fixture assertions

	// The dispatch path MUST NOT take the buildFullRootfsLayer
	// branch on a two-drive customer. We assert via the metric
	// counter: imaged_passwd_entries_total must NOT increment
	// on a two-drive build (the counter only fires inside the
	// full-rootfs path).
	before := readPasswdEntriesCounterForMetal(t, "ok")
	_ = buildTwoDriveForMetal(t, ctx, layers, manifest)
	after := readPasswdEntriesCounterForMetal(t, "ok")
	if after != before {
		t.Errorf("imaged_passwd_entries_total{outcome=ok} moved (%v → %v) on two-drive build; full-rootfs path leaked",
			before, after)
	}
}

// --- metal-only helpers --------------------------------------------------
//
// The helpers below wrap the production seams these tests pin.
// They live in this file (//go:build metal) so portable CI does
// not require KVM, network, or mkfs binaries. A future refactor
// may split them into a non-test helper package if other metal
// tests need them.
//
// pullImageForMetal pulls a public OCI image via imaged's full
// pull path (multi-arch walk, manifest, config, blobs) and
// returns the layer readers + the resolved per-arch manifest
// metadata. On amd64 hosts the walker must select linux/amd64.
func pullImageForMetal(t *testing.T, ctx context.Context, ref string) ([]layerReaderForMetal, manifestForMetal) {
	t.Helper()
	// Production seam: pkg/oci.RegistryClient.PullManifestWithAuth
	// + PullBlobWithAuth. The metal test wraps the same calls
	// the production handler uses (see handler.go::pullManifestWithAuth).
	t.Fatal("pullImageForMetal: implement against pkg/oci.RegistryClient on metal host — see handler.go::pullManifestWithAuth for the canonical seam")
	return nil, manifestForMetal{}
}

// aboveBaseForMetal mirrors imaged.handler::aboveBaseLayers
// (M-6 two-drive seam). The metal test asserts the typed
// ErrLayersNotAboveBase sentinel trips on distroless/alpine
// images and DOES NOT trip on runner-* images.
func aboveBaseForMetal(t *testing.T, ctx context.Context, _ []layerReaderForMetal, _ manifestForMetal, _ string) (aboveBaseStreamForMetal, []string, error) {
	t.Helper()
	t.Fatal("aboveBaseForMetal: implement against imaged.handler::aboveBaseLayers on metal host")
	return aboveBaseStreamForMetal{}, nil, nil
}

// buildFullRootfsForMetal mirrors imaged.handler::buildFullRootfsLayer
// (commit 6 wiring). Returns the produced ext4 metadata so the
// caller can boot a VM with it.
func buildFullRootfsForMetal(t *testing.T, ctx context.Context, layers []layerReaderForMetal, manifest manifestForMetal) fullRootfsResultForMetal {
	t.Helper()
	t.Fatal("buildFullRootfsForMetal: implement against rootfs.Builder.BuildFullRootfs on metal host")
	return fullRootfsResultForMetal{}
}

// buildTwoDriveForMetal mirrors imaged.handler::buildImageLayer
// (two-drive branch). The metal test asserts the dispatch
// stays on the two-drive path for runner-* customers — the
// metric counter check inside TestMetalFullRootfs_TwoDriveCustomerUnaffected
// proves no full-rootfs side effects fired.
func buildTwoDriveForMetal(t *testing.T, ctx context.Context, layers []layerReaderForMetal, manifest manifestForMetal) error {
	t.Helper()
	t.Fatal("buildTwoDriveForMetal: implement against rootfs.Builder.Build on metal host")
	return nil
}

// execInVMForMetal boots a real Firecracker VM with the given
// ext4 as drive0+vda and runs `cmd` inside the guest via vsock
// or guest-init's exec helper. Production seam:
// pkg/fcvm/Manager.BringUp + guest/init runCharacterizationForSup.
func execInVMForMetal(t *testing.T, ctx context.Context, ext4Path, cmd string) string {
	t.Helper()
	t.Fatal("execInVMForMetal: implement against pkg/fcvm.Manager.BringUp on metal host — KVM required")
	return ""
}

// readPasswdEntriesCounterForMetal reads the cumulative value of
// imaged_passwd_entries_total{outcome=ok}. The counter is
// incremented ONLY on the full-rootfs build path; a two-drive
// build must NOT move it.
func readPasswdEntriesCounterForMetal(t *testing.T, outcome string) float64 {
	t.Helper()
	t.Fatal("readPasswdEntriesCounterForMetal: implement against pkg/wire.OpsMetrics.PasswdEntries on metal host")
	return 0
}

// buildSyntheticUserNodeLayersForMetal constructs tar layers
// declaring `USER node` (Uname="node", Uid=0) and shipping a
// /etc/passwd with the `node` entry at uid 999. The metal test
// exercises the resolver path end-to-end without needing a
// real registry fixture.
func buildSyntheticUserNodeLayersForMetal(t *testing.T) []layerReaderForMetal {
	t.Helper()
	t.Fatal("buildSyntheticUserNodeLayersForMetal: implement tar-layer fixtures on metal host")
	return nil
}

// manifestForSyntheticUserNode — the AppManifest shape for the
// synthetic `USER node` image. Mirrors the production
// pkg/api.AppManifest.Validate() contract.
func manifestForSyntheticUserNode() manifestForMetal {
	return manifestForMetal{
		User:         "node",
		WorkingDir:   "/home/node",
		Entrypoint:   []string{"/bin/echo"},
		Architecture: "amd64",
		OS:           "linux",
	}
}

// --- fixture types -------------------------------------------------------
//
// These mirror the production types but live in the test file so
// the portable CI build does not require pkg/fcvm + pkg/api.
//
// layerReaderForMetal is a placeholder for a gzip-compressed tar
// blob reader (the same shape as oci.LayersAboveBase's diff_ids
// mapping).
type layerReaderForMetal struct{}

// manifestForMetal is the per-arch manifest metadata the metal
// tests pin. Production shape: pkg/oci.Manifest.
type manifestForMetal struct {
	Architecture string
	OS           string
	User         string
	WorkingDir   string
	Entrypoint   []string
}

// aboveBaseStreamForMetal is the (closers + readers) return
// shape from pkg/imaged.handler::aboveBaseLayers. Mirrors
// pkg/imaged.aboveBaseStream.
type aboveBaseStreamForMetal struct{}

// fullRootfsResultForMetal is the (ImagePath + ContentBytes)
// return shape from rootfs.Builder.BuildFullRootfs.
type fullRootfsResultForMetal struct {
	ImagePath    string
	ContentBytes int64
}
