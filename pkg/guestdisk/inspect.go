package guestdisk

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strconv"
	"strings"
)

// Kind is an inode type as debugfs reports it.
type Kind int

const (
	// Unknown means debugfs printed neither the inode nor "not found":
	// the lookup failed some other way (an unreadable or corrupt inode).
	Unknown Kind = iota
	Missing
	Regular
	Directory
	Symlink
	// Other is a device, FIFO or socket, including an overlayfs whiteout.
	Other
)

func (k Kind) String() string {
	switch k {
	case Missing:
		return "missing"
	case Regular:
		return "regular file"
	case Directory:
		return "directory"
	case Symlink:
		return "symlink"
	case Other:
		return "special file"
	default:
		return "unreadable entry"
	}
}

// Entry is what one path names in the image. debugfs does not follow a
// symlink in the last component, so Kind describes the entry itself.
type Entry struct {
	Kind Kind
	// Mode holds the permission, setuid, setgid and sticky bits (07777).
	Mode uint32
	UID  int
	GID  int
	Size int64
	// Data is the file's contents when the inspection asked to read it and
	// the entry is a regular file no larger than the requested limit.
	Data []byte
}

// Perm returns the permission bits, as fs.FileMode.Perm does.
func (e Entry) Perm() fs.FileMode { return fs.FileMode(e.Mode & 0o777) }

// Read asks Inspect for a regular file's contents, up to Max bytes.
type Read struct {
	Max int64
}

// marker is a debugfs command that fails harmlessly and whose echo on
// stdout cannot be forged by image contents: the name carries the session's
// random nonce. debugfs -f echoes every command as "debugfs: <command>\n"
// before running it.
func (s *Session) marker(tag string) []byte {
	s.seq++
	return []byte(fmt.Sprintf("stat /.gregale-guestdisk-%s-%d-%s", s.nonce, s.seq, tag))
}

func echo(cmd []byte) []byte {
	return append(append([]byte("debugfs: "), cmd...), '\n')
}

// Inspect reports what each path names. reads lists the paths whose contents
// to return as well; their outputs are bounded by Max, and an entry larger
// than Max comes back without Data.
func (s *Session) Inspect(ctx context.Context, paths []string, reads map[string]Read) (map[string]Entry, error) {
	if len(paths) == 0 {
		return map[string]Entry{}, nil
	}
	probes, err := s.newProbes(paths, reads)
	if err != nil {
		return nil, err
	}
	res, err := s.run(ctx, s.ed.debugfs, []string{"-f", "-", s.image}, probes.script, runLimits{output: probes.output})
	if err != nil {
		return nil, fmt.Errorf("guestdisk: inspect: %w", err)
	}
	return probes.parse(res.stdout, res.stderr)
}

// probeBatch is a set of path inspections as debugfs script lines. An edit
// appends one to its own script to read its changes back in the same run.
type probeBatch struct {
	probes []probe
	reads  map[string]Read
	end    []byte
	script []byte
	output int
}

type probe struct {
	path        string
	start, cont []byte
	read        bool
}

func (s *Session) newProbes(paths []string, reads map[string]Read) (*probeBatch, error) {
	var script bytes.Buffer
	b := &probeBatch{reads: reads, output: 64 << 10}
	seen := make(map[string]bool, len(paths))
	for _, p := range paths {
		if err := CheckPath(p); err != nil {
			return nil, err
		}
		if seen[p] {
			continue
		}
		seen[p] = true
		pr := probe{path: p, start: s.marker("s"), cont: s.marker("c")}
		fmt.Fprintf(&script, "%s\nstat %s\n%s\n", pr.start, p, pr.cont)
		if r, ok := reads[p]; ok {
			pr.read = true
			fmt.Fprintf(&script, "cat %s\n", p)
			b.output += int(r.Max) + 4096
		}
		b.output += 4096
		b.probes = append(b.probes, pr)
	}
	b.end = s.marker("end")
	fmt.Fprintf(&script, "%s\n", b.end)
	b.script = script.Bytes()
	return b, nil
}

func (b *probeBatch) parse(stdout, stderr []byte) (map[string]Entry, error) {
	notFound := notFoundPaths(stderr)
	out := make(map[string]Entry, len(b.probes))
	cursor := 0
	for i, pr := range b.probes {
		next := b.end
		if i+1 < len(b.probes) {
			next = b.probes[i+1].start
		}
		statBlock, err := between(stdout, &cursor, echo(pr.start), echo(pr.cont))
		if err != nil {
			return nil, err
		}
		entry, err := parseStat(statBlock, []byte("stat "+pr.path), notFound[pr.path])
		if err != nil {
			return nil, fmt.Errorf("guestdisk: inspect %s: %w", pr.path, err)
		}
		contBlock, err := between(stdout, &cursor, echo(pr.cont), echo(next))
		if err != nil {
			return nil, err
		}
		if pr.read && entry.Kind == Regular {
			data, ok := bytes.CutPrefix(contBlock, echo([]byte("cat "+pr.path)))
			if !ok {
				return nil, fmt.Errorf("guestdisk: inspect %s: missing cat output", pr.path)
			}
			if int64(len(data)) == entry.Size && entry.Size <= b.reads[pr.path].Max {
				entry.Data = data
			}
		}
		out[pr.path] = entry
	}
	return out, nil
}

// between returns the bytes after the first occurrence of start (at or past
// *cursor; nil means "from *cursor") up to the next occurrence of stop, and
// leaves *cursor at stop.
func between(stdout []byte, cursor *int, start, stop []byte) ([]byte, error) {
	from := *cursor
	if start != nil {
		i := bytes.Index(stdout[from:], start)
		if i < 0 {
			return nil, errors.New("guestdisk: debugfs output lost a command marker")
		}
		from += i + len(start)
	}
	j := bytes.Index(stdout[from:], stop)
	if j < 0 {
		return nil, errors.New("guestdisk: debugfs output lost a command marker")
	}
	*cursor = from + j
	return stdout[from : from+j], nil
}

var (
	statLine1 = regexp.MustCompile(`^Inode: \d+\s+Type: ([a-zA-Z ]+?)\s+Mode:\s+([0-7]+)\s+Flags:`)
	statLine3 = regexp.MustCompile(`^User:\s+(\d+)\s+Group:\s+(\d+)\s+(?:Project:\s+\d+\s+)?Size:\s+(\d+)`)
)

// parseStat reads the first and third lines of debugfs's stat output. They
// come before anything the guest controls (xattr values, symlink targets,
// extent lists), so later lines are never consulted.
func parseStat(block, cmd []byte, notFound bool) (Entry, error) {
	body, ok := bytes.CutPrefix(block, echo(cmd))
	if !ok {
		return Entry{}, errors.New("missing stat echo")
	}
	if len(body) == 0 {
		if notFound {
			return Entry{Kind: Missing}, nil
		}
		return Entry{Kind: Unknown}, nil
	}
	lines := strings.SplitN(string(body), "\n", 4)
	if len(lines) < 3 {
		return Entry{}, errors.New("short stat output")
	}
	m1 := statLine1.FindStringSubmatch(lines[0])
	m3 := statLine3.FindStringSubmatch(lines[2])
	if m1 == nil || m3 == nil {
		return Entry{}, errors.New("unrecognised stat output")
	}
	mode, err := strconv.ParseUint(m1[2], 8, 32)
	if err != nil {
		return Entry{}, err
	}
	uid, err1 := strconv.Atoi(m3[1])
	gid, err2 := strconv.Atoi(m3[2])
	size, err3 := strconv.ParseInt(m3[3], 10, 64)
	if err := errors.Join(err1, err2, err3); err != nil {
		return Entry{}, err
	}
	e := Entry{Mode: uint32(mode & 0o7777), UID: uid, GID: gid, Size: size}
	switch strings.TrimSpace(m1[1]) {
	case "regular":
		e.Kind = Regular
	case "directory":
		e.Kind = Directory
	case "symlink":
		e.Kind = Symlink
	default:
		e.Kind = Other
	}
	return e, nil
}

// notFoundPaths collects the paths debugfs reported as absent. Its messages
// read "<path>: File not found by ext2_lookup". The paths are the session's
// own validated strings, so a guest cannot make one appear.
func notFoundPaths(stderr []byte) map[string]bool {
	out := make(map[string]bool)
	for _, line := range strings.Split(string(stderr), "\n") {
		if p, ok := strings.CutSuffix(strings.TrimRight(line, " "), ": File not found by ext2_lookup"); ok && strings.HasPrefix(p, "/") {
			out[p] = true
		}
	}
	return out
}

// errListTooLong reports a listing past its entry bound.
var errListTooLong = errors.New("guestdisk: listing exceeds its entry bound")

// DirEntry is one name in a directory listing.
type DirEntry struct {
	Name string
	Kind Kind
	// Size is a regular file's apparent size; zero for anything else.
	Size int64
}

// List returns the entries of each directory, without "." and "..". A name
// the guest chose may hold any byte except "/" and NUL; callers must not
// splice one into a script without Quote.
func (s *Session) List(ctx context.Context, dirs []string, maxEntries int) (map[string][]DirEntry, error) {
	var script bytes.Buffer
	type probe struct {
		dir   string
		start []byte
	}
	var probes []probe
	for _, d := range dirs {
		if err := checkListPath(d); err != nil {
			return nil, err
		}
		pr := probe{dir: d, start: s.marker("l")}
		fmt.Fprintf(&script, "%s\nls -p %s\n", pr.start, Quote(d))
		probes = append(probes, pr)
	}
	end := s.marker("end")
	fmt.Fprintf(&script, "%s\n", end)
	res, err := s.run(ctx, s.ed.debugfs, []string{"-f", "-", s.image}, script.Bytes(), runLimits{output: 64<<10 + maxEntries*600})
	if errors.Is(err, errOutputCap) {
		return nil, fmt.Errorf("%w: more than %d entries", errListTooLong, maxEntries)
	}
	if err != nil {
		return nil, fmt.Errorf("guestdisk: list: %w", err)
	}
	out := make(map[string][]DirEntry, len(probes))
	cursor, total := 0, 0
	for i, pr := range probes {
		next := end
		if i+1 < len(probes) {
			next = probes[i+1].start
		}
		block, err := between(res.stdout, &cursor, echo(pr.start), echo(next))
		if err != nil {
			return nil, err
		}
		body, ok := bytes.CutPrefix(block, echo([]byte("ls -p "+Quote(pr.dir))))
		if !ok {
			return nil, fmt.Errorf("guestdisk: list %s: missing ls echo", pr.dir)
		}
		entries, err := parseList(body)
		if err != nil {
			return nil, fmt.Errorf("guestdisk: list %s: %w", pr.dir, err)
		}
		total += len(entries)
		if total > maxEntries {
			return nil, fmt.Errorf("%w: more than %d entries", errListTooLong, maxEntries)
		}
		out[pr.dir] = entries
	}
	return out, nil
}

// parseList reads `ls -p` output: one "/ino/mode/uid/gid/name/size/" record
// per entry, each followed by a newline. Names cannot contain "/", so
// splitting on it is unambiguous even when a name contains a newline.
func parseList(body []byte) ([]DirEntry, error) {
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, nil
	}
	fields := strings.Split(string(body), "/")
	// fields: "", ino, mode, uid, gid, name, size, "\n", ino, ..., size, "\n"
	if len(fields) < 8 || fields[0] != "" {
		return nil, errors.New("unrecognised listing")
	}
	var out []DirEntry
	for i := 1; i+6 <= len(fields); i += 7 {
		ino, mode, name, size, sep := fields[i], fields[i+1], fields[i+4], fields[i+5], fields[i+6]
		if _, err := strconv.ParseUint(ino, 10, 64); err != nil {
			return nil, errors.New("unrecognised listing inode")
		}
		m, err := strconv.ParseUint(mode, 8, 32)
		if err != nil {
			return nil, errors.New("unrecognised listing mode")
		}
		if sep != "\n" && sep != "\n\n" {
			return nil, errors.New("unrecognised listing record end")
		}
		if name == "." || name == ".." {
			continue
		}
		kind := Other
		switch m & 0o170000 {
		case 0o100000:
			kind = Regular
		case 0o040000:
			kind = Directory
		case 0o120000:
			kind = Symlink
		}
		e := DirEntry{Name: name, Kind: kind}
		if kind == Regular {
			if e.Size, err = strconv.ParseInt(size, 10, 64); err != nil || e.Size < 0 {
				return nil, errors.New("unrecognised listing size")
			}
		}
		out = append(out, e)
	}
	return out, nil
}

// checkListPath accepts a validated path or a Quote-able one: listing walks
// into directories whose names the guest chose.
func checkListPath(p string) error {
	if !strings.HasPrefix(p, "/") || strings.Contains(p, "//") || len(p) > 4*maxPathBytes {
		return fmt.Errorf("guestdisk: invalid image path %q", p)
	}
	return checkQuotable(p)
}

// Quote renders a path for a debugfs script: debugfs's command parser takes
// a double-quoted argument and reads "" inside it as one quote. A path with a
// control byte cannot be expressed on one script line.
func Quote(p string) string {
	if componentPathRE.MatchString(p) {
		return p
	}
	return `"` + strings.ReplaceAll(p, `"`, `""`) + `"`
}

var componentPathRE = regexp.MustCompile(`^[A-Za-z0-9._@+/-]+$`)

func checkQuotable(p string) error {
	for i := 0; i < len(p); i++ {
		if c := p[i]; c < 0x20 || c == 0x7f {
			return fmt.Errorf("guestdisk: path %q holds a control byte", p)
		}
	}
	return nil
}
