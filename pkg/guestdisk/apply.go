package guestdisk

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

// File is one file a session leaves in the image.
type File struct {
	Path string
	Data []byte
	// Mode is the permission bits (07777) of a new file.
	Mode uint32
	// KeepAttrs keeps an existing regular file's owner and mode, as an
	// in-place rewrite would. Otherwise the file always ends up root-owned
	// with Mode.
	KeepAttrs bool
}

// PathError attributes a failure to one path the caller asked for.
type PathError struct {
	Path string
	Err  error
}

func (e *PathError) Error() string { return e.Path + ": " + e.Err.Error() }
func (e *PathError) Unwrap() error { return e.Err }

// ErrNotDirectory reports a path component that exists but is not a real
// directory. A symlink counts: debugfs does not follow one in a parent
// component, and following it would let the guest pick where the host's
// write lands.
var ErrNotDirectory = errors.New("not a directory")

// ErrIsDirectory reports a directory where a file was to be written.
var ErrIsDirectory = errors.New("is a directory")

// ErrUnreadable reports an entry debugfs could not look up.
var ErrUnreadable = errors.New("entry is unreadable")

const (
	newDirMode      = 0o755
	maxFileBytes    = 16 << 20
	maxRemoveDepth  = 32
	maxRemoveTotals = 10000
)

// WriteFiles makes every file hold its Data. Missing parent directories are
// created root-owned with mode 0755. A file, symlink or special file already
// at a path is replaced; a directory there, or a parent that is not a
// directory, fails with a *PathError naming the file. A later File with the
// same Path wins.
func (s *Session) WriteFiles(ctx context.Context, files []File) error {
	return s.WriteFilesAfter(ctx, files, nil)
}

// WriteFilesAfter is WriteFiles for a caller that has already inspected the
// files' paths and their parents: state saves a separate inspection run, and
// only paths missing from it are inspected again. The image must not have
// changed since.
func (s *Session) WriteFilesAfter(ctx context.Context, files []File, inspected map[string]Entry) error {
	if len(files) == 0 {
		return nil
	}
	var need []string
	for _, f := range files {
		if err := CheckPath(f.Path); err != nil {
			return &PathError{Path: f.Path, Err: err}
		}
		if len(f.Data) > maxFileBytes {
			return &PathError{Path: f.Path, Err: fmt.Errorf("%d bytes exceeds %d", len(f.Data), maxFileBytes)}
		}
		if f.Mode&^0o7777 != 0 {
			return &PathError{Path: f.Path, Err: fmt.Errorf("invalid mode %o", f.Mode)}
		}
		need = append(need, components(f.Path)...)
	}
	state := make(map[string]Entry, len(need))
	var missing []string
	for _, p := range need {
		if e, ok := inspected[p]; ok {
			state[p] = e
		} else {
			missing = append(missing, p)
		}
	}
	if len(missing) > 0 {
		got, err := s.Inspect(ctx, missing, nil)
		if err != nil {
			return err
		}
		maps.Copy(state, got)
	}
	in := filepath.Join(s.scratch, "in")
	if err := s.mkScratchDir(in); err != nil {
		return err
	}
	var script bytes.Buffer
	final := make(map[string]written)
	var order, dirs []string
	for i, f := range files {
		comps := components(f.Path)
		for _, dir := range comps[:len(comps)-1] {
			switch e := state[dir]; e.Kind {
			case Directory:
			case Missing:
				fmt.Fprintf(&script, "mkdir %s\nsif %s mode 040%o\nsif %s uid 0\nsif %s gid 0\n", dir, dir, newDirMode, dir, dir)
				state[dir] = Entry{Kind: Directory, Mode: newDirMode}
				dirs = append(dirs, dir)
			case Unknown:
				return &PathError{Path: f.Path, Err: fmt.Errorf("%s: %w", dir, ErrUnreadable)}
			default:
				return &PathError{Path: f.Path, Err: fmt.Errorf("%s is a %s: %w", dir, e.Kind, ErrNotDirectory)}
			}
		}
		mode, uid, gid := f.Mode, 0, 0
		switch e := state[f.Path]; e.Kind {
		case Missing:
		case Regular:
			if f.KeepAttrs {
				mode, uid, gid = e.Mode, e.UID, e.GID
			}
			fmt.Fprintf(&script, "rm %s\n", f.Path)
		case Symlink, Other:
			fmt.Fprintf(&script, "rm %s\n", f.Path)
		case Directory:
			return &PathError{Path: f.Path, Err: ErrIsDirectory}
		default:
			return &PathError{Path: f.Path, Err: ErrUnreadable}
		}
		src := filepath.Join(in, strconv.Itoa(i))
		if err := s.writeScratchFile(src, f.Data); err != nil {
			return err
		}
		fmt.Fprintf(&script, "write %s %s\nsif %s mode 0100%o\nsif %s uid %d\nsif %s gid %d\n", s.toolScratch(src), f.Path, f.Path, mode, f.Path, uid, f.Path, gid)
		state[f.Path] = Entry{Kind: Regular, Mode: mode, UID: uid, GID: gid}
		if _, ok := final[f.Path]; !ok {
			order = append(order, f.Path)
		}
		final[f.Path] = written{data: f.Data, mode: mode, uid: uid, gid: gid}
	}
	// Read everything back in the same run: debugfs keeps going after a
	// failed command, so its silence is not proof.
	reads := make(map[string]Read, len(order))
	for _, p := range order {
		reads[p] = Read{Max: int64(len(final[p].data))}
	}
	probes, err := s.newProbes(append(append([]string(nil), dirs...), order...), reads)
	if err != nil {
		return err
	}
	script.Write(probes.script)
	got, err := s.applyScript(ctx, script.Bytes(), probes, nil, []string{s.toolScratch(in)})
	if err != nil {
		return err
	}
	for _, d := range dirs {
		if got[d].Kind != Directory {
			return &PathError{Path: d, Err: errors.New("directory was not created")}
		}
	}
	for _, p := range order {
		want, e := final[p], got[p]
		if e.Kind != Regular || e.Mode != want.mode || e.UID != want.uid || e.GID != want.gid || e.Size != int64(len(want.data)) || !bytes.Equal(e.Data, want.data) {
			return &PathError{Path: p, Err: fmt.Errorf("write did not take effect (found %s mode %o owner %d:%d size %d)", e.Kind, e.Mode, e.UID, e.GID, e.Size)}
		}
	}
	return nil
}

// written is what one planned write must leave behind.
type written struct {
	data     []byte
	mode     uint32
	uid, gid int
}

// RemoveAll removes each path and, for a directory, everything beneath it.
// An absent path, or one beneath a non-directory, is not an error. A name
// that cannot be written on a debugfs script line (one holding a control
// byte) fails the removal rather than leaving it half done.
func (s *Session) RemoveAll(ctx context.Context, paths []string) error {
	var probe []string
	for _, p := range paths {
		if err := CheckPath(p); err != nil {
			return &PathError{Path: p, Err: err}
		}
		probe = append(probe, components(p)...)
	}
	state, err := s.Inspect(ctx, probe, nil)
	if err != nil {
		return err
	}
	var files, trees []string
	for _, p := range paths {
		comps := components(p)
		reachable := true
		for _, dir := range comps[:len(comps)-1] {
			if state[dir].Kind != Directory {
				reachable = false
				break
			}
		}
		if !reachable {
			continue
		}
		switch e := state[p]; e.Kind {
		case Missing:
		case Directory:
			trees = append(trees, p)
		case Unknown:
			return &PathError{Path: p, Err: ErrUnreadable}
		default:
			files = append(files, p)
		}
	}
	var script bytes.Buffer
	for _, f := range files {
		fmt.Fprintf(&script, "rm %s\n", f)
	}
	removals, err := s.treeRemovals(ctx, trees)
	if err != nil {
		return err
	}
	script.Write(removals)
	if script.Len() == 0 {
		return nil
	}
	removed := append(files, trees...)
	probes, err := s.newProbes(removed, nil)
	if err != nil {
		return err
	}
	script.Write(probes.script)
	expectMissing := make(map[string]bool, len(removed))
	for _, p := range removed {
		expectMissing[p] = true
	}
	got, err := s.applyScript(ctx, script.Bytes(), probes, expectMissing, nil)
	if err != nil {
		return err
	}
	for _, p := range removed {
		if got[p].Kind != Missing {
			return &PathError{Path: p, Err: errors.New("removal did not take effect")}
		}
	}
	return nil
}

// treeRemovals lists each tree breadth first and returns the commands that
// empty and remove it, deepest entries first.
func (s *Session) treeRemovals(ctx context.Context, trees []string) ([]byte, error) {
	if len(trees) == 0 {
		return nil, nil
	}
	type dir struct {
		path  string
		depth int
	}
	var removeDirs []dir
	var removeFiles []string
	frontier := make([]dir, 0, len(trees))
	for _, t := range trees {
		frontier = append(frontier, dir{path: t})
	}
	total := 0
	for len(frontier) > 0 {
		paths := make([]string, len(frontier))
		for i, d := range frontier {
			paths[i] = d.path
		}
		listing, err := s.List(ctx, paths, maxRemoveTotals-total)
		if err != nil {
			return nil, err
		}
		var next []dir
		for _, d := range frontier {
			removeDirs = append(removeDirs, d)
			for _, e := range listing[d.path] {
				total++
				if total > maxRemoveTotals {
					return nil, fmt.Errorf("guestdisk: more than %d entries to remove", maxRemoveTotals)
				}
				child := path.Join(d.path, e.Name)
				if e.Name == "" || strings.Contains(e.Name, "/") {
					return nil, fmt.Errorf("guestdisk: invalid name under %s", d.path)
				}
				if err := checkQuotable(child); err != nil {
					return nil, err
				}
				if e.Kind == Directory {
					if d.depth+1 > maxRemoveDepth {
						return nil, fmt.Errorf("guestdisk: %s is nested deeper than %d", d.path, maxRemoveDepth)
					}
					next = append(next, dir{path: child, depth: d.depth + 1})
					continue
				}
				removeFiles = append(removeFiles, child)
			}
		}
		frontier = next
	}
	var script bytes.Buffer
	for _, f := range removeFiles {
		fmt.Fprintf(&script, "rm %s\n", Quote(f))
	}
	for i := len(removeDirs) - 1; i >= 0; i-- {
		fmt.Fprintf(&script, "rmdir %s\n", Quote(removeDirs[i].path))
	}
	return script.Bytes(), nil
}

// applyScript runs a write script that ends with a probe batch and returns
// what the probes saw. Every edit command was planned to succeed, so stderr
// may hold only debugfs's version banner, the probe markers' own "not found"
// lines and, for paths in expectMissing, theirs.
func (s *Session) applyScript(ctx context.Context, script []byte, probes *probeBatch, expectMissing map[string]bool, readDirs []string) (map[string]Entry, error) {
	res, err := s.run(ctx, s.ed.debugfs, []string{"-w", "-f", "-", s.toolImage()}, script, runLimits{output: probes.output + 1<<20, readDirs: readDirs})
	if err != nil {
		return nil, fmt.Errorf("guestdisk: edit: %w", err)
	}
	if res.exit != 0 {
		return nil, fmt.Errorf("guestdisk: edit: debugfs exit %d: %s", res.exit, firstLines(res.stderr, 4))
	}
	if problems := s.unexpectedStderr(res.stderr, expectMissing); len(problems) > 0 {
		return nil, fmt.Errorf("guestdisk: edit: %s", strings.Join(problems, "; "))
	}
	got, err := probes.parse(res.stdout, res.stderr)
	if err != nil {
		return nil, err
	}
	return got, syncImage(s.image)
}

func (s *Session) unexpectedStderr(stderr []byte, expectMissing map[string]bool) []string {
	var out []string
	for _, line := range strings.Split(string(stderr), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "":
			continue
		case strings.HasPrefix(line, "debugfs ") && strings.HasSuffix(line, ")"):
			continue
		case strings.Contains(line, "/.gregale-guestdisk-"+s.nonce+"-"):
			continue
		}
		if p, ok := strings.CutSuffix(line, ": File not found by ext2_lookup"); ok && expectMissing[p] {
			continue
		}
		out = append(out, line)
		if len(out) == 4 {
			break
		}
	}
	return out
}

// syncImage flushes the edits before the caller boots a VM on the image.
func syncImage(image string) error {
	f, err := os.OpenFile(image, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("guestdisk: open image for sync: %w", err)
	}
	return errors.Join(f.Sync(), f.Close())
}

func (s *Session) mkScratchDir(dir string) error {
	if err := os.Mkdir(dir, 0o700); err != nil && !os.IsExist(err) {
		return fmt.Errorf("guestdisk: scratch: %w", err)
	}
	if os.Geteuid() == 0 {
		if err := os.Chown(dir, s.uid, s.gid); err != nil {
			return fmt.Errorf("guestdisk: scratch: %w", err)
		}
	}
	return nil
}

func (s *Session) writeScratchFile(name string, data []byte) error {
	if err := os.WriteFile(name, data, 0o400); err != nil {
		return fmt.Errorf("guestdisk: scratch: %w", err)
	}
	if os.Geteuid() == 0 {
		if err := os.Chown(name, s.uid, s.gid); err != nil {
			return fmt.Errorf("guestdisk: scratch: %w", err)
		}
	}
	return nil
}
