// adr: 20261101085552

// Package requestjournal keeps the request-ID journal on the compute node that
// served each request, instead of one control-plane row per request. The
// gateway appends records in batches; a lookup reads a few index entries and
// one compressed frame per candidate segment.
//
// Layout, one directory per node:
//   - <start>.seg holds the records received in one segment span (start is the
//     span's Unix seconds) as frames: a 12-byte header (compressed length,
//     record count, CRC-32 of the compressed bytes) and a zstd frame.
//   - <start>.idx is written when the segment closes: a header (magic, entry
//     count, latest expiry, start) and the segment's (key hash, frame offset)
//     pairs sorted by hash.
//
// A segment whose index is missing at startup, after a crash, is rebuilt from
// its frames; a torn frame at its end is cut off.
package requestjournal

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/klauspost/compress/zstd"
)

// Record is one public request ID and where its evidence lives.
type Record struct {
	AccountID  uuid.UUID
	AppID      uuid.UUID
	RequestID  string
	TraceID    string
	ReceivedAt time.Time
	// ExpiresAt is when the plan's debugger retention ends for this record.
	ExpiresAt time.Time
}

// maxFieldBytes bounds the variable-length fields; the gateway already refuses
// request IDs over 128 bytes.
const maxFieldBytes = 255

const (
	frameHeaderBytes = 12
	indexEntryBytes  = 12
	indexHeaderBytes = 24
	indexMagic       = "RJI1"
)

// Config sizes a store. Zero values take the defaults in pkg/api.
type Config struct {
	Span   time.Duration
	Grace  time.Duration
	Budget int64
	Now    func() time.Time
	// OnEvict receives the record count of each segment removed to stay
	// within Budget before it expired.
	OnEvict func(records int64)
}

// Store is safe for concurrent use.
type Store struct {
	dir string
	cfg Config
	enc *zstd.Encoder
	dec *zstd.Decoder

	mu   sync.Mutex
	open map[int64]*openSegment
}

type openSegment struct {
	start      int64
	file       *os.File
	size       int64
	entries    []indexEntry
	maxExpires int64 // Unix ms
	records    int64
}

type indexEntry struct {
	hash   uint64
	offset uint32
}

// Open opens or creates the journal in dir, rebuilding any segment a crash
// left without an index.
func Open(dir string, cfg Config) (*Store, error) {
	if cfg.Span <= 0 || cfg.Grace < 0 || cfg.Budget <= 0 {
		return nil, fmt.Errorf("requestjournal: span, grace and budget are required")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("requestjournal: create %s: %w", dir, err)
	}
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedFastest))
	if err != nil {
		return nil, fmt.Errorf("requestjournal: zstd writer: %w", err)
	}
	dec, err := zstd.NewReader(nil)
	if err != nil {
		return nil, fmt.Errorf("requestjournal: zstd reader: %w", err)
	}
	s := &Store{dir: dir, cfg: cfg, enc: enc, dec: dec, open: make(map[int64]*openSegment)}
	starts, err := s.segmentStarts(".seg")
	if err != nil {
		return nil, err
	}
	for _, start := range starts {
		if _, err := os.Stat(s.path(start, ".idx")); err == nil {
			continue
		}
		seg, err := s.recover(start)
		if err != nil {
			return nil, err
		}
		s.open[start] = seg
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.closeDueLocked(cfg.Now()); err != nil {
		return nil, err
	}
	return s, nil
}

// Append writes records, one frame per segment they fall in. A record whose
// segment already closed goes to the segment that is open now; a lookup's
// window extends past its end to find it. Records with an empty or oversized
// request ID or trace ID are skipped and counted in the returned total.
func (s *Store) Append(records []Record) (skipped int, err error) {
	now := s.cfg.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	groups := make(map[int64][]Record)
	var order []int64
	for _, record := range records {
		if record.RequestID == "" || len(record.RequestID) > maxFieldBytes || len(record.TraceID) > maxFieldBytes {
			skipped++
			continue
		}
		start := s.startOf(record.ReceivedAt)
		if s.closedLocked(start, now) {
			start = s.startOf(now)
		}
		if _, ok := groups[start]; !ok {
			order = append(order, start)
		}
		groups[start] = append(groups[start], record)
	}
	for _, start := range order {
		if err := s.appendFrameLocked(start, groups[start]); err != nil {
			return skipped, err
		}
	}
	return skipped, s.closeDueLocked(now)
}

func (s *Store) appendFrameLocked(start int64, records []Record) error {
	seg, err := s.segmentLocked(start)
	if err != nil {
		return err
	}
	var payload []byte
	for _, record := range records {
		payload = appendRecord(payload, record)
	}
	compressed := s.enc.EncodeAll(payload, nil)
	frame := make([]byte, frameHeaderBytes, frameHeaderBytes+len(compressed))
	binary.BigEndian.PutUint32(frame[0:4], uint32(len(compressed)))
	binary.BigEndian.PutUint32(frame[4:8], uint32(len(records)))
	binary.BigEndian.PutUint32(frame[8:12], crc32.ChecksumIEEE(compressed))
	frame = append(frame, compressed...)
	if seg.size+int64(len(frame)) > 1<<32-1 {
		return fmt.Errorf("requestjournal: segment %d is full", start)
	}
	if _, err := seg.file.Write(frame); err != nil {
		return fmt.Errorf("requestjournal: append to segment %d: %w", start, err)
	}
	offset := uint32(seg.size)
	seg.size += int64(len(frame))
	for _, record := range records {
		seg.entries = append(seg.entries, indexEntry{hash: keyHash(record.AppID, record.RequestID), offset: offset})
		seg.maxExpires = max(seg.maxExpires, record.ExpiresAt.UnixMilli())
	}
	seg.records += int64(len(records))
	return nil
}

func (s *Store) segmentLocked(start int64) (*openSegment, error) {
	if seg := s.open[start]; seg != nil {
		return seg, nil
	}
	file, err := os.OpenFile(s.path(start, ".seg"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("requestjournal: open segment %d: %w", start, err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("requestjournal: stat segment %d: %w", start, err)
	}
	seg := &openSegment{start: start, file: file, size: info.Size()}
	s.open[start] = seg
	return seg, nil
}

// Lookup returns the newest unexpired record for the account, app and request
// ID received in [from, until).
func (s *Store) Lookup(accountID, appID uuid.UUID, requestID string, from, until time.Time) (Record, bool, error) {
	now := s.cfg.Now()
	hash := keyHash(appID, requestID)
	var best Record
	found := false
	last := s.startOf(until.Add(s.cfg.Span + s.cfg.Grace))
	for start := s.startOf(from); start <= last; start += int64(s.cfg.Span / time.Second) {
		offsets, err := s.offsets(start, hash)
		if err != nil {
			return Record{}, false, err
		}
		for _, offset := range offsets {
			records, err := s.readFrame(start, offset)
			if err != nil {
				return Record{}, false, err
			}
			for _, record := range records {
				if record.AccountID != accountID || record.AppID != appID || record.RequestID != requestID ||
					record.ReceivedAt.Before(from) || !record.ReceivedAt.Before(until) || !record.ExpiresAt.After(now) {
					continue
				}
				if !found || record.ReceivedAt.After(best.ReceivedAt) {
					best, found = record, true
				}
			}
		}
	}
	return best, found, nil
}

// offsets returns the distinct frame offsets that may hold hash in segment
// start.
func (s *Store) offsets(start int64, hash uint64) ([]uint32, error) {
	s.mu.Lock()
	if seg := s.open[start]; seg != nil {
		var out []uint32
		for _, entry := range seg.entries {
			if entry.hash == hash {
				out = append(out, entry.offset)
			}
		}
		s.mu.Unlock()
		return compactOffsets(out), nil
	}
	s.mu.Unlock()
	file, err := openSegmentFile(s.path(start, ".idx"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("requestjournal: open index %d: %w", start, err)
	}
	defer func() { _ = file.Close() }()
	header, err := readIndexHeader(file)
	if err != nil {
		return nil, fmt.Errorf("requestjournal: index %d: %w", start, err)
	}
	entryAt := func(i int) (indexEntry, error) {
		var raw [indexEntryBytes]byte
		if _, err := file.ReadAt(raw[:], int64(indexHeaderBytes+i*indexEntryBytes)); err != nil {
			return indexEntry{}, err
		}
		return indexEntry{hash: binary.BigEndian.Uint64(raw[0:8]), offset: binary.BigEndian.Uint32(raw[8:12])}, nil
	}
	var readErr error
	first := sort.Search(int(header.count), func(i int) bool {
		entry, err := entryAt(i)
		if err != nil {
			readErr = err
			return true
		}
		return entry.hash >= hash
	})
	if readErr != nil {
		return nil, fmt.Errorf("requestjournal: read index %d: %w", start, readErr)
	}
	var out []uint32
	for i := first; i < int(header.count); i++ {
		entry, err := entryAt(i)
		if err != nil {
			return nil, fmt.Errorf("requestjournal: read index %d: %w", start, err)
		}
		if entry.hash != hash {
			break
		}
		out = append(out, entry.offset)
	}
	return compactOffsets(out), nil
}

func (s *Store) readFrame(start int64, offset uint32) ([]Record, error) {
	file, err := openSegmentFile(s.path(start, ".seg"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil // expired or evicted since its offsets were read
	}
	if err != nil {
		return nil, fmt.Errorf("requestjournal: open segment %d: %w", start, err)
	}
	defer func() { _ = file.Close() }()
	records, _, err := s.frameAt(file, int64(offset))
	if err != nil {
		return nil, fmt.Errorf("requestjournal: segment %d frame %d: %w", start, offset, err)
	}
	return records, nil
}

// frameAt decodes the frame at offset and returns its records and length.
func (s *Store) frameAt(file io.ReaderAt, offset int64) ([]Record, int64, error) {
	var header [frameHeaderBytes]byte
	if _, err := file.ReadAt(header[:], offset); err != nil {
		return nil, 0, err
	}
	size := binary.BigEndian.Uint32(header[0:4])
	count := binary.BigEndian.Uint32(header[4:8])
	compressed := make([]byte, size)
	if _, err := file.ReadAt(compressed, offset+frameHeaderBytes); err != nil {
		return nil, 0, err
	}
	if crc32.ChecksumIEEE(compressed) != binary.BigEndian.Uint32(header[8:12]) {
		return nil, 0, errors.New("checksum mismatch")
	}
	payload, err := s.dec.DecodeAll(compressed, nil)
	if err != nil {
		return nil, 0, err
	}
	records := make([]Record, 0, count)
	for len(payload) > 0 {
		var record Record
		if record, payload, err = decodeRecord(payload); err != nil {
			return nil, 0, err
		}
		records = append(records, record)
	}
	if len(records) != int(count) {
		return nil, 0, fmt.Errorf("frame holds %d records, header says %d", len(records), count)
	}
	return records, frameHeaderBytes + int64(size), nil
}

// Sweep closes segments past their grace, removes segments whose records all
// expired, then removes the oldest closed segments until the directory fits
// the budget.
func (s *Store) Sweep() error {
	now := s.cfg.Now()
	s.mu.Lock()
	err := s.closeDueLocked(now)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	starts, err := s.segmentStarts(".idx")
	if err != nil {
		return err
	}
	type closed struct {
		start   int64
		bytes   int64
		records int64
	}
	var kept []closed
	var total int64
	for _, start := range starts {
		header, size, err := s.closedSegment(start)
		if err != nil {
			return err
		}
		if now.UnixMilli() >= header.maxExpires {
			if err := s.remove(start); err != nil {
				return err
			}
			continue
		}
		kept = append(kept, closed{start: start, bytes: size, records: int64(header.count)})
		total += size
	}
	s.mu.Lock()
	for _, seg := range s.open {
		total += seg.size + int64(len(seg.entries)*indexEntryBytes)
	}
	s.mu.Unlock()
	for _, segment := range kept {
		if total <= s.cfg.Budget {
			break
		}
		if err := s.remove(segment.start); err != nil {
			return err
		}
		total -= segment.bytes
		if s.cfg.OnEvict != nil {
			s.cfg.OnEvict(segment.records)
		}
	}
	return nil
}

// Close closes every open segment, writing its index.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var errs []error
	for start, seg := range s.open {
		if err := s.closeLocked(seg); err != nil {
			errs = append(errs, err)
		}
		delete(s.open, start)
	}
	_ = s.enc.Close()
	s.dec.Close()
	return errors.Join(errs...)
}

func (s *Store) closeDueLocked(now time.Time) error {
	for start, seg := range s.open {
		if !s.closedLocked(start, now) {
			continue
		}
		if err := s.closeLocked(seg); err != nil {
			return err
		}
		delete(s.open, start)
	}
	return nil
}

// closedLocked reports whether segment start no longer takes records: its span
// and grace have passed, or its index exists.
func (s *Store) closedLocked(start int64, now time.Time) bool {
	end := time.Unix(start, 0).Add(s.cfg.Span + s.cfg.Grace)
	if !now.Before(end) {
		return true
	}
	if s.open[start] != nil {
		return false
	}
	_, err := os.Stat(s.path(start, ".idx"))
	return err == nil
}

// closeLocked syncs the segment, then writes its sorted index with a rename,
// so an index never names bytes a crash could lose.
func (s *Store) closeLocked(seg *openSegment) error {
	if err := seg.file.Sync(); err != nil {
		return fmt.Errorf("requestjournal: sync segment %d: %w", seg.start, err)
	}
	if err := seg.file.Close(); err != nil {
		return fmt.Errorf("requestjournal: close segment %d: %w", seg.start, err)
	}
	slices.SortFunc(seg.entries, func(a, b indexEntry) int {
		switch {
		case a.hash < b.hash:
			return -1
		case a.hash > b.hash:
			return 1
		default:
			return int(a.offset) - int(b.offset)
		}
	})
	buf := make([]byte, indexHeaderBytes, indexHeaderBytes+len(seg.entries)*indexEntryBytes)
	copy(buf[0:4], indexMagic)
	binary.BigEndian.PutUint32(buf[4:8], uint32(len(seg.entries)))
	binary.BigEndian.PutUint64(buf[8:16], uint64(seg.maxExpires))
	binary.BigEndian.PutUint64(buf[16:24], uint64(seg.start))
	var entry [indexEntryBytes]byte
	for _, e := range seg.entries {
		binary.BigEndian.PutUint64(entry[0:8], e.hash)
		binary.BigEndian.PutUint32(entry[8:12], e.offset)
		buf = append(buf, entry[:]...)
	}
	tmp := s.path(seg.start, ".idx.tmp")
	if err := writeFileSync(tmp, buf); err != nil {
		return fmt.Errorf("requestjournal: write index %d: %w", seg.start, err)
	}
	if err := os.Rename(tmp, s.path(seg.start, ".idx")); err != nil {
		return fmt.Errorf("requestjournal: publish index %d: %w", seg.start, err)
	}
	return nil
}

// recover rebuilds the in-memory index of a segment without one, cutting off a
// torn frame at its end.
func (s *Store) recover(start int64) (*openSegment, error) {
	path := s.path(start, ".seg")
	file, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("requestjournal: open segment %d: %w", start, err)
	}
	seg := &openSegment{start: start, file: file}
	for {
		records, size, err := s.frameAt(file, seg.size)
		if err != nil {
			break
		}
		for _, record := range records {
			seg.entries = append(seg.entries, indexEntry{hash: keyHash(record.AppID, record.RequestID), offset: uint32(seg.size)})
			seg.maxExpires = max(seg.maxExpires, record.ExpiresAt.UnixMilli())
		}
		seg.records += int64(len(records))
		seg.size += size
	}
	if err := file.Truncate(seg.size); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("requestjournal: cut torn frame from segment %d: %w", start, err)
	}
	if _, err := file.Seek(seg.size, io.SeekStart); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("requestjournal: seek segment %d: %w", start, err)
	}
	return seg, nil
}

type indexHeader struct {
	count      uint32
	maxExpires int64
}

func readIndexHeader(file io.ReaderAt) (indexHeader, error) {
	var raw [indexHeaderBytes]byte
	if _, err := file.ReadAt(raw[:], 0); err != nil {
		return indexHeader{}, err
	}
	if !bytes.Equal(raw[0:4], []byte(indexMagic)) {
		return indexHeader{}, errors.New("not a journal index")
	}
	return indexHeader{count: binary.BigEndian.Uint32(raw[4:8]), maxExpires: int64(binary.BigEndian.Uint64(raw[8:16]))}, nil
}

func (s *Store) closedSegment(start int64) (indexHeader, int64, error) {
	file, err := openSegmentFile(s.path(start, ".idx"))
	if err != nil {
		return indexHeader{}, 0, fmt.Errorf("requestjournal: open index %d: %w", start, err)
	}
	defer func() { _ = file.Close() }()
	header, err := readIndexHeader(file)
	if err != nil {
		return indexHeader{}, 0, fmt.Errorf("requestjournal: index %d: %w", start, err)
	}
	var size int64
	for _, ext := range []string{".seg", ".idx"} {
		if info, err := os.Stat(s.path(start, ext)); err == nil {
			size += info.Size()
		}
	}
	return header, size, nil
}

// remove deletes the index first, so a crash in between leaves a segment
// Open rebuilds rather than an index naming a missing segment.
func (s *Store) remove(start int64) error {
	for _, ext := range []string{".idx", ".seg"} {
		if err := os.Remove(s.path(start, ext)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("requestjournal: remove segment %d: %w", start, err)
		}
	}
	return nil
}

func (s *Store) segmentStarts(ext string) ([]int64, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("requestjournal: list %s: %w", s.dir, err)
	}
	var starts []int64
	for _, entry := range entries {
		name, ok := strings.CutSuffix(entry.Name(), ext)
		if !ok {
			continue
		}
		if start, err := strconv.ParseInt(name, 10, 64); err == nil {
			starts = append(starts, start)
		}
	}
	slices.Sort(starts)
	return starts, nil
}

func (s *Store) startOf(t time.Time) int64 {
	return t.Truncate(s.cfg.Span).Unix()
}

func (s *Store) path(start int64, ext string) string {
	return filepath.Join(s.dir, strconv.FormatInt(start, 10)+ext)
}

func keyHash(appID uuid.UUID, requestID string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write(appID[:])
	_, _ = h.Write([]byte(requestID))
	return h.Sum64()
}

func compactOffsets(offsets []uint32) []uint32 {
	slices.Sort(offsets)
	return slices.Compact(offsets)
}

func appendRecord(b []byte, record Record) []byte {
	b = append(b, record.AccountID[:]...)
	b = append(b, record.AppID[:]...)
	b = binary.AppendUvarint(b, uint64(len(record.RequestID)))
	b = append(b, record.RequestID...)
	b = binary.AppendUvarint(b, uint64(len(record.TraceID)))
	b = append(b, record.TraceID...)
	b = binary.AppendVarint(b, record.ReceivedAt.UnixMilli())
	return binary.AppendVarint(b, record.ExpiresAt.UnixMilli())
}

func decodeRecord(b []byte) (Record, []byte, error) {
	var record Record
	if len(b) < 32 {
		return record, nil, errors.New("short record")
	}
	copy(record.AccountID[:], b[0:16])
	copy(record.AppID[:], b[16:32])
	b = b[32:]
	var err error
	if record.RequestID, b, err = decodeString(b); err != nil {
		return record, nil, err
	}
	if record.TraceID, b, err = decodeString(b); err != nil {
		return record, nil, err
	}
	received, n := binary.Varint(b)
	if n <= 0 {
		return record, nil, errors.New("bad received time")
	}
	b = b[n:]
	expires, n := binary.Varint(b)
	if n <= 0 {
		return record, nil, errors.New("bad expiry")
	}
	record.ReceivedAt, record.ExpiresAt = time.UnixMilli(received).UTC(), time.UnixMilli(expires).UTC()
	return record, b[n:], nil
}

func decodeString(b []byte) (string, []byte, error) {
	size, n := binary.Uvarint(b)
	if n <= 0 || size > maxFieldBytes || uint64(len(b)-n) < size {
		return "", nil, errors.New("bad field length")
	}
	return string(b[n : n+int(size)]), b[n+int(size):], nil
}

// openSegmentFile opens a journal file for reading.
func openSegmentFile(path string) (*os.File, error) {
	//nolint:forbidigo // path is <dir>/<unix seconds>.<ext> built by Store.path from an integer, never customer input.
	return os.Open(path)
}

func writeFileSync(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}
