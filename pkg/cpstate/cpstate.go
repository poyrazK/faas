// Package cpstate seals and opens the control plane's host-local state: the
// files a replacement control plane needs that live neither in PostgreSQL nor
// in the release bundle (ADR-1344 §8). The CA key, the apid HMAC keys,
// sealed.env and the per-daemon secrets are the examples; losing them with the
// host means re-issuing every certificate, invalidating every MFA recovery
// code and breaking audit joins.
//
// A bundle is an age-encrypted gzip tar holding index.json (each file's and
// directory's owner, group and mode) and the files under files/<path>. It is
// encrypted to operator-held recipients only, so the control plane can write a
// backup it can never read back; restoring needs the operator's identity.
//
// Restore writes the warm standby's staging layout (control_plane_standby:
// index.json plus files/<path>.b64), so the same takeover tasks install a
// restored bundle and a standby's staged copy.
package cpstate

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/user"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"filippo.io/age"
)

// IndexVersion is the bundle index format.
const IndexVersion = 1

// indexName is the index entry inside a bundle and in a staging directory.
const indexName = "index.json"

// maxFileBytes bounds one file in a bundle. Every control-plane state file is
// a key, a certificate or an env file; anything larger is not state.
const maxFileBytes = 16 << 20

// Entry is one file or directory with its ownership on the source host. The
// JSON field names match the standby staging index.
type Entry struct {
	Path  string `json:"path"`
	Owner string `json:"owner"`
	Group string `json:"group"`
	Mode  string `json:"mode"`
}

// Index describes a bundle.
type Index struct {
	Version     int       `json:"version"`
	Primary     string    `json:"primary"`
	CreatedAt   time.Time `json:"created_at"`
	Directories []Entry   `json:"directories"`
	Files       []Entry   `json:"files"`
}

// Spec selects what a bundle holds. A directory in Paths includes every
// regular file below it; a missing path is skipped, because most state files
// are optional. Exclude removes a path and everything below it.
type Spec struct {
	Paths   []string
	Exclude []string
}

// ParseSpec reads a spec: one absolute path per line, "!path" to exclude,
// blank lines and "#" comments ignored.
func ParseSpec(r io.Reader) (Spec, error) {
	var spec Spec
	scanner := bufio.NewScanner(r)
	for n := 1; scanner.Scan(); n++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		exclude := strings.HasPrefix(line, "!")
		p := strings.TrimSpace(strings.TrimPrefix(line, "!"))
		if !filepath.IsAbs(p) || filepath.Clean(p) != p {
			return Spec{}, fmt.Errorf("cpstate: spec line %d: %q must be a clean absolute path", n, p)
		}
		if exclude {
			spec.Exclude = append(spec.Exclude, p)
		} else {
			spec.Paths = append(spec.Paths, p)
		}
	}
	if err := scanner.Err(); err != nil {
		return Spec{}, fmt.Errorf("cpstate: read spec: %w", err)
	}
	if len(spec.Paths) == 0 {
		return Spec{}, errors.New("cpstate: spec selects no paths")
	}
	return spec, nil
}

func (s Spec) excluded(p string) bool {
	for _, e := range s.Exclude {
		if p == e || strings.HasPrefix(p, e+"/") {
			return true
		}
	}
	return false
}

// Collect lists what spec selects on this host: the selected directories and
// every directory below them, the parent directory of each single file, and
// every regular file. Symlinks and special files are not state and are
// skipped.
func Collect(spec Spec) (dirs, files []Entry, err error) {
	seenDir := map[string]bool{}
	seenFile := map[string]bool{}
	addDir := func(p string, info fs.FileInfo) error {
		if seenDir[p] || spec.excluded(p) {
			return nil
		}
		e, err := entryFor(p, info)
		if err != nil {
			return err
		}
		seenDir[p] = true
		dirs = append(dirs, e)
		return nil
	}
	addFile := func(p string, info fs.FileInfo) error {
		if seenFile[p] || spec.excluded(p) {
			return nil
		}
		if info.Size() > maxFileBytes {
			return fmt.Errorf("cpstate: %s is %d bytes, above the %d-byte state file limit", p, info.Size(), maxFileBytes)
		}
		e, err := entryFor(p, info)
		if err != nil {
			return err
		}
		seenFile[p] = true
		files = append(files, e)
		return nil
	}
	for _, root := range spec.Paths {
		if spec.excluded(root) {
			continue
		}
		info, statErr := os.Lstat(root)
		if errors.Is(statErr, fs.ErrNotExist) {
			continue
		}
		if statErr != nil {
			return nil, nil, fmt.Errorf("cpstate: stat %s: %w", root, statErr)
		}
		switch {
		case info.Mode().IsRegular():
			parent := filepath.Dir(root)
			if pinfo, perr := os.Stat(parent); perr == nil {
				if err := addDir(parent, pinfo); err != nil {
					return nil, nil, err
				}
			}
			if err := addFile(root, info); err != nil {
				return nil, nil, err
			}
		case info.IsDir():
			walkErr := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if spec.excluded(p) {
					if d.IsDir() {
						return filepath.SkipDir
					}
					return nil
				}
				info, err := d.Info()
				if err != nil {
					return err
				}
				switch {
				case info.IsDir():
					return addDir(p, info)
				case info.Mode().IsRegular():
					return addFile(p, info)
				}
				return nil
			})
			if walkErr != nil {
				return nil, nil, fmt.Errorf("cpstate: walk %s: %w", root, walkErr)
			}
		}
	}
	slices.SortFunc(dirs, func(a, b Entry) int { return strings.Compare(a.Path, b.Path) })
	slices.SortFunc(files, func(a, b Entry) int { return strings.Compare(a.Path, b.Path) })
	return dirs, files, nil
}

// entryFor records a path's owner, group and permission bits by name, because
// numeric ids differ between hosts.
func entryFor(p string, info fs.FileInfo) (Entry, error) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return Entry{}, fmt.Errorf("cpstate: %s: no ownership information", p)
	}
	owner, err := user.LookupId(strconv.FormatUint(uint64(st.Uid), 10))
	if err != nil {
		return Entry{}, fmt.Errorf("cpstate: %s: owner uid %d has no name: %w", p, st.Uid, err)
	}
	group, err := user.LookupGroupId(strconv.FormatUint(uint64(st.Gid), 10))
	if err != nil {
		return Entry{}, fmt.Errorf("cpstate: %s: group gid %d has no name: %w", p, st.Gid, err)
	}
	return Entry{Path: p, Owner: owner.Username, Group: group.Name, Mode: fmt.Sprintf("%04o", info.Mode().Perm())}, nil
}

// Seal writes an encrypted bundle of what spec selects to w and returns its
// index. Only recipients can open it.
func Seal(w io.Writer, spec Spec, recipients []age.Recipient, primary string, now time.Time) (Index, error) {
	if len(recipients) == 0 {
		return Index{}, errors.New("cpstate: no recipients; the bundle must be readable by an operator, never only by this host")
	}
	dirs, files, err := Collect(spec)
	if err != nil {
		return Index{}, err
	}
	if len(files) == 0 {
		return Index{}, errors.New("cpstate: spec matched no files on this host")
	}
	index := Index{Version: IndexVersion, Primary: primary, CreatedAt: now.UTC(), Directories: dirs, Files: files}

	enc, err := age.Encrypt(w, recipients...)
	if err != nil {
		return Index{}, fmt.Errorf("cpstate: start encryption: %w", err)
	}
	gz := gzip.NewWriter(enc)
	tw := tar.NewWriter(gz)
	body, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return Index{}, fmt.Errorf("cpstate: encode index: %w", err)
	}
	if err := writeTarFile(tw, indexName, body); err != nil {
		return Index{}, err
	}
	for _, f := range files {
		content, err := os.ReadFile(f.Path)
		if err != nil {
			return Index{}, fmt.Errorf("cpstate: read %s: %w", f.Path, err)
		}
		if err := writeTarFile(tw, "files"+f.Path, content); err != nil {
			return Index{}, err
		}
	}
	for _, c := range []io.Closer{tw, gz, enc} {
		if err := c.Close(); err != nil {
			return Index{}, fmt.Errorf("cpstate: finish bundle: %w", err)
		}
	}
	return index, nil
}

func writeTarFile(tw *tar.Writer, name string, content []byte) error {
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
		return fmt.Errorf("cpstate: write %s header: %w", name, err)
	}
	if _, err := tw.Write(content); err != nil {
		return fmt.Errorf("cpstate: write %s: %w", name, err)
	}
	return nil
}

// stageIndex is the warm standby's staging index (control_plane_standby).
type stageIndex struct {
	Primary     string  `json:"primary"`
	Directories []Entry `json:"directories"`
	Files       []Entry `json:"files"`
}

// Restore decrypts a bundle with identities and writes it to stageDir in the
// standby staging layout: stageDir/index.json and stageDir/files/<path>.b64.
// stageDir must not already hold a stage unless force is set, in which case
// the old stage is replaced. Every file in the bundle must be one the index
// lists, and the index must list every file.
func Restore(r io.Reader, identities []age.Identity, stageDir string, force bool) (Index, error) {
	if _, err := os.Stat(filepath.Join(stageDir, indexName)); err == nil && !force {
		return Index{}, fmt.Errorf("cpstate: %s already holds a stage; pass force to replace it", stageDir)
	}
	dec, err := age.Decrypt(r, identities...)
	if err != nil {
		return Index{}, fmt.Errorf("cpstate: decrypt bundle: %w", err)
	}
	gz, err := gzip.NewReader(dec)
	if err != nil {
		return Index{}, fmt.Errorf("cpstate: open bundle: %w", err)
	}
	tr := tar.NewReader(gz)

	hdr, err := tr.Next()
	if err != nil || hdr.Name != indexName {
		return Index{}, fmt.Errorf("cpstate: bundle does not start with %s", indexName)
	}
	var index Index
	if err := json.NewDecoder(io.LimitReader(tr, maxFileBytes)).Decode(&index); err != nil {
		return Index{}, fmt.Errorf("cpstate: decode index: %w", err)
	}
	if index.Version != IndexVersion {
		return Index{}, fmt.Errorf("cpstate: bundle index version %d, want %d", index.Version, IndexVersion)
	}
	want := map[string]bool{}
	for _, f := range index.Files {
		if !filepath.IsAbs(f.Path) || filepath.Clean(f.Path) != f.Path {
			return Index{}, fmt.Errorf("cpstate: index lists unsafe path %q", f.Path)
		}
		want["files"+f.Path] = true
	}

	tmp, err := os.MkdirTemp(filepath.Dir(stageDir), ".cpstate-restore-")
	if err != nil {
		return Index{}, fmt.Errorf("cpstate: create staging: %w", err)
	}
	defer os.RemoveAll(tmp)
	seen := map[string]bool{}
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Index{}, fmt.Errorf("cpstate: read bundle: %w", err)
		}
		name := hdr.Name
		if hdr.Typeflag != tar.TypeReg || !want[name] || seen[name] || path.Clean(name) != name {
			return Index{}, fmt.Errorf("cpstate: bundle entry %q is not a file the index lists", name)
		}
		seen[name] = true
		content, err := io.ReadAll(io.LimitReader(tr, maxFileBytes+1))
		if err != nil {
			return Index{}, fmt.Errorf("cpstate: read %s: %w", name, err)
		}
		if len(content) > maxFileBytes {
			return Index{}, fmt.Errorf("cpstate: %s exceeds the state file limit", name)
		}
		dest := filepath.Join(tmp, filepath.FromSlash(name)+".b64")
		if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
			return Index{}, fmt.Errorf("cpstate: stage %s: %w", name, err)
		}
		if err := os.WriteFile(dest, []byte(base64.StdEncoding.EncodeToString(content)), 0o600); err != nil {
			return Index{}, fmt.Errorf("cpstate: stage %s: %w", name, err)
		}
	}
	if len(seen) != len(want) {
		return Index{}, fmt.Errorf("cpstate: bundle holds %d of the %d files its index lists", len(seen), len(want))
	}
	body, err := json.MarshalIndent(stageIndex{Primary: index.Primary, Directories: index.Directories, Files: index.Files}, "", "  ")
	if err != nil {
		return Index{}, fmt.Errorf("cpstate: encode stage index: %w", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, indexName), body, 0o600); err != nil {
		return Index{}, fmt.Errorf("cpstate: write stage index: %w", err)
	}

	if err := os.RemoveAll(stageDir); err != nil {
		return Index{}, fmt.Errorf("cpstate: clear %s: %w", stageDir, err)
	}
	if err := os.Rename(tmp, stageDir); err != nil {
		return Index{}, fmt.Errorf("cpstate: install stage: %w", err)
	}
	if err := os.Chmod(stageDir, 0o700); err != nil {
		return Index{}, fmt.Errorf("cpstate: protect stage: %w", err)
	}
	return index, nil
}
