package rootfs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/storage"
)

// BaseBuildInput is one base-image provisioning run.
//
// imaged startup calls BuildBase once per box lifetime to convert
// ghcr.io/poyrazk/builder-base:latest (or the configured override) into
// /srv/fc/base/builder-base.ext4 — the read-only drive0 used by builder
// microVMs (spec §4.6, two-drive scheme). The OCI image may contain
// guest-init, but the produced drive0 must own the boot contract: Linux
// executes /sbin/init from drive0 before the per-app drive1 overlay can be
// mounted. BuildBase refreshes that binary when GuestInitPath is supplied.
// It never injects app.json or a plan cap.
//
// Like BuildInput, the produced ext4 is published via Storage + StorageKey
// (production) or OutImage (legacy / integration test). Exactly one must
// be set — see Builder.validateOutputTarget for the validation rules.
type BaseBuildInput struct {
	// Layers are ALL layers of the base OCI image, bottom-to-top,
	// gzip-compressed (same wire shape as Builders expect from
	// oci.RegistryClient.PullBlob). Empty when BuildBaseFromStaging
	// is invoked with a pre-populated staging dir (ADR-053 parent-ref
	// path: the staging dir is filled by vmmd's parent materialization RPC
	// + delta layer apply loop, not by BuildBase itself).
	Layers []io.Reader
	// Storage is the artifact backend the produced ext4 is Put into.
	// Mutually exclusive with OutImage.
	Storage storage.StorageBackend
	// StorageKey is the key the produced ext4 is published under, e.g.
	// "base/<runtime>.ext4". Mutually exclusive with OutImage.
	StorageKey string
	// OutImage is the legacy on-disk target, e.g.
	// "/srv/fc/base/builder-base.ext4". Kept for the integration test;
	// production wiring uses Storage + StorageKey.
	OutImage string
	// GuestInitPath is the static PID-1 binary to install as /sbin/init in
	// the produced base artifact. Optional for legacy callers and tests;
	// production imaged supplies the arch-matched guest-init binary.
	GuestInitPath string
}

// BaseBuildResult reports the produced base image.
type BaseBuildResult struct {
	ImageKey  string // set when Storage + StorageKey was used
	ImagePath string // set when OutImage was used
	SizeBytes int64
}

// MkdirBaseStaging creates a fresh temp dir for a base-image staging
// tree. Exported (ADR-053) so pkg/imaged's parent-ref path can
// pre-create the dir, populate it via vmmd's parent materialization RPC
// + a delta layer apply loop, then hand it to
// BuildBaseFromStaging for mkfs. The dir is NOT auto-removed: callers
// running BuildBaseFromStaging rely on this function's sibling-temp
// pattern (mirrors publishExt4 / publishBaseExt4). Callers that want
// automatic cleanup should defer os.RemoveAll on the returned path.
//
// Staging root: when FAAS_BASE_STAGING_ROOT is set, the staging dir
// is created under that root (MkdirAll'd with 0755 so a fresh
// /dev/shm/faas-base-staging works on a reboot); otherwise the
// default `os.MkdirTemp("", ...)` is used. The cd-controlplane EX44
// ships the env var pointing at /dev/shm/faas-base-staging in
// deploy/systemd/faas-imaged.service — host /tmp is ext4 there and the
// kernel rejects overlay mounts whose upper fs doesn't support
// tmpfile with `overlayfs: upper fs does not support tmpfile`
// (parent-ref overlay mount crash-loop, 2026-08-04 → 2026-08-05;
// every cd-controlplane deploy failed for 24h). Tests stay portable
// on macOS dev units by leaving the env var unset.
func MkdirBaseStaging() (string, error) {
	return mkdirBaseTemp("FAAS_BASE_STAGING_ROOT")
}

func MkdirBaseExtraction() (string, error) {
	return mkdirBaseTemp("FAAS_BASE_EXTRACT_ROOT")
}

func mkdirBaseTemp(envKey string) (string, error) {
	root := os.Getenv(envKey)
	if root == "" {
		return os.MkdirTemp("", "faas-base-*")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", fmt.Errorf("rootfs: staging root %s: %w", root, err)
	}
	return os.MkdirTemp(root, "faas-base-*")
}

// BuildBase assembles a base-image ext4 from the supplied OCI layers.
//
// Differences from Builder.Build (this is the inverse per §4.6):
//
//   - ALL layers are applied (no LayersAboveBase filter). The base is the
//     root, not a delta.
//   - No /etc/faas/app.json is injected (a base has no app).
//   - Guest-init is refreshed as /sbin/init when GuestInitPath is set. This
//     is required because PID 1 executes from drive0 before drive1 mounts.
//   - No plan / app-layer cap (the base is shared, not per-app).
//
// On error the staging dir is removed before returning. The caller owns
// the layer readers and is responsible for closing them — mirroring the
// Builder.Build contract (cmd/imaged closes them above the call).
func (b *Builder) BuildBase(ctx context.Context, in BaseBuildInput) (BaseBuildResult, error) {
	if err := validateBaseOutputTarget(in); err != nil {
		return BaseBuildResult{}, err
	}
	if len(in.Layers) == 0 {
		return BaseBuildResult{}, fmt.Errorf("rootfs: BuildBase: no layers")
	}

	staging, err := MkdirBaseExtraction()
	if err != nil {
		return BaseBuildResult{}, fmt.Errorf("rootfs: staging dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(staging) }()

	for i, layer := range in.Layers {
		if err := ApplyLayerGz(staging, layer); err != nil {
			return BaseBuildResult{}, fmt.Errorf("rootfs: apply base layer %d: %w", i, err)
		}
	}
	if err := injectBaseGuestInit(staging, in.GuestInitPath); err != nil {
		return BaseBuildResult{}, err
	}
	if err := ensureBaseMountpoints(staging); err != nil {
		return BaseBuildResult{}, err
	}

	return b.buildBaseFromStaging(ctx, staging, in)
}

// BuildBaseFromStaging (ADR-053) is the seam used by the parent-ref
// staging path: imaged pre-populates `staging` with the parent's tree
// (materialized by vmmd) + the delta OCI layers, then calls
// BuildBaseFromStaging to mkfs + publish.
//
// `staging` MUST be a path returned by MkdirBaseStaging (or otherwise
// outside the published ext4's directory, to avoid mkfs's -d flag
// copying its own growing output back into itself — see
// publishBaseExt4's sibling-temp pattern, this comment is mirrored
// there).
//
// `in.Storage` / `in.StorageKey` / `in.OutImage` follow the same
// exclusive-or rules as BaseBuildInput. The function does NOT touch
// in.Layers (those are zero in the parent-ref path).
//
// Cleanup of `staging` is the caller's responsibility: BuildBaseFromStaging
// does not RemoveAll it (matches the "caller owns the staging tree"
// contract the parent-ref path needs — imaged cleans up after publishing).
func (b *Builder) BuildBaseFromStaging(ctx context.Context, staging string, in BaseBuildInput) (BaseBuildResult, error) {
	if err := validateBaseOutputTarget(in); err != nil {
		return BaseBuildResult{}, err
	}
	if staging == "" {
		return BaseBuildResult{}, fmt.Errorf("rootfs: BuildBaseFromStaging: empty staging dir")
	}
	if _, err := os.Stat(staging); err != nil {
		return BaseBuildResult{}, fmt.Errorf("rootfs: BuildBaseFromStaging: stat staging: %w", err)
	}
	if err := injectBaseGuestInit(staging, in.GuestInitPath); err != nil {
		return BaseBuildResult{}, err
	}
	if err := ensureBaseMountpoints(staging); err != nil {
		return BaseBuildResult{}, err
	}
	return b.buildBaseFromStaging(ctx, staging, in)
}

// injectBaseGuestInit installs PID 1 in the boot root. It is optional so
// low-level BuildBase tests and legacy callers remain independent of a guest
// binary path.
func injectBaseGuestInit(staging, guestInitPath string) error {
	if guestInitPath == "" {
		return nil
	}
	if err := InjectGuestInit(staging, guestInitPath); err != nil {
		return fmt.Errorf("rootfs: inject base guest-init: %w", err)
	}
	return nil
}

// BuildFullRootfsInput is the per-deployment input for the
// full-rootfs build path (ADR-141 §Decision 1). It mirrors
// BuildInput but consumes the app image's ALL layers (no
// LayersAboveBase) and threads a Resolver through layer-apply
// so named users (distroless / alpine / scratch USER=...) land on
// the image's declared uid rather than uid 0.
type BuildFullRootfsInput struct {
	Layers              []io.Reader
	Manifest            api.AppManifest
	GuestInitPath       string
	Plan                api.Plan
	Storage             storage.StorageBackend
	StorageKey          string
	OutImage            string
	TarballPath         string
	FunctionHandlerPath string
	FunctionRunnerPath  string
	SBOMRun             func(ctx context.Context, dir string) ([]byte, error)
	SBOMStorageKey      string
	// Resolver is consulted by ApplyLayerGzWithResolver during the
	// per-entry chown path. Commit 5 lays the plumbing; commit 7
	// wires the real image-/etc/passwd parser + merge walk.
	//
	// The commit-7 implementation grows its OWN per-layer resolver
	// from the staging /etc/passwd after each apply (top-most-
	// wins merge), so the supplied `Resolver` field is currently
	// a no-op on the BuildFullRootfs path. It is preserved on
	// the wire so a future caller (per-customer override, ADR-053
	// fourth axis) can layer additional entries on top of the
	// merged map without rewriting the merge walk.
	Resolver Resolver
}

// BuildFullRootfs assembles a self-contained ext4 rootfs from ALL
// of the app image's layers (ADR-141 §Decision 1). It bypasses
// the two-drive shared-base path entirely — every per-app ext4
// carries the image's full rootfs (alpine ~40 MB, distroless ~3 MB,
// scratch ~0 MB), and the produced drive is mounted as drive0+vda
// inside the guest (no drive1 overlay layer).
//
// Reuses the same staging + InjectManifest + InjectGuestInit +
// mkfs + Storage.Put pipeline as Build. The per-entry chown path
// threads a per-layer resolver grown from the staging /etc/passwd
// (commit 7) so named users (`Uname!=""`, `Uid=0` — the distroless /
// alpine shape) land on the image's declared uid rather than uid 0
// inside the guest.
func (b *Builder) BuildFullRootfs(ctx context.Context, in BuildFullRootfsInput) (BuildResult, error) {
	limits, ok := api.LimitsFor(in.Plan)
	if !ok {
		return BuildResult{}, fmt.Errorf("rootfs: unknown plan %q", in.Plan)
	}
	if err := in.Manifest.Validate(); err != nil {
		return BuildResult{}, err
	}
	if err := validateFullRootfsOutputTarget(in); err != nil {
		return BuildResult{}, err
	}

	staging, err := os.MkdirTemp("", "faas-fullrootfs-*")
	if err != nil {
		return BuildResult{}, fmt.Errorf("rootfs: staging dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(staging) }()

	// Apply ALL layers with a per-layer resolver that grows after
	// every apply. The order matches ADR-142 §Decision 3: top-most
	// layer wins for any given name (overlay semantics). After each
	// apply, re-parse the staging /etc/passwd (now the merged
	// top-most view); add new entries to the map. Layers N+1 see
	// the merged resolver.
	//
	// The resolver is what preserves named-user chown correctness
	// at apply-time — without it, an image declaring
	// `Uname="node", Uid=0` lands on uid 0 in the guest, which
	// spec §11 forbids.
	passwdEntries := make(map[string]PasswdEntry)
	var resolver Resolver = in.Resolver
	if len(in.Layers) > 0 {
		// Layer zero may introduce /etc/passwd and files whose tar headers
		// use that layer's named users. Spool it once so it can be applied
		// first to discover the passwd table and then replayed with the
		// resolver. Pull blob readers are generally forward-only, so this
		// cannot rely on io.Seeker.
		layer0, cleanup, err := spoolFullRootfsLayer(in.Layers[0])
		if err != nil {
			return BuildResult{}, fmt.Errorf("rootfs: spool layer 0: %w", err)
		}
		defer cleanup()
		f, err := os.Open(layer0)
		if err != nil {
			return BuildResult{}, fmt.Errorf("rootfs: open layer 0: %w", err)
		}
		err = ApplyLayerGzWithResolver(staging, f, nil)
		_ = f.Close()
		if err != nil {
			return BuildResult{}, fmt.Errorf("rootfs: apply layer 0 (pre-parse): %w", err)
		}
		entries, perr := parseStagingPasswd(staging)
		if perr != nil {
			return BuildResult{}, fmt.Errorf("rootfs: parse layer 0 /etc/passwd: %w", perr)
		}
		if entries != nil {
			passwdEntries = entries
		}
		resolver = NewPasswdResolver(passwdEntries)
		f, err = os.Open(layer0)
		if err != nil {
			return BuildResult{}, fmt.Errorf("rootfs: reopen layer 0: %w", err)
		}
		err = ApplyLayerGzWithResolver(staging, f, resolver)
		_ = f.Close()
		if err != nil {
			return BuildResult{}, fmt.Errorf("rootfs: apply layer 0 (resolved): %w", err)
		}
	}
	for i := 1; i < len(in.Layers); i++ {
		layer := in.Layers[i]
		if err := ApplyLayerGzWithResolver(staging, layer, resolver); err != nil {
			return BuildResult{}, fmt.Errorf("rootfs: apply layer %d: %w", i, err)
		}
		// Re-parse the top-most /etc/passwd after each apply.
		// Last-writer-wins semantics: if layer N+1 ships a
		// `/etc/passwd` that overrides the entry for `root`,
		// the layer N+1 entry replaces layer N's. Same shape as
		// the per-layer tar apply above — the staging file is
		// always the merged top-most view.
		if entries, perr := parseStagingPasswd(staging); perr != nil {
			return BuildResult{}, fmt.Errorf("rootfs: parse layer %d /etc/passwd: %w", i, perr)
		} else if entries == nil {
			// A whiteout can remove /etc/passwd. The merged view then has
			// no named users; retaining an entry from a lower layer would
			// let a later tar header resolve against a file the image no
			// longer contains.
			passwdEntries = make(map[string]PasswdEntry)
		} else {
			// /etc/passwd is a regular file, so a later layer replaces the
			// complete file. Use that top-most view as the resolver table;
			// merging rows from a lower copy would resurrect deleted users.
			passwdEntries = entries
		}
		resolver = NewPasswdResolver(passwdEntries)
	}
	// Mark the published tree after customer layers have been applied. The
	// marker is consumed by guest-init before pivot_root; it is deliberately
	// written here rather than accepted from the image so ordinary two-drive
	// images cannot request the full-rootfs boot mode themselves.
	if err := removeFullRootfsMarker(staging); err != nil {
		return BuildResult{}, err
	}
	if err := writeFullRootfsMarker(staging); err != nil {
		return BuildResult{}, err
	}
	// Function-deploy path (spec §4.9, M7). Same semantics as Build;
	// the cap is the plan's AppLayerMaxMB — full-rootfs deployments
	// still respect the per-plan storage envelope.
	if in.TarballPath != "" {
		capBytes := int64(limits.AppLayerMaxMB) * 1024 * 1024
		if err := ApplyTarball(staging, in.TarballPath, capBytes); err != nil {
			var capErr *ErrTarballExceedsCap
			if errors.As(err, &capErr) {
				return BuildResult{}, api.ErrAppLayerTooLarge(limits, capErr.WrittenBytes+capErr.EntryBytes)
			}
			return BuildResult{}, err
		}
		if in.FunctionHandlerPath != "" {
			if err := NormalizeFunctionHandler(staging, in.FunctionHandlerPath); err != nil {
				return BuildResult{}, err
			}
		}
	}
	if in.FunctionRunnerPath != "" {
		if err := InjectFunctionRunner(staging, in.FunctionRunnerPath); err != nil {
			return BuildResult{}, err
		}
	}
	if err := InjectGuestInit(staging, in.GuestInitPath); err != nil {
		return BuildResult{}, err
	}
	if err := InjectManifest(staging, in.Manifest); err != nil {
		return BuildResult{}, err
	}

	// buildPasswdTable writes the merged /etc/passwd map (built
	// per-layer above) into a binary /etc/faas/app_passwd table
	// guest-init reads at boot (M-3 commit 8 wires the reader).
	// The map is the source of truth here — the staging
	// /etc/passwd file is also written, mirroring the standard
	// layout for any tooling that expects it. ADR-142 §Decision 3.
	//
	// Cap is the per-plan api.UserUIDOverrideMax[plan] (M-3 commit
	// 9). Unknown plan → 0 cap (no entries written); the metric
	// fires `over_cap` and the build still proceeds. Hobby 16 /
	// Pro 64 / Scale 256.
	if err := writePasswdTable(staging, passwdEntries, api.UserUIDOverrideMax[in.Plan]); err != nil {
		return BuildResult{}, fmt.Errorf("rootfs: build passwd table: %w", err)
	}

	stats, err := InspectStaging(staging)
	if err != nil {
		return BuildResult{}, err
	}
	sizeMB, err := CheckCapForStaging(limits, stats)
	if err != nil {
		return BuildResult{}, err
	}
	// M-3 commit 9 / ADR-141 §Decision 5: per-plan ceiling on
	// the unpacked full-rootfs staging tree size. Hobby 256 MB /
	// Pro 1 GB / Scale 4 GB; unknown plan → no extra cap (the
	// two-drive AppLayerMaxMB check above still applies). The
	// error path returns a stable CodeImageManifestInvalid so
	// the dispatch site can map it via api.SentinelToCode.
	if maxBytes, ok := api.MaxFullRootfsLayerBytes[in.Plan]; ok && maxBytes > 0 {
		if stats.ContentBytes > maxBytes {
			return BuildResult{}, api.ErrAppLayerTooLarge(limits, stats.ContentBytes)
		}
	}

	// ADR-141 §Decision 5: SBOM emission runs on the full-rootfs
	// staging dir, mirroring the two-drive Build path. Best-effort
	// like Build: a syft error or non-JSON output leaves SBOMKey
	// empty and the build still succeeds.
	sbomKey, sbomErr := b.emitFullRootfsSBOM(ctx, in, staging)
	if sbomErr != nil {
		_ = sbomErr
	}

	// Full-rootfs: stageAppUpper is NOT applied (the full rootfs
	// is the produced drive — drive0+vda, not drive1). The guest
	// sees drive0 as root directly; no overlayfs assembly.
	if err := b.publishExt4FullRootfs(ctx, in, staging, sizeMB); err != nil {
		return BuildResult{}, err
	}

	res := BuildResult{SizeMB: sizeMB, ContentBytes: stats.ContentBytes, SBOMKey: sbomKey}
	if in.OutImage != "" {
		res.ImagePath = in.OutImage
	} else {
		res.ImageKey = in.StorageKey
	}
	return res, nil
}

func spoolFullRootfsLayer(r io.Reader) (string, func(), error) {
	f, err := os.CreateTemp("", "faas-fullrootfs-layer-*.tar.gz")
	if err != nil {
		return "", func() {}, err
	}
	path := f.Name()
	cleanup := func() { _ = os.Remove(path) }
	if _, err := io.Copy(f, r); err != nil {
		_ = f.Close()
		cleanup()
		return "", func() {}, err
	}
	if err := f.Close(); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return path, cleanup, nil
}

func writeFullRootfsMarker(staging string) error {
	path, err := fullRootfsMarkerPath(staging)
	if err != nil {
		return fmt.Errorf("rootfs: resolve full-rootfs marker: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("rootfs: full-rootfs marker dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(api.FullRootfsMarkerValue), 0o444); err != nil {
		return fmt.Errorf("rootfs: write full-rootfs marker: %w", err)
	}
	return nil
}

func removeFullRootfsMarker(staging string) error {
	path, err := fullRootfsMarkerPath(staging)
	if err != nil {
		return fmt.Errorf("rootfs: resolve full-rootfs marker: %w", err)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("rootfs: remove full-rootfs marker: %w", err)
	}
	return nil
}

// fullRootfsMarkerPath applies the same ancestor-symlink containment checks
// used for OCI entries. Marker operations happen after layer extraction, so
// using filepath.Join directly here could follow an image-provided `etc` or
// `faas` symlink into the host filesystem.
func fullRootfsMarkerPath(staging string) (string, error) {
	return resolveEntryPath(staging, strings.TrimPrefix(api.FullRootfsMarkerPath, "/"))
}

// passwdTablePath is the on-disk location of the binary passwd
// table guest-init reads at boot. ADR-142 §Decision 3.
//
// Format (per record, big-endian, contiguous, no padding):
//
//	bytes 0..3   uint32  uid
//	bytes 4..7   uint32  gid
//	byte  8      uint8   name length (0..255)
//	bytes 9..9+N name (UTF-8, no NUL terminator)
//
// Records are sorted ascending by name so guest-init can
// binary-search in O(log N) on every lookup. The file is owned
// by root:root mode 0o644 — readable by the app user inside the
// guest but not writable.
const passwdTablePath = "/etc/faas/app_passwd"

// parseStagingPasswd reads the merged /etc/passwd at the staging
// dir's root after a layer apply. Returns nil + no error when the
// file is missing (an image without /etc/passwd — extremely rare).
// Caller merges the entries into the rolling map (top-most-wins
// semantics: if the file is present, the most recent apply is the
// source of truth).
func parseStagingPasswd(staging string) (map[string]PasswdEntry, error) {
	p := filepath.Join(staging, "etc", "passwd")
	f, err := os.Open(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	return ParsePasswd(f)
}

// writePasswdTable writes the merged /etc/passwd map to two places:
//  1. /etc/passwd — standard text form, so any tooling inside the
//     guest that expects a real passwd file finds one. The text
//     form is rebuilt from the same map (top-most-wins by name
//     sort order) so the two views are byte-identical for any
//     given name.
//  2. /etc/faas/app_passwd — binary form guest-init reads at boot
//     (commit 8). Sorted by name. Capped at maxEntries; over-cap
//     images increment the over_cap counter so the dashboard
//     tripwires without polluting the success series.
//
// Errors:
//   - directory creation fails → wrapped.
//   - /etc/passwd write fails → wrapped.
//   - binary table write fails → wrapped.
//
// The on-disk format is fixed (see passwdTablePath comment); any
// future widening MUST either be additive (new file alongside)
// or gated on a new migration.
func writePasswdTable(staging string, entries map[string]PasswdEntry, maxEntries int) error {
	if ops != nil {
		if c := ops.PasswdEntries("ok"); c != nil {
			c.Add(float64(len(entries)))
		}
		if len(entries) > maxEntries {
			if c := ops.PasswdEntries("over_cap"); c != nil {
				c.Inc()
			}
		}
	}
	// Build the sorted name list — used for both the text form
	// and the binary form. Top-most-wins is enforced by the
	// caller (entries[k] = v at the merge site).
	names := make([]string, 0, len(entries))
	for n := range entries {
		names = append(names, n)
	}
	sortStrings(names)

	// Keep the image's original /etc/passwd when it supplied one. Rewriting
	// it would discard the gecos, home, shell, and password fields that
	// container tooling may rely on. The synthetic form is only a fallback
	// for callers that provide an entry map without an existing file.
	if len(entries) > 0 {
		textPath := filepath.Join(staging, "etc", "passwd")
		if _, err := os.Lstat(textPath); errors.Is(err, os.ErrNotExist) {
			if err := os.MkdirAll(filepath.Dir(textPath), 0o755); err != nil {
				return fmt.Errorf("rootfs: mkdir etc: %w", err)
			}
			var buf strings.Builder
			for _, n := range names {
				e := entries[n]
				// Standard 7-field colon-separated form. We do not write a
				// password hash; the guest has no shadow file by default.
				fmt.Fprintf(&buf, "%s:x:%d:%d::/home/%s:/sbin/nologin\n",
					e.Name, e.Uid, e.Gid, e.Name)
			}
			if err := os.WriteFile(textPath, []byte(buf.String()), 0o644); err != nil {
				return fmt.Errorf("rootfs: write /etc/passwd: %w", err)
			}
		} else if err != nil {
			return fmt.Errorf("rootfs: inspect /etc/passwd: %w", err)
		}
	}

	// Cap the binary table at maxEntries. Excess entries are
	// silently dropped (the metric fires above so the dashboard
	// sees it). The text file still carries the full set so any
	// guest tooling that reads /etc/passwd sees the real image
	// shape.
	binNames := names
	if maxEntries <= 0 {
		binNames = nil
	} else if len(binNames) > maxEntries {
		binNames = binNames[:maxEntries]
	}

	// Build the binary table: header per record, contiguous.
	var bin bytes.Buffer
	bin.Grow(len(binNames) * (4 + 4 + 1 + 16))
	for _, n := range binNames {
		e := entries[n]
		if len(n) > 255 {
			// Pathological image with a >255-byte user name.
			// Skip silently — the metric counter would have
			// fired above. The text form still carries it.
			continue
		}
		var uidBytes [4]byte
		var gidBytes [4]byte
		binaryBigEndianPutUint32(uidBytes[:], uint32(e.Uid))
		binaryBigEndianPutUint32(gidBytes[:], uint32(e.Gid))
		bin.Write(uidBytes[:])
		bin.Write(gidBytes[:])
		bin.WriteByte(byte(len(n)))
		bin.Write([]byte(n))
	}

	binPath := filepath.Join(staging, passwdTablePath)
	if err := os.MkdirAll(filepath.Dir(binPath), 0o755); err != nil {
		return fmt.Errorf("rootfs: mkdir /etc/faas: %w", err)
	}
	if err := os.WriteFile(binPath, bin.Bytes(), 0o644); err != nil {
		return fmt.Errorf("rootfs: write %s: %w", passwdTablePath, err)
	}
	return nil
}

// sortStrings is a tiny insertion-sort helper that avoids importing
// "sort" into this hot path. The passwd table is ≤ 256 entries so
// O(N²) is fine and saves a package-level dependency.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		j := i
		for j > 0 && s[j-1] > s[j] {
			s[j-1], s[j] = s[j], s[j-1]
			j--
		}
	}
}

// binaryBigEndianPutUint32 encodes a uint32 big-endian into the
// first 4 bytes of buf. Avoids importing "encoding/binary" —
// keeps the build_base.go self-contained.
func binaryBigEndianPutUint32(buf []byte, v uint32) {
	buf[0] = byte(v >> 24)
	buf[1] = byte(v >> 16)
	buf[2] = byte(v >> 8)
	buf[3] = byte(v)
}

// emitFullRootfsSBOM is the full-rootfs sibling of emitSBOM — same
// best-effort semantics, scopes the SBOM to the full-rootfs
// staging dir.
func (b *Builder) emitFullRootfsSBOM(ctx context.Context, in BuildFullRootfsInput, staging string) (string, error) {
	if in.SBOMRun == nil {
		return "", nil
	}
	if in.Storage == nil || in.SBOMStorageKey == "" {
		return "", nil
	}
	body, err := in.SBOMRun(ctx, staging)
	if err != nil || !json.Valid(body) {
		return "", nil
	}
	if err := in.Storage.Put(ctx, in.SBOMStorageKey, bytes.NewReader(body)); err != nil {
		return "", nil
	}
	return in.SBOMStorageKey, nil
}

// publishExt4FullRootfs is the full-rootfs sibling of publishExt4.
// Same mkfs.ext4 + Storage.Put pipeline; no drive1 wrapper, no
// overlayfs staging.
func (b *Builder) publishExt4FullRootfs(ctx context.Context, in BuildFullRootfsInput, staging string, sizeMB int) error {
	if in.OutImage != "" {
		if err := os.MkdirAll(filepath.Dir(in.OutImage), 0o755); err != nil {
			return fmt.Errorf("rootfs: mkdir full-rootfs out dir: %w", err)
		}
		if err := b.run.Run(ctx, MkfsCommand(staging, in.OutImage, sizeMB)); err != nil {
			return fmt.Errorf("rootfs: full-rootfs mkfs: %w", err)
		}
		return nil
	}
	// Keep the mkfs output beside the staging directory. This avoids
	// copying a growing ext4 file into the staging tree when mkfs.ext4
	// walks -d, and mirrors publishExt4's atomic Storage.Put flow.
	tmp, err := os.CreateTemp(filepath.Dir(staging), "faas-fullrootfs-mkfs-*.ext4")
	if err != nil {
		return fmt.Errorf("rootfs: create full-rootfs tmp ext4: %w", err)
	}
	tmpPath := tmp.Name()
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rootfs: close full-rootfs tmp ext4: %w", err)
	}
	defer os.Remove(tmpPath)
	if err := b.run.Run(ctx, MkfsCommand(staging, tmpPath, sizeMB)); err != nil {
		return fmt.Errorf("rootfs: full-rootfs mkfs: %w", err)
	}
	f, err := os.Open(tmpPath)
	if err != nil {
		return fmt.Errorf("rootfs: open full-rootfs mkfs output: %w", err)
	}
	defer f.Close()
	if err := in.Storage.Put(ctx, in.StorageKey, f); err != nil {
		return fmt.Errorf("rootfs: publish full-rootfs %q: %w", in.StorageKey, err)
	}
	if b.signer != nil {
		sigKey := "sigs/" + in.StorageKey + ".sig"
		if err := b.signer.Sign(ctx, in.StorageKey, sigKey); err != nil {
			return fmt.Errorf("rootfs: sign full-rootfs %q: %w", in.StorageKey, err)
		}
	}
	return nil
}

// validateFullRootfsOutputTarget mirrors validateOutputTarget for
// the full-rootfs BuildFullRootfsInput (Storage + StorageKey
// mutually exclusive with OutImage; SBOMStorageKey requires
// SBOMRun).
func validateFullRootfsOutputTarget(in BuildFullRootfsInput) error {
	if (in.Storage == nil) == (in.OutImage == "") {
		return fmt.Errorf("rootfs: exactly one of Storage or OutImage must be set")
	}
	if in.Storage != nil && in.StorageKey == "" {
		return fmt.Errorf("rootfs: StorageKey required when Storage is set")
	}
	if in.SBOMRun != nil && in.SBOMStorageKey == "" {
		return fmt.Errorf("rootfs: SBOMStorageKey required when SBOMRun is set")
	}
	return nil
}

// ensureBaseMountpoints creates directories that must exist on the read-only
// boot root before guest-init can mount drive1. OCI layers commonly omit empty
// pseudo-filesystem mountpoints, but guest-init runs with the base root
// read-only, so it cannot create them during boot. Keep this list in sync with
// the mounts performed by guest-init before and after pivot_root.
func ensureBaseMountpoints(staging string) error {
	for _, path := range []string{
		"dev",
		"overlay",
		"proc",
		"run",
		"sys",
		"sys/fs/cgroup",
		"tmp",
	} {
		if err := os.MkdirAll(filepath.Join(staging, path), 0o755); err != nil {
			return fmt.Errorf("rootfs: create base mountpoint %q: %w", path, err)
		}
	}
	return nil
}

// buildBaseFromStaging is the shared tail of BuildBase (apply-all
// path) and BuildBaseFromStaging (ADR-053 parent-ref path): measure,
// mkfs, publish. Extracted so both paths share the same padding +
// signing + storage Put semantics.
//
// Sizing uses BasePaddedSizeMB (small-file-ratio-aware) rather than
// PaddedSizeMB because base images — particularly the Debian 12-slim
// parent (ADR-053) — are dominated by tiny files like
// /usr/share/zoneinfo/*. The 10 % slack PaddedSizeMB adds is far too
// tight for that shape; the previous formula left imaged's
// base-debian-parent staging ENOSPC-failing inside mkfs.ext4 -d (EX44
// run 30656504195, 2026-07-31). InspectStaging walks the tree once;
// the cost is dwarfed by the mkfs that follows.
func (b *Builder) buildBaseFromStaging(ctx context.Context, staging string, in BaseBuildInput) (BaseBuildResult, error) {
	stats, err := InspectStaging(staging)
	if err != nil {
		return BaseBuildResult{}, err
	}

	if err := b.publishBaseExt4(ctx, in, staging, BasePaddedSizeMB(stats.ContentBytes, stats.SmallRatio)); err != nil {
		return BaseBuildResult{}, err
	}

	res := BaseBuildResult{SizeBytes: stats.ContentBytes}
	if in.OutImage != "" {
		res.ImagePath = in.OutImage
	} else {
		res.ImageKey = in.StorageKey
	}
	return res, nil
}

// publishBaseExt4 mirrors Builder.publishExt4 but writes via the base
// path: mkfs into a tmp file, Put under StorageKey. The legacy OutImage
// path mkfs-es directly into OutImage (matches the pre-#96 behaviour).
func (b *Builder) publishBaseExt4(ctx context.Context, in BaseBuildInput, staging string, sizeMB int) error {
	if in.OutImage != "" {
		if err := b.runBaseMkfs(ctx, staging, in.OutImage, sizeMB); err != nil {
			return fmt.Errorf("rootfs: base mkfs: %w", err)
		}
		return nil
	}
	// mkfs's -d flag populates the new image from `staging`; the output file
	// must therefore live outside that tree and its tmpfs staging budget.
	tmpRoot := os.Getenv("FAAS_BASE_TMP_ROOT")
	if tmpRoot == "" {
		tmpRoot = os.TempDir()
	}
	if err := os.MkdirAll(tmpRoot, 0o755); err != nil {
		return fmt.Errorf("rootfs: create base tmp root: %w", err)
	}
	tmp, err := os.CreateTemp(tmpRoot, "faas-base-mkfs-*.ext4")
	if err != nil {
		return fmt.Errorf("rootfs: create base tmp ext4: %w", err)
	}
	tmpPath := tmp.Name()
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rootfs: close base tmp ext4: %w", err)
	}
	if err := b.runBaseMkfs(ctx, staging, tmpPath, sizeMB); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rootfs: base mkfs: %w", err)
	}
	// nolint:forbidigo // tmpPath is from os.MkdirTemp at the top of
	// this function — a daemon-internal scratch file the builder just
	// wrote via MkfsCommand. Not a customer path.
	f, err := os.Open(tmpPath)
	if err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rootfs: open base mkfs output: %w", err)
	}
	defer func() { _ = f.Close(); _ = os.Remove(tmpPath) }()
	if err := in.Storage.Put(ctx, in.StorageKey, f); err != nil {
		return fmt.Errorf("rootfs: publish base %q: %w", in.StorageKey, err)
	}
	// ADR-038: mirror publishExt4's sign call. The base sig lives
	// at sigs/<StorageKey>.sig — same convention as the app-layer
	// path; schedd's verify side derives the sig key from the
	// layer key the same way (sigs/<layerKey>.sig). imaged's
	// startup restages + signs the base on first run if no sig
	// exists (see pkg/imaged/base_stage.go); app layers re-sign on
	// the next deploy.
	if b.signer != nil {
		sigKey := "sigs/" + in.StorageKey + ".sig"
		if err := b.signer.Sign(ctx, in.StorageKey, sigKey); err != nil {
			return fmt.Errorf("rootfs: sign base %q: %w", in.StorageKey, err)
		}
	}
	return nil
}

const (
	// mkfs.ext4 -d reports block exhaustion from the populate phase rather
	// than returning a machine-readable required size. Base trees can gain
	// many inode- and block-heavy files between runtime refreshes, so retry a
	// bounded number of times with additional room instead of publishing a
	// partial image or crash-looping imaged on a one-shot sizing miss.
	baseMkfsMaxAttempts = 4
	baseMkfsGrowthPct   = 25
	baseMkfsGrowthFloor = 16
)

func (b *Builder) runBaseMkfs(ctx context.Context, staging, outImage string, sizeMB int) error {
	if sizeMB < MinLayerMB {
		sizeMB = MinLayerMB
	}
	lastSize := sizeMB
	var lastErr error
	for attempt := 0; attempt < baseMkfsMaxAttempts; attempt++ {
		lastSize = sizeMB
		if err := b.run.Run(ctx, MkfsCommand(staging, outImage, sizeMB)); err == nil {
			return nil
		} else {
			lastErr = err
			if !baseMkfsNeedsMoreSpace(err) || attempt == baseMkfsMaxAttempts-1 {
				break
			}
		}
		growth := sizeMB * baseMkfsGrowthPct / 100
		if growth < baseMkfsGrowthFloor {
			growth = baseMkfsGrowthFloor
		}
		sizeMB += growth
	}
	return fmt.Errorf("mkfs.ext4 -d exhausted base image sizing after %d attempt(s) at %d MiB: %w", baseMkfsMaxAttempts, lastSize, lastErr)
}

func baseMkfsNeedsMoreSpace(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "could not allocate block") ||
		strings.Contains(message, "no space left") ||
		strings.Contains(message, "populating file system")
}

// validateBaseOutputTarget enforces the same exclusive-or rule as
// BuildInput.validateOutputTarget. The two helpers live separately
// because the input structs differ and a single generic helper would
// need a shared interface that adds nothing for the call sites.
func validateBaseOutputTarget(in BaseBuildInput) error {
	hasStorage := in.Storage != nil && in.StorageKey != ""
	hasOut := in.OutImage != ""
	switch {
	case hasStorage && hasOut:
		return errors.New("rootfs: BaseBuildInput has both Storage and OutImage set; pick one")
	case !hasStorage && !hasOut:
		return errors.New("rootfs: BaseBuildInput has neither Storage nor OutImage set")
	case hasStorage && in.StorageKey == "":
		return errors.New("rootfs: BaseBuildInput has Storage but empty StorageKey")
	case hasStorage && in.Storage == nil:
		return errors.New("rootfs: BaseBuildInput has StorageKey but nil Storage")
	}
	return nil
}
