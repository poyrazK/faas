//go:build linux && amd64 && metal

package rootfs

// adr: 435. This gate executes the real ext4/OverlayFS boundary. It does not
// establish Grype freshness, publisher approval, native boot or rollout adoption.

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/overlaymetadata"
	"github.com/onebox-faas/faas/pkg/scanview"
)

const overlayNamespaceParent = "GREGALE_TEST_OVERLAY_PARENT_NAMESPACE"
const overlayParentPID = "GREGALE_TEST_OVERLAY_PARENT_PID"

func TestMetalApplicationStandardOverlayWhiteouts(t *testing.T) {
	if os.Getenv("FAAS_RUN_APPLICATION_STANDARD_OVERLAY_TESTS") != "1" {
		t.Skip("set FAAS_RUN_APPLICATION_STANDARD_OVERLAY_TESTS=1 on the dedicated native acceptance host")
	}
	if os.Geteuid() != 0 {
		t.Fatal("native overlay acceptance requires root")
	}
	if _, err := os.Stat("/etc/faas/builder-acceptance-host"); err != nil {
		t.Fatal("dedicated acceptance-host marker is missing")
	}
	if info, err := os.Stat("/dev/kvm"); err != nil || info.Mode()&os.ModeCharDevice == 0 {
		t.Fatal("dedicated native KVM host is required")
	}
	namespace, err := os.Readlink("/proc/self/ns/mnt")
	if err != nil {
		t.Fatal(err)
	}
	if parent := os.Getenv(overlayNamespaceParent); parent != "" {
		liveParent, err := os.Readlink(fmt.Sprintf("/proc/%d/ns/mnt", os.Getppid()))
		if err != nil || liveParent != parent || namespace == parent || os.Getenv(overlayParentPID) != strconv.Itoa(os.Getppid()) {
			t.Fatalf("private overlay namespace handshake refused: %v", err)
		}
		for _, opaque := range []bool{false, true} {
			t.Run(fmt.Sprintf("root-opaque=%v", opaque), func(t *testing.T) { exerciseNativeOverlayWhiteouts(t, opaque) })
		}
		t.Run("independent-sidecar", exerciseNativeSidecarWhiteouts)
		return
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "unshare", "--mount", "--propagation", "private", "--", executable,
		"-test.run=^TestMetalApplicationStandardOverlayWhiteouts$", "-test.v", "-test.timeout=110s")
	cmd.Env = append(os.Environ(), overlayNamespaceParent+"="+namespace, overlayParentPID+"="+strconv.Itoa(os.Getpid()))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("isolated native overlay test: %v\n%s", err, out)
	}
}

type nativeOverlayEntry struct {
	name, body, link string
	kind             byte
}

func applyNativeOverlayEntries(t *testing.T, root string, entries []nativeOverlayEntry, opts layerApplyOptions) {
	t.Helper()
	var data bytes.Buffer
	w := tar.NewWriter(&data)
	for _, entry := range entries {
		kind := entry.kind
		if kind == 0 {
			kind = tar.TypeReg
		}
		hdr := &tar.Header{Name: entry.name, Typeflag: kind, Mode: 0o755, Size: int64(len(entry.body)), Linkname: entry.link}
		if err := w.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(entry.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := applyLayer(root, tar.NewReader(&data), opts); err != nil {
		t.Fatal(err)
	}
}

func nativeOverlayCommand(t *testing.T, name string, args ...string) {
	t.Helper()
	if out, err := exec.CommandContext(t.Context(), name, args...).CombinedOutput(); err != nil {
		t.Fatalf("%s: %v\n%s", name, err, out)
	}
}

func exerciseNativeOverlayWhiteouts(t *testing.T, rootOpaque bool) {
	t.Helper()
	root := t.TempDir()
	baseTree, appTree := filepath.Join(root, "base-tree"), filepath.Join(root, "app-tree")
	for _, tree := range []string{baseTree, appTree} {
		if err := os.Mkdir(tree, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"base-only", "base-root-visible", "survivor", "file-after", "link-after", "hardlink-after", "skipped-after",
		"opaque/lower", "dir-after/lower", "implicit-dir/lower"} {
		path := filepath.Join(baseTree, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("lower content"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	opaqueRoot := false
	opts := layerApplyOptions{preserveWhiteouts: true, opaqueRoot: &opaqueRoot}
	if rootOpaque {
		applyNativeOverlayEntries(t, appTree, []nativeOverlayEntry{{name: ".wh..wh..opq"}}, opts)
	}
	var deletions []nativeOverlayEntry
	for _, name := range []string{"base-only", "file-after", "link-after", "hardlink-after", "dir-after", "implicit-dir", "skipped-after"} {
		deletions = append(deletions, nativeOverlayEntry{name: ".wh." + name})
	}
	deletions = append(deletions, nativeOverlayEntry{name: "opaque/.wh..wh..opq"})
	applyNativeOverlayEntries(t, appTree, deletions, opts)
	applyNativeOverlayEntries(t, appTree, []nativeOverlayEntry{
		{name: "file-after", body: "replacement"},
		{name: "dir-after", kind: tar.TypeDir},
		{name: "dir-after/current", body: "replacement"},
		{name: "implicit-dir/current", body: "replacement"},
		{name: "link-after", kind: tar.TypeSymlink, link: "/survivor"},
		{name: "hardlink-after", kind: tar.TypeLink, link: "file-after"},
		{name: "opaque/current", body: "replacement"},
		{name: "skipped-after", kind: tar.TypeChar},
		{name: "upper/customer", body: "replacement"},
		{name: ".faas-app-upper-source/customer", body: "replacement"},
	}, opts)
	if rootOpaque {
		applyNativeOverlayEntries(t, appTree, []nativeOverlayEntry{{name: "survivor", body: "lower content"}}, opts)
	}
	if opaqueRoot != rootOpaque {
		t.Fatal("builder lost root opacity")
	}
	if err := stageAppUpper(appTree, opaqueRoot); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(appTree, "work"), 0o755); err != nil {
		t.Fatal(err)
	}
	baseImage, appImage := filepath.Join(root, "base.ext4"), filepath.Join(root, "app.ext4")
	for image, tree := range map[string]string{baseImage: baseTree, appImage: appTree} {
		nativeOverlayCommand(t, "mkfs.ext4", "-q", "-F", "-O", "^has_journal", "-d", tree, image, "16M")
	}
	baseBytes, err := os.ReadFile(baseImage)
	if err != nil {
		t.Fatal(err)
	}
	baseHash := sha256.Sum256(baseBytes)
	var mounted []string
	t.Cleanup(func() {
		for i := len(mounted) - 1; i >= 0; i-- {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			out, err := exec.CommandContext(ctx, "umount", "--", mounted[i]).CombinedOutput()
			cancel()
			if err != nil {
				t.Errorf("release owned mount: %v\n%s", err, out)
			}
		}
	})
	mount := func(target string, args ...string) {
		if err := os.Mkdir(target, 0o700); err != nil {
			t.Fatal(err)
		}
		nativeOverlayCommand(t, "mount", append(args, target)...)
		mounted = append(mounted, target)
	}
	base, main := filepath.Join(root, "base"), filepath.Join(root, "main")
	guest, readonly := filepath.Join(root, "guest"), filepath.Join(root, "readonly")
	mount(base, "-o", "loop,ro,noload,nodev,nosuid,noexec", baseImage)
	mount(main, "-o", "loop,nodev,nosuid,noexec", appImage)
	lower, err := overlaymetadata.GuestLowerDirectory(main+"/upper", base, main+"/empty-lower")
	if err != nil {
		t.Fatal(err)
	}
	if rootOpaque {
		if lower != main+"/empty-lower" {
			t.Fatal("opaque root selected shared base")
		}
		if err := overlaymetadata.EnsureEmptyLowerDirectory(lower); err != nil {
			t.Fatal(err)
		}
	} else if lower != base {
		t.Fatal("ordinary root lost shared base")
	}
	scanLowers, err := overlaymetadata.ReadOnlyLowerDirectories(main+"/upper", base)
	if err != nil {
		t.Fatal(err)
	}
	// Match guest-init's actual upper/work selection; keep both drives distinct.
	mount(guest, "-t", "overlay", "-o", "lowerdir="+lower+",upperdir="+main+"/upper,workdir="+main+"/work", "overlay")
	mount(readonly, "-t", "overlay", "-o", "ro,nodev,nosuid,noexec,lowerdir="+strings.Join(scanLowers, ":"), "overlay")
	assertNativeOverlayVisibility(t, guest)
	assertNativeOverlayVisibility(t, readonly)
	for _, view := range []string{guest, readonly} {
		_, err := os.Stat(filepath.Join(view, "base-root-visible"))
		if rootOpaque && !os.IsNotExist(err) || !rootOpaque && err != nil {
			t.Fatal("root opacity differs from guest policy", err)
		}
	}
	actual, err := scanview.Snapshot(t.Context(), guest)
	if err != nil {
		t.Fatal(err)
	}
	scan, err := scanview.Snapshot(t.Context(), readonly)
	if err != nil || actual != scan {
		t.Fatal("read-only scanner view differs from guest composition", err)
	}
	projection := filepath.Join(root, "projection")
	if err := os.Mkdir(projection, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := scanview.Copy(t.Context(), readonly, projection, scan); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(projection, "link-after")); err != nil || string(data) != "lower content" {
		t.Fatal("guest absolute link was not preserved in scanner projection", err)
	}
	if err := os.WriteFile(filepath.Join(readonly, "forbidden"), nil, 0o600); err == nil {
		t.Fatal("scanner overlay accepted a write")
	}
	if data, err := os.ReadFile(baseImage); err != nil || sha256.Sum256(data) != baseHash {
		t.Fatal("shared read-only base artifact changed", err)
	}
}

func assertNativeOverlayVisibility(t *testing.T, root string) {
	t.Helper()
	for _, name := range []string{"base-only", ".wh.base-only", "opaque/lower", "dir-after/lower", "implicit-dir/lower", "skipped-after"} {
		if _, err := os.Lstat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatal("deleted lower content or archive marker remains visible", name, err)
		}
	}
	for _, name := range []string{"file-after", "dir-after/current", "implicit-dir/current", "opaque/current", "hardlink-after", "upper/customer", ".faas-app-upper-source/customer"} {
		if data, err := os.ReadFile(filepath.Join(root, name)); err != nil || string(data) != "replacement" {
			t.Fatal("recreated upper entry differs", name, err)
		}
	}
	if target, err := os.Readlink(filepath.Join(root, "link-after")); err != nil || target != "/survivor" {
		t.Fatal("guest symlink target changed", err)
	}
}

func exerciseNativeSidecarWhiteouts(t *testing.T) {
	root := t.TempDir()
	tree := filepath.Join(root, "tree")
	if err := os.Mkdir(tree, 0o700); err != nil {
		t.Fatal(err)
	}
	opts := layerApplyOptions{skipRuntimeMountpoints: true}
	applyNativeOverlayEntries(t, tree, []nativeOverlayEntry{
		{name: "removed", body: "first layer"}, {name: "opaque/lower", body: "first layer"},
	}, opts)
	applyNativeOverlayEntries(t, tree, []nativeOverlayEntry{
		{name: ".wh.removed"}, {name: "opaque/.wh..wh..opq"},
		{name: "opaque/current", body: "replacement"}, {name: "upper/customer", body: "replacement"},
	}, opts)
	if err := stageAppUpper(tree, false); err != nil {
		t.Fatal(err)
	}
	image, mounted := filepath.Join(root, "sidecar.ext4"), filepath.Join(root, "mounted")
	nativeOverlayCommand(t, "mkfs.ext4", "-q", "-F", "-O", "^has_journal", "-d", tree, image, "16M")
	if err := os.Mkdir(mounted, 0o700); err != nil {
		t.Fatal(err)
	}
	nativeOverlayCommand(t, "mount", "-o", "loop,ro,noload,nodev,nosuid,noexec", image, mounted)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if out, err := exec.CommandContext(ctx, "umount", "--", mounted).CombinedOutput(); err != nil {
			t.Errorf("release sidecar root: %v\n%s", err, out)
		}
	})
	for _, name := range []string{"removed", ".wh.removed", "opaque/lower", "opaque/.wh..wh..opq"} {
		if _, err := os.Lstat(filepath.Join(mounted, "upper", name)); !os.IsNotExist(err) {
			t.Fatal("sidecar retained deletion marker", name, err)
		}
	}
	for _, name := range []string{"opaque/current", "upper/customer"} {
		if data, err := os.ReadFile(filepath.Join(mounted, "upper", name)); err != nil || string(data) != "replacement" {
			t.Fatal("sidecar root changed", name, err)
		}
	}
	if _, err := scanview.Snapshot(t.Context(), filepath.Join(mounted, "upper")); err != nil {
		t.Fatal("sidecar scanner view contains unsupported filesystem entries", err)
	}
	if err := os.WriteFile(filepath.Join(mounted, "upper", "forbidden"), nil, 0o600); err == nil {
		t.Fatal("sidecar root accepted a write")
	}
}
