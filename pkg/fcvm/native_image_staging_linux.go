//go:build linux

// adr: 568 — temporary source names exist only inside a durable owned epoch.
package fcvm

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// Temporary links must be owned on the same filesystem as both the anonymous
// data and its epoch journal. A tmpfs jail journal needs a separate durable disk
// ownership adapter; refuse before producing a clone or capture output.
func (b linuxNativeImageSources) requireAnonymousStagingFilesystem(directory string) error {
	var disk, journal unix.Stat_t
	if err := unix.Lstat(directory, &disk); err != nil {
		return err
	}
	if disk.Mode&unix.S_IFMT != unix.S_IFDIR {
		return errors.New("native image source: original disk directory is unavailable")
	}
	path := filepath.Join(b.base, ".native-processes", "image-sources", "points")
	for {
		err := unix.Lstat(path, &journal)
		if err == nil {
			if journal.Mode&unix.S_IFMT != unix.S_IFDIR || journal.Dev != disk.Dev {
				return errors.New("native image source: anonymous staging requires the data and native journal on the same filesystem; the tmpfs disk ownership adapter is unavailable")
			}
			return nil
		}
		if !errors.Is(err, os.ErrNotExist) || path == b.base {
			return fmt.Errorf("native image source: original journal placement is unavailable: %w", err)
		}
		path = filepath.Dir(path)
	}
}

func (p *linuxNativeImagePreparation) linkAnonymousSource(point string) (err error) {
	var stat unix.Stat_t
	if err := unix.Fstat(int(p.source.Fd()), &stat); err != nil {
		return err
	}
	if stat.Nlink != 0 {
		return nil // Original immutable inputs already have a connected dentry.
	}
	if p.staging != "" || stat.Ino != p.identity.Inode || uint64(stat.Dev) != p.identity.Device {
		return errors.New("native image source: anonymous staging identity changed")
	}
	if err := checkNativeJournalPath(filepath.Dir(point), true); err != nil {
		return err
	}
	path := point + nativeImageStagingSuffix
	if err := unix.Linkat(unix.AT_FDCWD, nativeImageFDPath(p.source), unix.AT_FDCWD, path, unix.AT_SYMLINK_FOLLOW); err != nil {
		return err
	}
	p.staging = path
	if err := syncNativeImageParent(path); err != nil {
		return err
	}
	// Reopen the connected dentry for mount(2). The original anonymous FD's
	// dentry stays disconnected even after linkat adds a name for its inode.
	connected, err := os.OpenFile(path, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	identity, _, err := nativeImageFileMetadata(connected)
	if err != nil || identity != p.identity {
		return errors.Join(err, connected.Close(), errors.New("native image source: temporary link changed original inode"))
	}
	previous := p.source
	p.source = connected
	return previous.Close()
}

func removeNativeImageStagingSource(path string, identity nativeLoopIdentity) error {
	file, err := os.OpenFile(path, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	actual, _, statErr := nativeImageFileMetadata(file)
	var stat unix.Stat_t
	if err := errors.Join(statErr, unix.Fstat(int(file.Fd()), &stat), file.Close()); err != nil {
		return err
	}
	if actual != identity || stat.Nlink != 1 {
		return errors.New("native image source: temporary link was replaced or acquired another alias")
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	return syncNativeImageParent(path)
}

func inspectNativeImageStagingSource(record nativeImageSourceRecord, point string) (err error) {
	file, err := os.OpenFile(point+nativeImageStagingSuffix, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	identity, metadata, err := nativeImageFileMetadata(file)
	var stat unix.Stat_t
	if err := errors.Join(err, unix.Fstat(int(file.Fd()), &stat)); err != nil {
		return err
	}
	if record.Removed || identity != record.Identity || stat.Nlink != 1 || len(record.References) != 1 || record.References[0].ReadOnly || record.References[0].Link {
		return errors.New("native image source: temporary link has no exclusive original epoch")
	}
	intermediate := record.Applied
	intermediate.UID, intermediate.GID = record.Desired.UID, record.Desired.GID
	if !record.Ready && metadata != record.Original || metadata != record.Applied && metadata != record.Desired && metadata != intermediate {
		return errors.New("native image source: temporary source metadata changed outside its declared transition")
	}
	return nil
}
