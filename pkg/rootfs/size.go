package rootfs

import (
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/onebox-faas/faas/pkg/api"
)

// App-layer sizing (spec §4.6). The per-app drive1 ext4 must fit the
// content (deps + code + guest-init + app.json) plus filesystem overhead,
// and the final image must fit the plan's app-layer cap. Getting this
// wrong either wastes disk (hurts the 130 MB fleet target) or produces an
// unbootable too-small fs.
//
// Slack calibration note (run 30656504195, 2026-07-31,
// base-debian-parent staging — mkfs.ext4 -d failed mid-populate on
// /usr/share/zoneinfo/Africa/Dakar with "Could not allocate block in
// ext2 filesystem"). The previous 10 % slack was tuned for trees of
// medium-to-large files; it under-estimates trees of mostly-tiny files
// by a factor of ~3× (a 76 MB apparent Debian 12-slim tree needs a
// 109 MB ext4 — a 1.45× expansion — because ~70 % of files are under
// 4 KB and mkfs.ext4's lazy allocation rounds them up). The base-image
// staging path (BuildBase / BuildBaseFromStaging, ADR-053) now walks
// the staging tree once to compute a small-file ratio and adds slack
// proportional to it. The per-app CheckCap path keeps the simpler
// percentage-of-apparent formula since the cap rejects before slack
// matters (and per-app users mostly ship larger code artefacts, not
// zoneinfo).

const (
	// mib is one mebibyte.
	mib = int64(1024 * 1024)
	// MinLayerMB floors the image so tiny apps still get room for inode tables
	// and journal.
	MinLayerMB = 16
	// slackFloorMB is the minimum absolute overhead added on top of content.
	slackFloorMB = 4
	// baseSlackPct is the baseline fractional overhead for
	// BasePaddedSizeMB, floored at slackFloorMB. Calibrated to match
	// the legacy 10 % for trees where most files sit at or above
	// smallFileThreshold (e.g. alpine-builder, node22 npm trees).
	baseSlackPct = 10
	// smallFileThreshold is the boundary (in bytes) below which a
	// regular file in the staging tree is counted as "small" for the
	// BasePaddedSizeMB per-file overhead. Matches the default ext4
	// block size: anything under one block wastes up to a full block's
	// worth of alignment when copied via mkfs.ext4's -d. Files at or
	// above this size fit one block exactly and don't contribute.
	smallFileThreshold = 4096
	// smallFileSlackPct is the per-small-file fractional overhead
	// blended into the base slack. Empirical calibration: with
	// baseSlackPct=10 and a Debian-shaped tree where 80 % of files
	// are small, the combined effective slack is
	// 10 % + (80 % × smallFileSlackPct / 100) = 58 %, matching the
	// 1.45× apparent-to-image ratio observed at run 30656504195 (109 MB
	// ext4 for 75 MB Debian 12-slim apparent bytes). With no small
	// files, the formula degenerates to the legacy 10 %.
	smallFileSlackPct = 60
	// perAppSlackPct is the simple percentage-of-apparent slack used
	// by PaddedSizeMB / CheckCap. Kept conservative on purpose; the
	// per-app cap path rejects early for users shipping too much, and
	// per-app layers see fewer tiny files than base images (no
	// zoneinfo).
	perAppSlackPct = 10
)

// DirSize returns the total apparent size in bytes of every regular file under
// root (following the tree, not counting directory entries themselves).
func DirSize(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			total += info.Size()
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("rootfs: sizing %s: %w", root, err)
	}
	return total, nil
}

// SmallFileStats summarises a staging tree's file-size distribution for the
// BasePaddedSizeMB path. ContentBytes is the same total DirSize would
// compute; SmallRatio is the fraction of regular files under
// smallFileThreshold (0.0 for an empty tree, 1.0 for an entirely-small
// tree). The ratio drives the per-file slack overhead — see package doc
// for the calibration.
type SmallFileStats struct {
	ContentBytes int64
	SmallRatio   float64
}

// InspectStaging walks `root` once, returning both the apparent-size total
// and the fraction of regular files below smallFileThreshold. Used by
// BuildBase's publishBaseExt4 to pick an image size that survives
// mkfs.ext4's per-block rounding for trees dominated by tiny files.
// Single-walk by design — the staging tree is one MkdirTemp removal
// away, and the cost is dwarfed by the mkfs.ext4 -d that follows.
func InspectStaging(root string) (SmallFileStats, error) {
	var stats SmallFileStats
	var total, small, n int64
	err := filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		sz := info.Size()
		n++
		total += sz
		if sz < smallFileThreshold {
			small++
		}
		return nil
	})
	if err != nil {
		return SmallFileStats{}, fmt.Errorf("rootfs: inspect staging %s: %w", root, err)
	}
	stats.ContentBytes = total
	if n > 0 {
		stats.SmallRatio = float64(small) / float64(n)
	}
	return stats, nil
}

// PaddedSizeMB computes the ext4 image size in whole MB for the given content
// size: content + max(perAppSlackPct%, slack floor), rounded up, floored at
// MinLayerMB. The slack covers ext4 metadata (inodes, journal, block
// bitmaps). Used by CheckCap (per-app layer cap); see package doc for the
// base-image equivalent (BasePaddedSizeMB).
func PaddedSizeMB(contentBytes int64) int {
	if contentBytes < 0 {
		contentBytes = 0
	}
	slack := contentBytes * int64(perAppSlackPct) / 100
	if floor := int64(slackFloorMB) * mib; slack < floor {
		slack = floor
	}
	total := contentBytes + slack
	sizeMB := int((total + mib - 1) / mib) // ceil to MB
	if sizeMB < MinLayerMB {
		sizeMB = MinLayerMB
	}
	return sizeMB
}

// BasePaddedSizeMB computes the ext4 image size in whole MB for a base
// image, given the staging tree's apparent total and small-file ratio
// (from InspectStaging). The slack blends a baseline (baseSlackPct)
// with a per-small-file overhead (smallFileSlackPct × smallRatio):
//
//	effectiveSlackPct = baseSlackPct + smallRatio × smallFileSlackPct
//
// floored at slackFloorMB. For all-big-file trees (smallRatio=0) this
// degenerates to the legacy 10 % slack; for a Debian 12-slim-shaped
// tree (smallRatio ≈ 0.8) it produces ~58 % overhead, matching the
// empirical 1.45× apparent-to-image ratio observed at run 30656504195
// (2026-07-31, base-debian-parent staging). The cap check (CheckCap)
// still rejects over-limit per-app layers; this helper is for the
// base staging path only.
func BasePaddedSizeMB(contentBytes int64, smallRatio float64) int {
	if contentBytes < 0 {
		contentBytes = 0
	}
	if smallRatio < 0 {
		smallRatio = 0
	}
	if smallRatio > 1 {
		smallRatio = 1
	}
	effectivePct := int64(baseSlackPct) + int64(float64(smallFileSlackPct)*smallRatio+0.5)
	slack := contentBytes * effectivePct / 100
	if floor := int64(slackFloorMB) * mib; slack < floor {
		slack = floor
	}
	total := contentBytes + slack
	sizeMB := int((total + mib - 1) / mib) // ceil to MB
	if sizeMB < MinLayerMB {
		sizeMB = MinLayerMB
	}
	return sizeMB
}

// CheckCap enforces the plan's app-layer cap against the padded image size. It
// returns an actionable *api.Problem (naming the cap and observed size) when the
// layer would exceed the cap — the deploy fails here, before any snapshot work.
func CheckCap(l api.Limits, contentBytes int64) (sizeMB int, err error) {
	sizeMB = PaddedSizeMB(contentBytes)
	if sizeMB > l.EphemeralDiskMaxMB() {
		return sizeMB, api.ErrAppLayerTooLarge(l, int64(sizeMB)*mib)
	}
	return sizeMB, nil
}

// CheckCapForStaging is the app-layer cap check for a fully materialized
// staging tree. Unlike CheckCap, it accounts for ext4's per-file block
// allocation when the tree contains many small runtime files (for example,
// Node's CA bundle and npm metadata). Using only apparent byte size can make
// mkfs.ext4 -d run out of blocks even though the nominal 10% app slack fits.
func CheckCapForStaging(l api.Limits, stats SmallFileStats) (sizeMB int, err error) {
	sizeMB = BasePaddedSizeMB(stats.ContentBytes, stats.SmallRatio)
	if sizeMB > l.EphemeralDiskMaxMB() {
		return sizeMB, api.ErrAppLayerTooLarge(l, int64(sizeMB)*mib)
	}
	return sizeMB, nil
}

// MkfsCommand builds the argv that creates a populated ext4 image from a staging
// directory WITHOUT mounting it — mke2fs's `-d` feature, so no root/loop device
// is needed (spec §4.6). `-F` forces creation over a non-block-device file.
func MkfsCommand(stagingDir, outImage string, sizeMB int) []string {
	return []string{
		"mkfs.ext4",
		"-F",
		"-L", "applayer",
		"-d", stagingDir,
		outImage,
		fmt.Sprintf("%dM", sizeMB),
	}
}
