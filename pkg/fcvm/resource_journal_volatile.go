// adr: 473
// adr: 20261026113920
package fcvm

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ResourceJournalConfig locates the resource journal (ADR-473) and its tmpfs
// copies (ADR-20261026113920).
type ResourceJournalConfig struct {
	// Dir is the persistent record directory.
	Dir string
	// VolatileDir holds, on tmpfs, the newest version of every record a
	// same-boot restart needs. Empty disables the copies.
	VolatileDir string
	// VolatileWrites sends volatile commits only to VolatileDir. Off, vmmd
	// still reads copies left by a run that had it on and commits them to Dir.
	VolatileWrites bool
}

// resourceVolatileBootFile names the kernel boot whose copies the volatile
// directory holds, and the persistent journal they belong to.
const resourceVolatileBootFile = ".boot"

// Replaced by tests; the Linux versions read the kernel boot id and the
// directory's filesystem type.
var (
	resourceVolatileBootID = kernelBootID
	resourceVolatileTmpfs  = onTmpfs
)

// openVolatileJournal opens the tmpfs copies of the journal at journalDir.
// Copies written under another kernel boot are discarded unread; copies of
// another journal on this boot fail the open closed. With writes off, a
// missing directory is not created: there is nothing to read.
func openVolatileJournal(path, journalDir string, writes bool) (*os.Root, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("resource journal: absolute volatile directory required")
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) && !writes {
		return nil, nil
	}
	if writes {
		// Check before creating: a refused directory is never left behind.
		// A missing one is checked through its parent.
		probe := path
		if errors.Is(err, os.ErrNotExist) {
			probe = filepath.Dir(path)
		}
		tmpfs, terr := resourceVolatileTmpfs(probe)
		if terr != nil {
			return nil, fmt.Errorf("resource journal: volatile filesystem: %w", terr)
		}
		if !tmpfs {
			return nil, errors.New("resource journal: volatile directory must be on tmpfs")
		}
		if errors.Is(err, os.ErrNotExist) {
			if err = os.Mkdir(path, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
				return nil, fmt.Errorf("resource journal: volatile mkdir: %w", err)
			}
			info, err = os.Lstat(path)
		}
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode().Perm() != 0o700 || !journalOwned(info) {
		return nil, errors.New("resource journal: volatile directory must be owner-only (0700)")
	}
	boot, err := resourceVolatileBootID()
	if err != nil {
		return nil, fmt.Errorf("resource journal: boot id: %w", err)
	}
	dir, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	if err = claimVolatileBoot(dir, boot+"\n"+journalDir+"\n"); err != nil {
		_ = dir.Close()
		return nil, err
	}
	return dir, nil
}

// claimVolatileBoot discards copies from another kernel boot and marks the
// directory as this boot's and this journal's. marker is the boot id and the
// journal directory, one per line.
func claimVolatileBoot(dir *os.Root, marker string) error {
	marked, err := dir.ReadFile(resourceVolatileBootFile)
	if err == nil && string(marked) == marker {
		return nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil {
		was, now := strings.SplitN(string(marked), "\n", 2), strings.SplitN(marker, "\n", 2)
		if was[0] == now[0] {
			return fmt.Errorf("resource journal: volatile directory holds another journal's copies (%q)", strings.TrimSpace(was[len(was)-1]))
		}
	}
	entries, err := fs.ReadDir(dir.FS(), ".")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() == resourceVolatileBootFile || !entry.Type().IsRegular() {
			continue
		}
		if err := dir.Remove(entry.Name()); err != nil {
			return err
		}
	}
	return replaceVolatileFile(dir, resourceVolatileBootFile, []byte(marker))
}

// replaceVolatileFile replaces one tmpfs file: private temporary file, then
// atomic rename. tmpfs has nothing to fsync.
func replaceVolatileFile(dir *os.Root, target string, b []byte) error {
	name := ".pending-" + rand.Text()
	f, err := dir.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = dir.Remove(name) }()
	n, err := f.Write(b)
	if err == nil && n != len(b) {
		err = io.ErrShortWrite
	}
	if err = errors.Join(err, f.Close()); err != nil {
		return err
	}
	return dir.Rename(name, target)
}

// readRecords reads every record in dir by file name. skip names the
// directory's non-record files; abandoned temporary files are removed.
func readRecords(dir *os.Root, skip ...string) (map[string]resourceJournalRecord, error) {
	entries, err := fs.ReadDir(dir.FS(), ".")
	if err != nil {
		return nil, err
	}
	records := make(map[string]resourceJournalRecord, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if isSkipped(name, skip) {
			continue
		}
		if strings.HasPrefix(name, ".pending-") && entry.Type().IsRegular() {
			if err := dir.Remove(name); err != nil {
				return nil, err
			}
			continue
		}
		if !entry.Type().IsRegular() || !strings.HasSuffix(name, ".json") {
			return nil, fmt.Errorf("unexpected journal entry %q", name)
		}
		r, err := readRecord(dir, name)
		if err != nil {
			return nil, err
		}
		records[name] = r
	}
	return records, nil
}

func isSkipped(name string, skip []string) bool {
	for _, s := range skip {
		if name == s {
			return true
		}
	}
	return false
}

// flushVolatileCopies commits every tmpfs copy to the persistent directory and
// then removes the copies, so a vmmd without volatile writes, or one that
// predates them, finds each record's newest version on disk. The caller holds
// j.mu, or owns j before it is published.
func (j *ResourceJournal) flushVolatileCopies() error {
	if j.volatile == nil {
		return nil
	}
	copies, err := readRecords(j.volatile, resourceVolatileBootFile)
	if err != nil || len(copies) == 0 {
		return err
	}
	for name, r := range copies {
		b, err := json.Marshal(r)
		if err != nil {
			return err
		}
		if err := j.persistFile(name, b, "", false, nil); err != nil {
			return err
		}
	}
	if err := j.commitSync(true); err != nil {
		return err
	}
	for name := range copies {
		if err := j.volatile.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}
