//go:build metal

package fcvm

// Snapshot storage experiment (2026-11-14): how much of a parked app's
// snapshot is unique once snapshots are stored as content-addressed chunks
// across the fleet, how small the snapshot drive gets when it is stored as a
// delta against the app layer, and what each layout does to restore time.
//
// Nothing here changes production code. The bench boots a small fleet of
// Node and Python apps (several deployments, replicas of one deployment,
// several plan sizes), parks each with a production-shaped capture key (so
// the private drive is published too), then:
//
//   - storage: per snapshot, today's object sizes (extent-encoded zstd, as
//     ADR-1135 publishes them) against a fleet chunk store at each chunk size,
//     where a chunk is stored once and image pages count as already stored;
//     and the drive as a block delta against its layer;
//   - restore: for the timing apps, cold and warm restore p50 and the first
//     request after it, from the memory file as captured, as production
//     shares it today (ADR-956 image + peer passes), and as a node would
//     assemble it from a local chunk store by reflink (one clone per run);
//     plus a restore whose drive was rebuilt from the layer and the delta;
//   - cache miss: the local decode/assembly time for today's object against
//     fetching only the chunks the node does not hold.
//
// Opt-in and slow. Requires root, Firecracker, XFS with reflink=1 under
// FAAS_RESTORE_BENCH_FIXTURE_DIR, FAAS_TEST_KERNEL, and runner root
// filesystem tarballs: FAAS_DEDUP_NODE_ROOTFS (required) and
// FAAS_DEDUP_PYTHON_ROOTFS (optional). FAAS_DEDUP_RESTORES sets restores per
// measurement (default 10), FAAS_DEDUP_CHUNKS the chunk sizes in bytes
// (default 4096,16384,65536), FAAS_DEDUP_TIMING the apps whose restores are
// timed (default node-b,node-c).

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	"golang.org/x/sys/unix"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

type dedupApp struct {
	name    string
	runtime string // node | python
	deploy  string // apps of one deployment share base and layer
	memMiB  int
	seed    int
	heapMB  int
	appMB   int // deployment-specific app code in the layer
}

type dedupImages struct{ base, layer string }

type dedupCapture struct {
	app                   dedupApp
	images                dedupImages
	snap                  *Snapshot
	mem, drive            string
	memOrig, driveOrig    string // pristine copies, outside the store
	memSize, memNonZero   int64
	driveSize, driveAlloc int64
}

func dedupFleet(withPython bool) []dedupApp {
	fleet := []dedupApp{
		{name: "node-a", runtime: "node", deploy: "node-a", memMiB: 512, seed: 1, heapMB: 24, appMB: 4},
		{name: "node-a-replica", runtime: "node", deploy: "node-a", memMiB: 512, seed: 1, heapMB: 24, appMB: 4},
		{name: "node-b", runtime: "node", deploy: "node-b", memMiB: 512, seed: 2, heapMB: 32, appMB: 8},
		{name: "node-c", runtime: "node", deploy: "node-c", memMiB: 2048, seed: 3, heapMB: 48, appMB: 12},
		{name: "node-d", runtime: "node", deploy: "node-d", memMiB: 2048, seed: 4, heapMB: 16, appMB: 2},
	}
	if withPython {
		fleet = append(fleet,
			dedupApp{name: "py-a", runtime: "python", deploy: "py-a", memMiB: 512, seed: 5, heapMB: 24, appMB: 4},
			dedupApp{name: "py-b", runtime: "python", deploy: "py-b", memMiB: 2048, seed: 6, heapMB: 32, appMB: 8},
		)
	}
	return fleet
}

func TestMetalSnapshotDedupBench(t *testing.T) {
	nodeTar := os.Getenv("FAAS_DEDUP_NODE_ROOTFS")
	if nodeTar == "" {
		t.Skip("set FAAS_DEDUP_NODE_ROOTFS to a Node runner root filesystem tarball")
	}
	pyTar := os.Getenv("FAAS_DEDUP_PYTHON_ROOTFS")
	kernel := os.Getenv("FAAS_TEST_KERNEL")
	if _, err := os.Stat(kernel); kernel == "" || err != nil {
		t.Skipf("set FAAS_TEST_KERNEL to the production guest kernel (%q: %v)", kernel, err)
	}
	restores := envInt(t, "FAAS_DEDUP_RESTORES", 10)
	chunkSizes := []int{4096, 16384, 65536}
	if raw := os.Getenv("FAAS_DEDUP_CHUNKS"); raw != "" {
		chunkSizes = nil
		for _, f := range strings.Split(raw, ",") {
			n, err := strconv.Atoi(strings.TrimSpace(f))
			if err != nil || n < 4096 || n%4096 != 0 {
				t.Fatalf("FAAS_DEDUP_CHUNKS: %q is not a multiple of 4096", f)
			}
			chunkSizes = append(chunkSizes, n)
		}
	}
	timing := []string{"node-b", "node-c"}
	if raw := os.Getenv("FAAS_DEDUP_TIMING"); raw != "" {
		timing = strings.Split(raw, ",")
	}

	dir := benchFixtureDir(t)
	fcVersion, err := DetectFirecrackerVersion(context.Background())
	if err != nil {
		t.Fatalf("detect firecracker version: %v", err)
	}
	t.Logf("RESULT setup: firecracker %s, kernel %s, fixture dir %s", fcVersion, kernel, dir)
	if !FreePageReportingSupported(fcVersion) || os.Getenv("FAAS_TEST_FREE_PAGE_REPORTING") != "1" {
		t.Logf("WARNING: free page reporting is off (firecracker %s); production has it on", fcVersion)
	}
	snapshotRoot := filepath.Join(dir, "store")
	if err := os.MkdirAll(filepath.Join(snapshotRoot, "snap"), 0o2770); err != nil {
		t.Fatal(err)
	}
	store, err := storage.NewLocalStorageBackend(snapshotRoot)
	if err != nil {
		t.Fatal(err)
	}
	vmm := newMetalVMM(t, 60*time.Second).WithStorage(store)
	stageBenchMountHelper(t, vmm)
	m := NewManager(wire.ExecRunner{}, vmm, Paths{Kernel: kernel}, fcVersion, nil, nil)
	withCgroupRootAt(t, "/sys/fs/cgroup")

	tarballs := map[string]string{"node": nodeTar, "python": pyTar}
	images := map[string]dedupImages{}
	var captures []*dedupCapture
	for _, app := range dedupFleet(pyTar != "") {
		img, ok := images[app.deploy]
		if !ok {
			img = buildDedupImages(t, dir, app, tarballs[app.runtime])
			images[app.deploy] = img
		}
		captures = append(captures, dedupPark(t, m, store, fcVersion, dir, app, img))
	}

	ctx := context.Background()
	dedupStorageReport(t, captures, images, chunkSizes)
	dedupDriveReport(t, captures)
	for _, name := range timing {
		idx := slices.IndexFunc(captures, func(c *dedupCapture) bool { return c.app.name == name })
		if idx < 0 {
			t.Fatalf("FAAS_DEDUP_TIMING: no app %q", name)
		}
		dedupRestoreReport(ctx, t, m, dir, captures, idx, images, chunkSizes, restores)
	}
	if m.LeasedCount() != 0 {
		t.Errorf("leaked leases: %d", m.LeasedCount())
	}
}

// buildDedupImages builds a deployment's base (runtime + app entrypoint) and
// layer (overlay upper with shared libraries and deployment-specific app
// code under /srv/deps), as a deployment's app layer carries its code.
func buildDedupImages(t *testing.T, dir string, app dedupApp, tarball string) dedupImages {
	t.Helper()
	base := filepath.Join(dir, app.deploy+"-base.ext4")
	layer := filepath.Join(dir, app.deploy+"-layer.ext4")
	env := map[string]string{"SEED": strconv.Itoa(app.seed), "HEAP_MB": strconv.Itoa(app.heapMB)}
	var err error
	switch app.runtime {
	case "node":
		err = buildRuntimeBaseExt4(base, repoRoot(t), tarball, "srv/app.js", dedupNodeApp,
			[]string{"/usr/local/bin/node", "/srv/app.js"}, env)
	case "python":
		err = buildRuntimeBaseExt4(base, repoRoot(t), tarball, "srv/app.py", dedupPythonApp,
			[]string{"/usr/local/bin/python3", "/srv/app.py"}, env)
	default:
		t.Fatalf("unknown runtime %q", app.runtime)
	}
	if err != nil {
		t.Fatalf("%s: build base: %v", app.deploy, err)
	}
	if err := buildDedupLayer(layer, tarball, app); err != nil {
		t.Fatalf("%s: build layer: %v", app.deploy, err)
	}
	return dedupImages{base: base, layer: layer}
}

// buildDedupLayer packs an overlay upper whose /srv/deps holds the runtime's
// bundled libraries (shared by every deployment of the runtime, like popular
// packages) and appMB of deployment-specific source text.
func buildDedupLayer(dst, tarball string, app dedupApp) error {
	work, err := os.MkdirTemp(filepath.Dir(dst), "dedup-layer-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(work) }()
	unpack := filepath.Join(work, "unpack")
	if err := os.MkdirAll(unpack, 0o755); err != nil {
		return err
	}
	if out, err := exec.Command("tar", "-xf", tarball, "-C", unpack).CombinedOutput(); err != nil {
		return fmt.Errorf("unpack %s: %w: %s", tarball, err, out)
	}
	upper := filepath.Join(work, "skel", "upper")
	deps := filepath.Join(upper, "srv", "deps")
	if err := os.MkdirAll(deps, 0o755); err != nil {
		return err
	}
	for _, sub := range []string{"etc/faas", "tmp"} {
		if err := os.MkdirAll(filepath.Join(work, "skel", sub), 0o755); err != nil {
			return err
		}
	}
	marker := filepath.Join(upper, "etc/faas/uuid.txt")
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(marker, nil, 0o644); err != nil {
		return err
	}
	if err := os.Chown(marker, 1000, 1000); err != nil {
		return err
	}
	libs, _ := filepath.Glob(filepath.Join(unpack, "usr/local/lib/node_modules"))
	if app.runtime == "python" {
		libs = nil
		for _, prefix := range []string{"usr/local/lib/python3*", "usr/lib/python3*"} {
			for _, pkg := range []string{"email", "json", "http", "asyncio", "xml"} {
				more, _ := filepath.Glob(filepath.Join(unpack, prefix, pkg))
				libs = append(libs, more...)
			}
		}
	}
	for _, lib := range libs {
		if out, err := exec.Command("cp", "-a", lib, deps).CombinedOutput(); err != nil {
			return fmt.Errorf("copy %s: %w: %s", lib, err, out)
		}
	}
	src := filepath.Join(deps, "app")
	if err := os.MkdirAll(src, 0o755); err != nil {
		return err
	}
	r := rand.New(rand.NewSource(int64(app.seed) * 7919))
	words := []string{"const", "function", "return", "await", "if", "else", "for", "let", "user", "order", "price",
		"items", "request", "response", "handler", "config", "=", "(", ")", "{", "}", ";", "\n", "\n  "}
	for i := 0; i < app.appMB; i++ {
		var b strings.Builder
		for b.Len() < 1<<20 {
			b.WriteString(words[r.Intn(len(words))])
			b.WriteString(" ")
			if r.Intn(9) == 0 {
				fmt.Fprintf(&b, "v%x_%d ", r.Uint32(), r.Intn(1000))
			}
		}
		if err := os.WriteFile(filepath.Join(src, fmt.Sprintf("bundle-%02d.js", i)), []byte(b.String()), 0o644); err != nil {
			return err
		}
	}
	if out, err := exec.Command("chown", "-R", "1000:1000", filepath.Join(upper, "srv")).CombinedOutput(); err != nil {
		return fmt.Errorf("chown deps: %w: %s", err, out)
	}
	du, err := exec.Command("du", "-sm", filepath.Join(work, "skel")).Output()
	if err != nil {
		return err
	}
	var usedMB int
	if _, err := fmt.Sscan(string(du), &usedMB); err != nil {
		return err
	}
	// Production pads the app layer to the plan's disk; 256 MiB of slack
	// keeps the fixture small while leaving the image mostly holes.
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	if err := f.Truncate(int64(usedMB+256) << 20); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if out, err := exec.Command("mkfs.ext4", "-q", "-O", "^has_journal", "-d", filepath.Join(work, "skel"),
		"-L", "faas-dedup-layer", "-F", dst).CombinedOutput(); err != nil {
		return fmt.Errorf("mkfs.ext4: %w: %s", err, out)
	}
	return os.Chmod(dst, 0o644)
}

func dedupPark(t *testing.T, m *Manager, store *storage.LocalStorageBackend, fcVersion, dir string, app dedupApp, img dedupImages) *dedupCapture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	instance := "dedup-" + app.name
	t.Cleanup(func() { _ = m.Destroy(context.Background(), instance) })
	cold, err := m.ColdBoot(ctx, ColdBootRequest{
		Instance: instance, Plan: "pro", BaseKey: img.base, LayerKey: img.layer, VcpuCount: 2, MemSizeMiB: app.memMiB,
	})
	if err != nil {
		t.Fatalf("%s: cold boot: %v", app.name, err)
	}
	url := fmt.Sprintf("http://%s:8080", cold.Lease.HostIP)
	hello, err := dedupGet(url+"/", 60*time.Second)
	if err != nil {
		t.Fatalf("%s: first request: %v", app.name, err)
	}
	for range 5 {
		if _, err := dedupGet(url+"/work", 30*time.Second); err != nil {
			t.Fatalf("%s: work request: %v", app.name, err)
		}
	}
	// An app parks after its idle timeout; free page reporting runs 2 s
	// after a free.
	time.Sleep(10 * time.Second)
	prefix := "snap/dedup/captures/" + app.name + "/v2/"
	snap := &Snapshot{
		FCVersion:         fcVersion,
		StorageKey:        prefix + "mem",
		VMStateStorageKey: prefix + "vmstate",
		VMStatePath:       filepath.Join(dir, app.name+".vmstate"),
	}
	if _, err := m.Park(ctx, instance, SnapshotSpec{
		VMStatePath: snap.VMStatePath, StorageKey: snap.StorageKey, VMStateStorageKey: snap.VMStateStorageKey,
	}); err != nil {
		t.Fatalf("%s: park: %v", app.name, err)
	}
	c := &dedupCapture{app: app, images: img, snap: snap}
	var ok bool
	if c.mem, ok, err = store.LocalPath(snap.StorageKey); err != nil || !ok {
		t.Fatalf("%s: resolve memory file: ok=%v err=%v", app.name, ok, err)
	}
	driveKey := state.SnapshotDriveKey(state.Snapshot{StorageKey: snap.StorageKey})
	if c.drive, ok, err = store.LocalPath(driveKey); err != nil || !ok {
		t.Fatalf("%s: resolve drive %s: ok=%v err=%v", app.name, driveKey, ok, err)
	}
	c.memOrig = filepath.Join(dir, app.name+".mem.orig")
	c.driveOrig = filepath.Join(dir, app.name+".drive.orig")
	dedupCopySparse(t, c.mem, c.memOrig)
	dedupCopySparse(t, c.drive, c.driveOrig)
	c.memSize, _ = fileSizes(t, c.mem)
	c.memNonZero = nonZeroPages(t, c.mem)
	c.driveSize, c.driveAlloc = fileSizes(t, c.drive)
	t.Logf("RESULT park %s (%s, %d MiB, heap %d MB): app says %q; memory %d MiB logical, %d MiB non-zero, %.1f MB incompressible pages; drive %d MiB logical, %d MiB allocated",
		app.name, app.runtime, app.memMiB, app.heapMB, strings.TrimSpace(hello), c.memSize>>20, c.memNonZero>>20, mb(dedupHighEntropy(t, c.memOrig)), c.driveSize>>20, c.driveAlloc>>20)
	return c
}

// ---- storage ----

type chunkLoc struct {
	file string
	off  int64
}

// dedupStore is a fleet chunk store at one chunk size: every distinct
// non-zero chunk is kept once, in pack (new chunks) or in an image file
// (chunks the images already hold).
type dedupStore struct {
	size  int
	locs  map[[32]byte]chunkLoc
	pack  *os.File
	packN int64
}

type dedupCounts struct{ zero, image, fleet, self, fresh int64 }

func newDedupStore(t *testing.T, dir string, size int, imagePaths []string) *dedupStore {
	t.Helper()
	pack, err := os.Create(filepath.Join(dir, fmt.Sprintf("pack-%d", size)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pack.Close() })
	s := &dedupStore{size: size, locs: map[[32]byte]chunkLoc{}, pack: pack}
	for _, p := range imagePaths {
		dedupForEachChunk(t, p, size, func(off int64, chunk []byte) {
			h := sha256.Sum256(chunk)
			if _, ok := s.locs[h]; !ok {
				s.locs[h] = chunkLoc{file: p, off: off}
			}
		})
	}
	return s
}

// add files a memory file's chunks into the store and returns how they
// split, plus the new chunks' bytes in file order (the pack an upload of
// this snapshot would carry).
func (s *dedupStore) add(t *testing.T, path string) (dedupCounts, []byte) {
	t.Helper()
	var c dedupCounts
	var fresh bytes.Buffer
	seen := map[[32]byte]bool{}
	dedupForEachChunkWithZero(t, path, s.size, func(off int64, chunk []byte, zero bool) {
		n := int64(len(chunk))
		if zero {
			c.zero += n
			return
		}
		h := sha256.Sum256(chunk)
		if loc, ok := s.locs[h]; ok {
			switch {
			case loc.file != s.pack.Name():
				c.image += n
			case seen[h]:
				c.self += n
			default:
				c.fleet += n
			}
			return
		}
		seen[h] = true
		if _, err := s.pack.WriteAt(chunk, s.packN); err != nil {
			t.Fatal(err)
		}
		s.locs[h] = chunkLoc{file: s.pack.Name(), off: s.packN}
		s.packN += n
		c.fresh += n
		fresh.Write(chunk)
	})
	return c, fresh.Bytes()
}

func dedupStorageReport(t *testing.T, captures []*dedupCapture, images map[string]dedupImages, chunkSizes []int) {
	t.Helper()
	var imagePaths []string
	for _, img := range images {
		imagePaths = append(imagePaths, img.base, img.layer)
	}
	slices.Sort(imagePaths)
	dir := filepath.Dir(captures[0].memOrig)

	var todayTotal int64
	for _, c := range captures {
		fast := dedupCompressFile(t, c.memOrig, zstd.SpeedFastest)
		todayTotal += fast
		t.Logf("RESULT today %s: memory non-zero %.1f MB -> object %.1f MB (zstd fastest, as published)",
			c.app.name, mb(c.memNonZero), mb(fast))
	}
	t.Logf("RESULT today total: %.1f MB memory objects for %d snapshots (%.1f MB avg)",
		mb(todayTotal), len(captures), mb(todayTotal)/float64(len(captures)))

	for _, size := range chunkSizes {
		s := newDedupStore(t, dir, size, imagePaths)
		var total, totalDefault int64
		var marginal []float64
		firstOfRuntime := map[string]bool{}
		for _, c := range captures {
			counts, fresh := s.add(t, c.memOrig)
			fast := dedupCompressBytes(t, fresh, zstd.SpeedFastest)
			def := dedupCompressBytes(t, fresh, zstd.SpeedDefault)
			total += fast
			totalDefault += def
			if firstOfRuntime[c.app.runtime] {
				marginal = append(marginal, mb(def))
			}
			firstOfRuntime[c.app.runtime] = true
			t.Logf("RESULT chunk %dK %s: in images %.1f MB, in fleet %.1f MB, repeated in itself %.1f MB, new %.1f MB -> stored %.1f MB fastest / %.1f MB default",
				size>>10, c.app.name, mb(counts.image), mb(counts.fleet), mb(counts.self), mb(counts.fresh), mb(fast), mb(def))
		}
		slices.Sort(marginal)
		med := 0.0
		if len(marginal) > 0 {
			med = marginal[len(marginal)/2]
		}
		t.Logf("RESULT chunk %dK total: %.1f MB fastest / %.1f MB default for %d snapshots (today %.1f MB, %.0f%% saved); median unique per app after the runtime's first %.1f MB (default)",
			size>>10, mb(total), mb(totalDefault), len(captures), mb(todayTotal), 100*(1-float64(totalDefault)/float64(todayTotal)), med)
		_ = s.pack.Close()
		_ = os.Remove(s.pack.Name())
	}
}

func dedupDriveReport(t *testing.T, captures []*dedupCapture) {
	t.Helper()
	for _, c := range captures {
		full := dedupCompressFile(t, c.driveOrig, zstd.SpeedFastest)
		delta := dedupDriveDelta(t, c.images.layer, c.driveOrig)
		deltaZ := dedupCompressBytes(t, delta.bytes, zstd.SpeedFastest)
		t.Logf("RESULT drive %s: full object %.1f MB (zstd fastest, as published); delta vs layer %d blocks %.2f MB -> %.2f MB",
			c.app.name, mb(full), len(delta.offsets), mb(int64(len(delta.bytes))), mb(deltaZ))
	}
}

type driveDelta struct {
	offsets []int64
	bytes   []byte
}

// dedupDriveDelta lists the 4 KiB blocks of drive that differ from layer
// (the deployment's app layer the drive started as).
func dedupDriveDelta(t *testing.T, layer, drive string) driveDelta {
	t.Helper()
	l, err := os.ReadFile(layer) //nolint:forbidigo // test-owned fixture.
	if err != nil {
		t.Fatal(err)
	}
	d, err := os.ReadFile(drive) //nolint:forbidigo // test-owned fixture.
	if err != nil {
		t.Fatal(err)
	}
	var out driveDelta
	for off := 0; off < len(d); off += 4096 {
		end := min(off+4096, len(d))
		var lb []byte
		if off < len(l) {
			lb = l[off:min(end, len(l))]
		}
		if !bytes.Equal(d[off:end], lb) && !(len(lb) == 0 && isZero(d[off:end])) {
			out.offsets = append(out.offsets, int64(off))
			out.bytes = append(out.bytes, d[off:end]...)
		}
	}
	return out
}

// ---- restore ----

func dedupRestoreReport(ctx context.Context, t *testing.T, m *Manager, dir string, captures []*dedupCapture, idx int,
	images map[string]dedupImages, chunkSizes []int, restores int) {
	t.Helper()
	target := captures[idx]
	label := target.app.name
	want := fileDigest(t, target.memOrig)
	var imagePaths []string
	for _, img := range images {
		imagePaths = append(imagePaths, img.base, img.layer)
	}
	slices.Sort(imagePaths)

	measure := func(variant string) {
		if got := fileDigest(t, target.mem); got != want {
			t.Fatalf("%s %s: memory file bytes differ from the capture", label, variant)
		}
		t.Logf("RESULT restore %s %s layout: %s", label, variant, dedupFilefrag(target.mem))
		for _, cold := range []bool{true, false} {
			var wakes, firsts []time.Duration
			fallbacks := 0
			for range restores {
				w, f, ok := dedupWake(ctx, t, m, target, cold)
				if !ok {
					fallbacks++
					continue
				}
				wakes, firsts = append(wakes, w), append(firsts, f)
			}
			if len(wakes) == 0 {
				t.Logf("RESULT restore %s %s: every restore fell back to a cold boot", label, variant)
				continue
			}
			slices.Sort(wakes)
			slices.Sort(firsts)
			kind := "warm"
			if cold {
				kind = "cold"
			}
			t.Logf("RESULT restore %s %s %s: wake p50 %s min %s max %s; first request p50 %s (n=%d, %d fell back to cold boot)",
				label, variant, kind, wakes[len(wakes)/2].Round(time.Millisecond), wakes[0].Round(time.Millisecond),
				wakes[len(wakes)-1].Round(time.Millisecond), firsts[len(firsts)/2].Round(time.Millisecond), len(wakes), fallbacks)
		}
	}
	replace := func(src string) {
		tmp := target.mem + ".next"
		dedupCopySparse(t, src, tmp)
		if err := os.Rename(tmp, target.mem); err != nil {
			t.Fatal(err)
		}
	}

	// 1. As captured.
	replace(target.memOrig)
	measure("captured")

	// 2. As production shares it today: image pass, then peer pass over
	// every other memory file of the fleet.
	var peers []string
	for i, c := range captures {
		if i != idx {
			peers = append(peers, c.memOrig)
		}
	}
	replace(target.memOrig)
	img, err := storage.ShareMemoryWithImages(ctx, target.mem, []string{target.images.layer, target.images.base})
	if errors.Is(err, storage.ErrBlockSharingUnsupported) {
		t.Skipf("%s cannot share blocks; run on XFS with reflink=1", dir)
	}
	if err != nil {
		t.Fatalf("share with images: %v", err)
	}
	t.Logf("RESULT restore %s today-images: %.1f MB shared with images", label, mb(img))
	measure("today-images")
	peer, err := storage.ShareMemoryWithPeers(ctx, target.mem, peers)
	if err != nil {
		t.Fatalf("share with peers: %v", err)
	}
	t.Logf("RESULT restore %s today: %.1f MB shared with images, %.1f MB with peers", label, mb(img), mb(peer))
	measure("today-shared")

	// 3. Assembled from a local chunk store by reflink, one clone per run of
	// chunks that sit back to back in the store. The assembled file is
	// renamed into place, so the restore reads that layout.
	for _, size := range chunkSizes {
		s := newDedupStore(t, dir, size, imagePaths)
		for i, c := range captures {
			if i != idx {
				s.add(t, c.memOrig)
			}
		}
		_, fresh := s.add(t, target.memOrig)
		runs := dedupManifest(t, s, target.memOrig)
		assembled := target.mem + ".next"
		dropPageCache(t)
		start := time.Now()
		dedupAssembleRuns(t, runs, target.memSize, assembled)
		took := time.Since(start)
		if err := os.Rename(assembled, target.mem); err != nil {
			t.Fatal(err)
		}
		t.Logf("RESULT restore %s chunk %dK: assembled from the store in %s with %d clones (%.1f MB of its chunks were new to the store)",
			label, size>>10, took.Round(time.Millisecond), len(runs), mb(int64(len(fresh))))
		measure(fmt.Sprintf("chunk-%dK", size>>10))
		assembled = filepath.Join(dir, label+".assembled")

		// Cache miss: today's object is decoded whole; with a chunk store
		// the node fetches and decodes only the chunks it lacks, then
		// assembles. Local work only; transfer is bytes / link speed.
		obj := filepath.Join(dir, label+".obj.zst")
		dedupWriteCompressedFile(t, target.memOrig, obj)
		objInfo, _ := os.Stat(obj)
		dropPageCache(t)
		start = time.Now()
		dedupDecodeTo(t, obj, filepath.Join(dir, label+".decoded"), target.memSize)
		todayMiss := time.Since(start)
		freshZ := dedupCompressBytes(t, fresh, zstd.SpeedFastest)
		dropPageCache(t)
		start = time.Now()
		// The fetched chunks land in the store (written and synced, as an
		// append to the pack would be), then the file is assembled.
		landed := filepath.Join(dir, label+".landed")
		if err := os.WriteFile(landed, dedupDecodeBytes(t, dedupCompressBytesRaw(t, fresh)), 0o600); err != nil {
			t.Fatal(err)
		}
		if lf, err := os.OpenFile(landed, os.O_RDWR, 0); err == nil { //nolint:forbidigo // test-owned fixture.
			_ = lf.Sync()
			_ = lf.Close()
		}
		dedupAssembleRuns(t, runs, target.memSize, assembled)
		_ = os.Remove(landed)
		chunkMiss := time.Since(start)
		_ = os.Remove(assembled)
		_ = os.Remove(obj)
		_ = os.Remove(filepath.Join(dir, label+".decoded"))
		t.Logf("RESULT miss %s chunk %dK: today fetch %.1f MB + decode %s; chunked fetch %.1f MB + decode/assemble %s",
			label, size>>10, mb(objInfo.Size()), todayMiss.Round(time.Millisecond), mb(freshZ), chunkMiss.Round(time.Millisecond))
		_ = s.pack.Close()
		_ = os.Remove(s.pack.Name())
	}

	// 4. Drive rebuilt from the layer and the delta, memory as captured.
	replace(target.memOrig)
	delta := dedupDriveDelta(t, target.images.layer, target.driveOrig)
	start := time.Now()
	rebuilt := target.drive + ".next"
	dedupCloneFile(t, target.images.layer, rebuilt)
	f, err := os.OpenFile(rebuilt, os.O_WRONLY, 0) //nolint:forbidigo // test-owned fixture.
	if err != nil {
		t.Fatal(err)
	}
	for i, off := range delta.offsets {
		if _, err := f.WriteAt(delta.bytes[i*4096:min((i+1)*4096, len(delta.bytes))], off); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Truncate(target.driveSize); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	took := time.Since(start)
	if fileDigest(t, rebuilt) != fileDigest(t, target.driveOrig) {
		t.Fatalf("%s: drive rebuilt from layer + delta differs from the capture", label)
	}
	if err := os.Rename(rebuilt, target.drive); err != nil {
		t.Fatal(err)
	}
	t.Logf("RESULT restore %s drive-delta: rebuilt drive from layer + %d blocks in %s", label, len(delta.offsets), took.Round(time.Millisecond))
	measure("drive-delta")
	dedupCopySparse(t, target.driveOrig, target.drive+".next")
	if err := os.Rename(target.drive+".next", target.drive); err != nil {
		t.Fatal(err)
	}
}

// dedupWake restores c once; ok is false when the wake fell back to a cold
// boot (the restore failed), which is reported, not fatal.
func dedupWake(ctx context.Context, t *testing.T, m *Manager, c *dedupCapture, cold bool) (time.Duration, time.Duration, bool) {
	t.Helper()
	if cold {
		dropPageCache(t)
	}
	instance := "dedup-" + c.app.name
	start := time.Now()
	inst, err := m.Wake(ctx, WakeRequest{
		Instance: instance, Plan: "pro", BaseKey: c.images.base, LayerKey: c.images.layer,
		VcpuCount: 2, MemSizeMiB: c.app.memMiB, Snapshot: c.snap,
	})
	took := time.Since(start)
	if err != nil {
		t.Fatalf("%s: wake: %v", c.app.name, err)
	}
	if inst.Method != WakeRestore {
		t.Logf("%s: woke by %s after %s, not a snapshot restore", c.app.name, inst.Method, took.Round(time.Millisecond))
		if err := m.Destroy(ctx, instance); err != nil {
			t.Fatalf("%s: destroy after cold boot: %v", c.app.name, err)
		}
		return took, 0, false
	}
	fs := time.Now()
	first := time.Duration(-1)
	if _, err := dedupGet(fmt.Sprintf("http://%s:8080/work", inst.Lease.HostIP), 30*time.Second); err != nil {
		t.Logf("%s: first request after restore: %v", c.app.name, err)
	} else {
		first = time.Since(fs)
	}
	if err := m.Destroy(ctx, instance); err != nil {
		t.Fatalf("%s: destroy after wake: %v", c.app.name, err)
	}
	return took, first, true
}

type dedupRun struct {
	file          string
	srcOff, dstOf int64
	n             int64
}

// dedupManifest lists, for path's non-zero chunks, where the store holds
// each one, merging chunks adjacent in both files into one run: what a
// snapshot's chunk manifest resolves to on a node.
func dedupManifest(t *testing.T, s *dedupStore, path string) []dedupRun {
	t.Helper()
	var runs []dedupRun
	dedupForEachChunkWithZero(t, path, s.size, func(off int64, chunk []byte, zero bool) {
		if zero {
			return
		}
		if len(chunk)%4096 != 0 {
			t.Fatalf("%s: size is not a multiple of 4 KiB", path)
		}
		loc, ok := s.locs[sha256.Sum256(chunk)]
		if !ok {
			t.Fatalf("chunk at %d is not in the store", off)
		}
		n := int64(len(chunk))
		if k := len(runs) - 1; k >= 0 && runs[k].file == loc.file && runs[k].srcOff+runs[k].n == loc.off && runs[k].dstOf+runs[k].n == off {
			runs[k].n += n
			return
		}
		runs = append(runs, dedupRun{file: loc.file, srcOff: loc.off, dstOf: off, n: n})
	})
	return runs
}

// dedupAssembleRuns builds dst (size bytes) with one reflink clone per run.
func dedupAssembleRuns(t *testing.T, runs []dedupRun, size int64, dst string) {
	t.Helper()
	out, err := os.OpenFile(dst, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600) //nolint:forbidigo // test-owned fixture.
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = out.Close() }()
	if err := out.Truncate(size); err != nil {
		t.Fatal(err)
	}
	srcs := map[string]*os.File{}
	defer func() {
		for _, f := range srcs {
			_ = f.Close()
		}
	}()
	for _, r := range runs {
		src, ok := srcs[r.file]
		if !ok {
			src, err = os.Open(r.file) //nolint:forbidigo // test-owned fixture.
			if err != nil {
				t.Fatal(err)
			}
			srcs[r.file] = src
		}
		if err := unix.IoctlFileCloneRange(int(out.Fd()), &unix.FileCloneRange{
			Src_fd: int64(src.Fd()), Src_offset: uint64(r.srcOff), Src_length: uint64(r.n), Dest_offset: uint64(r.dstOf),
		}); err != nil {
			t.Fatalf("clone %s@%d -> %d (+%d): %v", r.file, r.srcOff, r.dstOf, r.n, err)
		}
	}
	if err := out.Sync(); err != nil {
		t.Fatal(err)
	}
}

// dedupHighEntropy returns the bytes of path's 4 KiB pages that zstd cannot
// shrink below 90%: incompressible data such as random heap buffers.
func dedupHighEntropy(t *testing.T, path string) int64 {
	t.Helper()
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedFastest), zstd.WithEncoderConcurrency(1))
	if err != nil {
		t.Fatal(err)
	}
	var n int64
	var dst []byte
	dedupForEachChunk(t, path, 4096, func(_ int64, page []byte) {
		dst = enc.EncodeAll(page, dst[:0])
		if len(dst)*10 >= len(page)*9 {
			n += int64(len(page))
		}
	})
	return n
}

// ---- helpers ----

func dedupForEachChunk(t *testing.T, path string, size int, fn func(off int64, chunk []byte)) {
	dedupForEachChunkWithZero(t, path, size, func(off int64, chunk []byte, zero bool) {
		if !zero {
			fn(off, chunk)
		}
	})
}

func dedupForEachChunkWithZero(t *testing.T, path string, size int, fn func(off int64, chunk []byte, zero bool)) {
	t.Helper()
	f, err := os.Open(path) //nolint:forbidigo // test-owned fixture.
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, size)
	for off := int64(0); ; off += int64(size) {
		n, err := io.ReadFull(f, buf)
		if n > 0 {
			fn(off, buf[:n], isZero(buf[:n]))
		}
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}

func isZero(b []byte) bool {
	for _, c := range b {
		if c != 0 {
			return false
		}
	}
	return true
}

// dedupCompressFile returns the zstd size of path's non-zero 4 KiB blocks,
// which is what the extent encoding stores (holes and zero blocks cost
// nothing but the header).
func dedupCompressFile(t *testing.T, path string, level zstd.EncoderLevel) int64 {
	t.Helper()
	var cw countWriter
	enc, err := zstd.NewWriter(&cw, zstd.WithEncoderLevel(level), zstd.WithEncoderConcurrency(1))
	if err != nil {
		t.Fatal(err)
	}
	dedupForEachChunk(t, path, 1<<20, func(_ int64, chunk []byte) {
		for i := 0; i < len(chunk); i += 4096 {
			b := chunk[i:min(i+4096, len(chunk))]
			if !isZero(b) {
				_, _ = enc.Write(b)
			}
		}
	})
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	return cw.n
}

func dedupWriteCompressedFile(t *testing.T, path, dst string) {
	t.Helper()
	out, err := os.Create(dst)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := zstd.NewWriter(out, zstd.WithEncoderLevel(zstd.SpeedFastest), zstd.WithEncoderConcurrency(1))
	if err != nil {
		t.Fatal(err)
	}
	// Offsets are implicit in this simplified encoding: whole file, so the
	// decode below does work proportional to the data plus the zeros the
	// extent encoding would skip; zero 1 MiB runs are skipped to match it.
	dedupForEachChunkWithZero(t, path, 1<<20, func(_ int64, chunk []byte, zero bool) {
		if !zero {
			_, _ = enc.Write(chunk)
		}
	})
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}

func dedupDecodeTo(t *testing.T, obj, dst string, size int64) {
	t.Helper()
	in, err := os.Open(obj) //nolint:forbidigo // test-owned fixture.
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = in.Close() }()
	dec, err := zstd.NewReader(in, zstd.WithDecoderConcurrency(1))
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()
	out, err := os.Create(dst)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, dec); err != nil {
		t.Fatal(err)
	}
	if err := out.Truncate(size); err != nil {
		t.Fatal(err)
	}
	if err := out.Sync(); err != nil {
		t.Fatal(err)
	}
	_ = out.Close()
}

func dedupCompressBytes(t *testing.T, b []byte, level zstd.EncoderLevel) int64 {
	t.Helper()
	if len(b) == 0 {
		return 0
	}
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(level), zstd.WithEncoderConcurrency(1))
	if err != nil {
		t.Fatal(err)
	}
	return int64(len(enc.EncodeAll(b, nil)))
}

func dedupCompressBytesRaw(t *testing.T, b []byte) []byte {
	t.Helper()
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedFastest), zstd.WithEncoderConcurrency(1))
	if err != nil {
		t.Fatal(err)
	}
	return enc.EncodeAll(b, nil)
}

func dedupDecodeBytes(t *testing.T, b []byte) []byte {
	t.Helper()
	dec, err := zstd.NewReader(nil, zstd.WithDecoderConcurrency(1))
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()
	out, err := dec.DecodeAll(b, nil)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

type countWriter struct{ n int64 }

func (c *countWriter) Write(p []byte) (int, error) { c.n += int64(len(p)); return len(p), nil }

// dedupCopySparse copies src to dst without sharing blocks, keeping holes.
func dedupCopySparse(t *testing.T, src, dst string) {
	t.Helper()
	_ = os.Remove(dst)
	if out, err := exec.Command("cp", "--sparse=always", "--reflink=never", src, dst).CombinedOutput(); err != nil {
		t.Fatalf("copy %s -> %s: %v: %s", src, dst, err, out)
	}
}

func dedupCloneFile(t *testing.T, src, dst string) {
	t.Helper()
	_ = os.Remove(dst)
	if out, err := exec.Command("cp", "--reflink=always", src, dst).CombinedOutput(); err != nil {
		t.Fatalf("clone %s -> %s: %v: %s", src, dst, err, out)
	}
}

func dedupFilefrag(path string) string {
	out, err := exec.Command("filefrag", path).CombinedOutput()
	if err != nil {
		return "filefrag unavailable: " + err.Error()
	}
	return strings.TrimSpace(string(out))
}

func dedupGet(url string, timeout time.Duration) (string, error) {
	client := &http.Client{Timeout: timeout, Transport: &http.Transport{DisableKeepAlives: true}}
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d: %v: %q", resp.StatusCode, err, body)
	}
	return string(body), nil
}

func mb(n int64) float64 { return float64(n) / 1e6 }

// dedupNodeApp reads its libraries and app code (page cache), builds a heap
// of half seeded random bytes and half small objects, and serves /work.
const dedupNodeApp = `const http = require("http"), fs = require("fs"), path = require("path"), crypto = require("crypto");
const seed = Number(process.env.SEED || 1), heapMB = Number(process.env.HEAP_MB || 16);
let files = 0, depBytes = 0;
function walk(d) {
  for (const e of fs.readdirSync(d, { withFileTypes: true })) {
    const p = path.join(d, e.name);
    if (e.isDirectory()) walk(p);
    else if (e.isFile()) { depBytes += fs.readFileSync(p).length; files++; }
  }
}
try { walk("/srv/deps"); } catch (e) {}
let x = seed >>> 0 || 1;
function rnd() { x ^= x << 13; x >>>= 0; x ^= x >>> 17; x ^= x << 5; x >>>= 0; return x; }
const bufs = [];
for (let i = 0; i < heapMB / 2; i++) { const b = Buffer.allocUnsafe(1 << 20); for (let j = 0; j < b.length; j += 4) b.writeUInt32LE(rnd(), j); bufs.push(b); }
const objs = []; let bytes = 0;
while (bytes < (heapMB / 2) * 1048576) {
  objs.push({ id: rnd(), name: "user-" + rnd().toString(36), email: "u" + rnd() + "@example.com", tags: [rnd() % 100, rnd() % 100], created: new Date(rnd() * 1000).toISOString() });
  bytes += 200;
}
http.createServer((req, res) => {
  if (req.url === "/work") {
    const h = crypto.createHash("sha256");
    for (let i = 0; i < 200; i++) h.update(JSON.stringify(objs[rnd() % objs.length]));
    res.end(h.digest("hex"));
    return;
  }
  res.end("ok files=" + files + " deps=" + depBytes + " objs=" + objs.length);
}).listen(8080, "0.0.0.0");
`

const dedupPythonApp = `import http.server, os, random, json, hashlib
seed = int(os.environ.get("SEED", "1")); heap = int(os.environ.get("HEAP_MB", "16"))
r = random.Random(seed)
files = 0; dep_bytes = 0
for root, _, names in os.walk("/srv/deps"):
    for n in names:
        try:
            with open(os.path.join(root, n), "rb") as fh:
                dep_bytes += len(fh.read()); files += 1
        except Exception:
            pass
blobs = [r.randbytes(1 << 20) for _ in range(heap // 2)]
objs = []; size = 0
while size < (heap // 2) * 1048576:
    objs.append({"id": r.getrandbits(32), "name": "user-%x" % r.getrandbits(40), "email": "u%d@example.com" % r.getrandbits(32), "tags": [r.randrange(100), r.randrange(100)]})
    size += 400
class H(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path == "/work":
            h = hashlib.sha256()
            for _ in range(200):
                h.update(json.dumps(objs[r.randrange(len(objs))]).encode())
            body = h.hexdigest().encode()
        else:
            body = ("ok files=%d deps=%d objs=%d" % (files, dep_bytes, len(objs))).encode()
        self.send_response(200); self.send_header("content-length", str(len(body))); self.end_headers(); self.wfile.write(body)
    def log_message(self, *a):
        pass
http.server.ThreadingHTTPServer(("0.0.0.0", 8080), H).serve_forever()
`
