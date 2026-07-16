package rootfs

import (
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/onebox-faas/faas/pkg/api"
)

// App-layer sizing (spec §4.6). The per-app drive1 ext4 must hold the content
// (deps + code + guest-init + app.json) plus filesystem overhead, and the final
// image must fit the plan's app-layer cap. Getting this wrong either wastes disk
// (hurts the 130 MB fleet target) or produces an unbootable too-small fs.

const (
	// mib is one mebibyte.
	mib = int64(1024 * 1024)
	// MinLayerMB floors the image so tiny apps still get room for inode tables
	// and journal.
	MinLayerMB = 16
	// slackFloorMB is the minimum absolute overhead added on top of content.
	slackFloorMB = 4
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

// PaddedSizeMB computes the ext4 image size in whole MB for the given content
// size: content + max(10%, slack floor), rounded up, floored at MinLayerMB. The
// slack covers ext4 metadata (inodes, journal, block bitmaps).
func PaddedSizeMB(contentBytes int64) int {
	if contentBytes < 0 {
		contentBytes = 0
	}
	slack := contentBytes / 10
	if floor := slackFloorMB * mib; slack < floor {
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
	if sizeMB > l.AppLayerMaxMB {
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
