package releasebundle

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Overlay builds the manifest of a per-unit release
// (docs/adr/1650-per-unit-control-plane-releases.md). root is a copy of
// the base release in which the files named by replaced were swapped or
// added; every other file must still be the base release's.
//
// Only replaced files are hashed. Inherited entries are carried over from
// base after a size and mode check, because the deploy controller hashes the
// whole tree again before it activates the release: an inherited file that
// drifted still fails closed there, and the overlay does not pay for a
// second full hash of a tree that is mostly hard links to the active release.
func Overlay(base Manifest, root, releaseID, commitSHA string, replaced []string, now time.Time) (Manifest, error) {
	if err := ValidateManifest(base); err != nil {
		return Manifest{}, err
	}
	if releaseID == "" || commitSHA == "" {
		return Manifest{}, errors.New("releasebundle: overlay identity is incomplete")
	}
	if releaseID == base.ReleaseID {
		return Manifest{}, fmt.Errorf("releasebundle: overlay must not reuse base release id %q", releaseID)
	}
	if len(replaced) == 0 {
		return Manifest{}, errors.New("releasebundle: overlay replaces no files")
	}

	files := make(map[string]File, len(base.Files)+len(replaced))
	for _, file := range base.Files {
		files[file.Path] = file
	}
	swapped := make(map[string]struct{}, len(replaced))
	for _, rel := range replaced {
		if err := validatePath(rel); err != nil {
			return Manifest{}, err
		}
		if rel == ManifestName || isForbiddenPath(rel) {
			return Manifest{}, fmt.Errorf("releasebundle: overlay cannot replace %q", rel)
		}
		if _, dup := swapped[rel]; dup {
			return Manifest{}, fmt.Errorf("releasebundle: overlay replaces %q twice", rel)
		}
		swapped[rel] = struct{}{}
		path := filepath.Join(root, filepath.FromSlash(rel))
		info, err := os.Lstat(path)
		if err != nil {
			return Manifest{}, fmt.Errorf("releasebundle: overlay file %s: %w", rel, err)
		}
		if !info.Mode().IsRegular() {
			return Manifest{}, fmt.Errorf("releasebundle: overlay file %s is not a regular file", rel)
		}
		hash, err := hashFile(path)
		if err != nil {
			return Manifest{}, err
		}
		files[rel] = File{Path: rel, Mode: uint32(info.Mode().Perm()), Size: info.Size(), SHA256: hash}
	}

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == ManifestName {
			return nil
		}
		want, ok := files[rel]
		if !ok {
			return fmt.Errorf("releasebundle: overlay has a file the base release does not: %s", rel)
		}
		if _, ok := swapped[rel]; ok {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() != want.Size || uint32(info.Mode().Perm()) != want.Mode {
			return fmt.Errorf("releasebundle: overlay file %s differs from the base release", rel)
		}
		return nil
	})
	if err != nil {
		return Manifest{}, fmt.Errorf("releasebundle: overlay walk: %w", err)
	}

	out := Manifest{
		FormatVersion: 1,
		ReleaseID:     releaseID,
		CommitSHA:     commitSHA,
		Target:        base.Target,
		CreatedAt:     now.UTC(),
		Files:         make([]File, 0, len(files)),
	}
	var missing []string
	for _, file := range files {
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(file.Path))); err != nil {
			missing = append(missing, file.Path)
			continue
		}
		out.Files = append(out.Files, file)
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return Manifest{}, fmt.Errorf("releasebundle: overlay is missing base files: %s", strings.Join(missing, ", "))
	}
	sort.Slice(out.Files, func(i, j int) bool { return out.Files[i].Path < out.Files[j].Path })
	return out, ValidateManifest(out)
}
