package objectnode

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf8"

	bolt "go.etcd.io/bbolt"

	"github.com/onebox-faas/faas/pkg/api"
	"golang.org/x/sys/unix"
)

type auditDirectory struct {
	Path          string
	Offset        int64
	Device, Inode uint64
	Pending       []string
}

// Linux directory cookies resume bounded getdents batches. Buffered names are
// checkpointed too: os.File.ReadDir's private read-ahead cannot be represented
// by the underlying file offset alone. No descriptor survives the turn.
func (s *Store) nextAuditName(d *auditDirectory) (string, error) {
	if d.Path != "" && (filepath.Clean(d.Path) != d.Path || filepath.IsAbs(d.Path) || strings.HasPrefix(d.Path, "..")) || d.Offset < 0 {
		return "", ErrCorrupt
	}
	fd, err := unix.Open(filepath.Join(s.root, d.Path), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return "", err
	}
	defer func() { _ = unix.Close(fd) }()
	var stat unix.Stat_t
	if err = unix.Fstat(fd, &stat); err != nil {
		return "", err
	}
	if d.Inode != 0 && (d.Inode != stat.Ino || d.Device != uint64(stat.Dev)) {
		return "", ErrInventoryChanged
	}
	d.Inode, d.Device = stat.Ino, uint64(stat.Dev)
	if len(d.Pending) == 0 {
		if _, err = unix.Seek(fd, d.Offset, io.SeekStart); err != nil {
			return "", err
		}
		buffer := make([]byte, api.ObjectNodeAuditDirectoryBytes)
		n, err := unix.ReadDirent(fd, buffer)
		if err != nil {
			return "", err
		}
		if n == 0 {
			return "", io.EOF
		}
		d.Offset, err = unix.Seek(fd, 0, io.SeekCurrent)
		if err != nil {
			return "", err
		}
		_, _, d.Pending = syscall.ParseDirent(buffer[:n], -1, nil)
		for _, name := range d.Pending {
			if !utf8.ValidString(name) {
				return "", ErrCorrupt // JSON checkpoints must preserve exact names.
			}
		}
		if len(d.Pending) == 0 {
			return "", nil // An empty batch is a work unit, never an unbounded loop.
		}
	}
	name := d.Pending[0]
	d.Pending = d.Pending[1:]
	if name == "" || name == "." || name == ".." || filepath.Base(name) != name {
		return "", ErrCorrupt
	}
	return name, nil
}

func auditRealDirectory(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.IsDir()
}

func (s *Store) auditPhysicalUnit(tx *bolt.Tx, p *inventoryAuditState) error {
	if len(p.Walk) == 0 {
		p.Status.Phase, p.Cursor = "heads", ""
		return nil
	}
	last := len(p.Walk) - 1
	name, err := s.nextAuditName(&p.Walk[last])
	if errors.Is(err, io.EOF) || errors.Is(err, os.ErrNotExist) || errors.Is(err, ErrInventoryChanged) {
		p.Walk = p.Walk[:last]
		return nil
	}
	if err != nil || name == "" {
		return err
	}
	path := filepath.Join(p.Walk[last].Path, name)
	info, err := os.Lstat(filepath.Join(s.root, path))
	if errors.Is(err, os.ErrNotExist) {
		return nil // Physical samples allow staging and maintenance to change.
	}
	if err != nil {
		return err
	}
	u := &p.Status.Usage
	if err = s.auditPhysicalSource(tx, p, path, info.IsDir()); err != nil {
		return err
	}
	if info.IsDir() {
		if len(p.Walk) > api.ObjectNodeInventoryMaxDepth {
			addInventoryIssue(u, "unexpected_directory_depth", path, true)
			return nil
		}
		p.Walk = append(p.Walk, auditDirectory{Path: path})
		return nil
	}
	if !info.Mode().IsRegular() {
		addInventoryIssue(u, "unexpected_file_type", path, true)
		return nil
	}
	u.Files++
	if err = addInventoryBytes(&u.FileBytes, info.Size()); err != nil {
		return err
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		if err = addInventoryBytes(&u.AllocatedBytes, stat.Blocks*512); err != nil {
			return err
		}
	}
	receipts, receiptBytes := u.Receipts, u.ReceiptBytes
	err = s.auditFile(u, path, info.Size())
	u.Receipts, u.ReceiptBytes = receipts, receiptBytes // Logical receipt coverage is tracked independently.
	return err
}

// Physical discovery catches records absent from the registry. Directory cookies
// may change under writes; the independent source anchor proves logical coverage.
func (s *Store) auditPhysicalSource(tx *bolt.Tx, p *inventoryAuditState, path string, directory bool) error {
	pieces := strings.Split(filepath.ToSlash(path), "/")
	key := ""
	switch {
	case pieces[0] == "buckets" && directory && len(pieces) == 2:
		key = "b/" + pieces[1]
	case pieces[0] == "buckets" && directory && len(pieces) == 4 && pieces[2] == "objects":
		key = "o/" + pieces[1] + "/" + pieces[3]
	case pieces[0] == "multipart" && directory && len(pieces) == 2:
		key = "u/" + pieces[1]
	case pieces[0] == "multipart" && directory && len(pieces) == 4 && pieces[2] == "parts":
		key = "p/" + pieces[1] + "/" + pieces[3]
	case pieces[0] == "receipts" && len(pieces) == 2 && strings.HasSuffix(pieces[1], ".json"):
		key = "r/" + strings.TrimSuffix(pieces[1], ".json")
	}
	if key == "" {
		return nil
	}
	if _, err := auditSourcePath(key); err != nil {
		addInventoryIssue(&p.Status.Usage, "invalid_source_identity", path, true)
		return nil //nolint:nilerr // Retain the finding and continue bounded source discovery.
	}
	return s.checkAuditSource(tx, p, key)
}
