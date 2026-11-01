package logarchive

import (
	"bufio"
	"compress/gzip"
	"context"
	"crypto/md5" // #nosec G501 -- compared with the store's single-PUT ETag, not authentication.
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
)

// dailyBase names what one whole-day rebuild starts from.
type dailyBase struct {
	key     string // the day object
	history string // this node's local history: the object it last committed
	upload  string // the sealed fragment this flush appends
}

// copyDailyBase writes the day's archived gzip members to dst, so the
// whole-day replacement object keeps them, and leaves dst at its end. It
// reports whether the bucket already holds the upload fragment itself.
//
// The base is what the bucket holds now. Usually that is this node's local
// history: the history is copied and a GET conditional on its MD5 (the
// store's ETag for a single PUT) answers 304 without a body. Any other
// object means another writer replaced the day: a live migration keeps the
// instance ID, so the source and the destination node both ship the same
// day, and each node's PUT of its own history plus its new lines wiped the
// lines only the other had shipped (logs/traces audit 2026-10-31 A4). The
// base then becomes the bucket's object followed by every member of the
// local history it lacks (mergeDailyBase).
//
// When the history is gone (purged after RetentionDays, or a replaced disk)
// but lines for that day still arrive, the bucket's object is the base. A
// missing object leaves the local history, if any, as the base.
func (s *Shipper) copyDailyBase(ctx context.Context, dst *os.File, b dailyBase) (fragmentArchived bool, err error) {
	digest := md5.New() // #nosec G401 -- matches the store's single-PUT ETag; not a security check.
	haveHistory, err := copyArchiveHistory(io.MultiWriter(dst, digest), b.history)
	if err != nil {
		return false, err
	}
	etag := ""
	if haveHistory {
		etag = hex.EncodeToString(digest.Sum(nil))
	}
	body, current, err := s.s3.OpenObjectIfChanged(ctx, b.key, etag)
	if isNotFound(err) || err == nil && body == nil {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read archived day %s: %w", b.key, err)
	}
	defer func() { _ = body.Close() }()
	if haveHistory && current == etag {
		return false, nil // a store that ignores If-None-Match
	}
	return s.mergeDailyBase(dst, body, b, haveHistory)
}

// copyArchiveHistory copies the local history to dst and reports whether
// there was one.
func copyArchiveHistory(dst io.Writer, path string) (bool, error) {
	//nolint:forbidigo // path is the retained gzip sibling of a spool-owned upload.
	src, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("open archived history: %w", err)
	}
	defer func() { _ = src.Close() }()
	if _, err := io.Copy(dst, src); err != nil {
		return false, fmt.Errorf("copy archived history: %w", err)
	}
	return true, nil
}

// archiveMember is one gzip member of a day object: where it starts in the
// compressed stream, and the SHA-256 and length of its decompressed bytes.
type archiveMember struct {
	start int64
	sum   [sha256.Size]byte
	size  int64
}

// mergeDailyBase replaces what dst holds with the bucket's object, copied
// byte for byte, followed by the local history's members the object lacks.
//
// A member is one flush's JSONL fragment, and every later replacement
// copies it verbatim, so members compare by their decompressed bytes. Line
// sequence numbers cannot identify lines across nodes: the destination's
// ring numbers from 1 again.
//
// An object whose last member is a prefix of this flush's upload ends with
// this node's own earlier attempt at the same fragment: a PUT the store
// accepted although this node saw an error or crashed before committing it.
// The upload has grown since only by appended lines, so that member is
// dropped and the whole upload appended again, as the retry would have
// done from the history alone.
func (s *Shipper) mergeDailyBase(dst *os.File, bucket io.Reader, b dailyBase, haveHistory bool) (bool, error) {
	if err := rewind(dst, 0); err != nil {
		return false, err
	}
	src := &readErrors{r: bucket}
	sink := &writeErrors{w: dst}
	tee := io.TeeReader(src, sink)
	var members []archiveMember
	err := forEachGzipMember(tee, func(start int64, member io.Reader) error {
		sum, size, err := contentDigest(member)
		members = append(members, archiveMember{start: start, sum: sum, size: size})
		return err
	})
	if err == nil {
		// Bytes past the last member stay in the object too.
		_, err = io.Copy(io.Discard, tee)
	}
	switch {
	case src.err != nil:
		return false, fmt.Errorf("read archived day %s: %w", b.key, src.err)
	case sink.err != nil:
		return false, fmt.Errorf("write archive base: %w", sink.err)
	case err != nil:
		// Not a readable archive. Rebuild from this node's history alone,
		// as before the merge existed, rather than never shipping the day.
		s.log.Warn("logarchive.day_object_unreadable", "key", b.key, "err", err)
		if err := rewind(dst, 0); err != nil {
			return false, err
		}
		_, err := copyArchiveHistory(dst, b.history)
		return false, err
	}
	if n := len(members); n > 0 {
		last := members[n-1]
		own, err := uploadHasPrefix(b.upload, last)
		if err != nil {
			return false, err
		}
		if own {
			members = members[:n-1]
			if err := rewind(dst, last.start); err != nil {
				return false, err
			}
		}
	}
	seen := make(map[[sha256.Size]byte]bool, len(members))
	for _, m := range members {
		seen[m.sum] = true
	}
	fragment, err := uploadDigest(b.upload)
	if err != nil {
		return false, err
	}
	if haveHistory {
		if err := s.appendMissingMembers(dst, b.history, seen); err != nil {
			return false, err
		}
	}
	return seen[fragment], nil
}

// appendMissingMembers appends, recompressed, each member of the local
// history whose content is not in seen.
func (s *Shipper) appendMissingMembers(dst io.Writer, historyPath string, seen map[[sha256.Size]byte]bool) error {
	var keep []bool
	err := forEachHistoryMember(historyPath, func(_ int64, member io.Reader) error {
		sum, _, err := contentDigest(member)
		keep = append(keep, !seen[sum])
		seen[sum] = true
		return err
	})
	if err != nil {
		// The bucket's object stays the base; only lines this node alone
		// shipped before can be missing from it.
		s.log.Warn("logarchive.history_unreadable", "path", historyPath, "err", err)
		return nil
	}
	if !slices.Contains(keep, true) {
		return nil
	}
	i := 0
	err = forEachHistoryMember(historyPath, func(_ int64, member io.Reader) error {
		i++
		if !keep[i-1] {
			return nil
		}
		return appendGzipMember(dst, member)
	})
	if err != nil {
		return fmt.Errorf("merge archived history: %w", err)
	}
	return nil
}

func forEachHistoryMember(path string, fn func(int64, io.Reader) error) error {
	//nolint:forbidigo // path is the retained gzip sibling of a spool-owned upload.
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return forEachGzipMember(f, fn)
}

// forEachGzipMember calls fn with each member of a concatenated gzip stream
// in order, and the offset in r where the member starts. fn may leave part
// of a member unread. An empty stream has no members.
func forEachGzipMember(r io.Reader, fn func(start int64, member io.Reader) error) error {
	counted := &countingReader{r: r}
	// A bufio.Reader is an io.ByteReader, so the decompressor reads no
	// further than the member's trailer and offsets stay exact.
	br := bufio.NewReader(counted)
	zr, err := gzip.NewReader(br)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = zr.Close() }()
	var start int64
	for {
		zr.Multistream(false)
		if err := fn(start, zr); err != nil {
			return err
		}
		// #nosec G110 -- an archive object this platform wrote; draining
		// also verifies the member's checksum.
		if _, err := io.Copy(io.Discard, zr); err != nil {
			return err
		}
		start = counted.n - int64(br.Buffered())
		if err := zr.Reset(br); errors.Is(err, io.EOF) {
			return nil
		} else if err != nil {
			return err
		}
	}
}

func contentDigest(r io.Reader) ([sha256.Size]byte, int64, error) {
	h := sha256.New()
	var sum [sha256.Size]byte
	n, err := io.Copy(h, r)
	if err != nil {
		return sum, n, err
	}
	copy(sum[:], h.Sum(nil))
	return sum, n, nil
}

// uploadDigest is the SHA-256 of a sealed upload, the content of the gzip
// member a flush makes of it.
func uploadDigest(path string) ([sha256.Size]byte, error) {
	//nolint:forbidigo // path is a spool-owned sealed upload.
	f, err := os.Open(path)
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("open upload: %w", err)
	}
	defer func() { _ = f.Close() }()
	sum, _, err := contentDigest(f)
	if err != nil {
		return sum, fmt.Errorf("read upload: %w", err)
	}
	return sum, nil
}

// uploadHasPrefix reports whether the sealed upload starts with exactly the
// member's content.
func uploadHasPrefix(path string, m archiveMember) (bool, error) {
	//nolint:forbidigo // path is a spool-owned sealed upload.
	f, err := os.Open(path)
	if err != nil {
		return false, fmt.Errorf("open upload: %w", err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return false, fmt.Errorf("stat upload: %w", err)
	}
	if m.size == 0 || info.Size() < m.size {
		return false, nil
	}
	sum, _, err := contentDigest(io.LimitReader(f, m.size))
	if err != nil {
		return false, fmt.Errorf("read upload: %w", err)
	}
	return sum == m.sum, nil
}

// rewind truncates f to size and moves its offset there.
func rewind(f *os.File, size int64) error {
	if err := f.Truncate(size); err != nil {
		return fmt.Errorf("reset archive base: %w", err)
	}
	if _, err := f.Seek(size, io.SeekStart); err != nil {
		return fmt.Errorf("reset archive base: %w", err)
	}
	return nil
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// readErrors and writeErrors remember the first I/O error, so a failed read
// or write is not mistaken for an unreadable archive.
type readErrors struct {
	r   io.Reader
	err error
}

func (e *readErrors) Read(p []byte) (int, error) {
	n, err := e.r.Read(p)
	if err != nil && !errors.Is(err, io.EOF) && e.err == nil {
		e.err = err
	}
	return n, err
}

type writeErrors struct {
	w   io.Writer
	err error
}

func (e *writeErrors) Write(p []byte) (int, error) {
	n, err := e.w.Write(p)
	if err != nil && e.err == nil {
		e.err = err
	}
	return n, err
}
