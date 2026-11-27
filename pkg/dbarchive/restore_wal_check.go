package dbarchive

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"time"
)

// Native images use little-endian WAL, 8 KiB pages and 16 MiB segments.
// The inspection budget bounds archive traffic and temporary storage per
// request. PostgreSQL performs the full replay when a restore runs.
const (
	walPageBytes        = 8 << 10
	walSegmentBytes     = 16 << 20
	walRecordHeader     = 24
	maxWALCheckSegments = 16
	maxWALCheckRecord   = walSegmentBytes
	walContinuation     = 1
	walLongHeader       = 2
	walOverwrite        = 8
)

var (
	errWALCheckLimit  = errors.New("dbarchive: WAL inspection limit")
	errWALCheckFormat = errors.New("dbarchive: unverified WAL format")
	walCRC            = crc32.MakeTable(crc32.Castagnoli)
)

func walPageMagic(major int) uint16 {
	switch major {
	case 14:
		return 0xD10D
	case 15:
		return 0xD110
	case 16:
		return 0xD113
	case 17:
		return 0xD116
	case 18:
		return 0xD118
	default:
		return 0
	}
}

func timelineWALName(timeline uint32, segment uint64) string {
	const segmentsPerLog = (1 << 32) / walSegmentBytes
	return fmt.Sprintf("%08X%08X%08X", timeline, segment/segmentsPerLog, segment%segmentsPerLog)
}

type restoreWALReader struct {
	store     Store
	id        string
	magic     uint16
	names     map[string]bool
	backupEnd uint64
	lineage   restoreLineage

	segNumber uint64
	segment   []byte
	checked   int
	systemID  uint64
	missing   string
}

func (r *restoreWALReader) scan(ctx context.Context, start uint64, target time.Time, latest bool) (time.Time, string, string) {
	var verified time.Time
	var previous uint64
	consistent := false
	at := start
	for ctx.Err() == nil && at >= start {
		record, position, next, err := r.record(ctx, at)
		switch {
		case errors.Is(err, ErrNotFound):
			if r.missing != "" {
				return verified, r.missing, "missing_wal_segment"
			}
			return verified, "", "target_not_reached"
		case errors.Is(err, errWALCheckLimit):
			return verified, "", "wal_inspection_limit"
		case err != nil:
			return verified, "", "wal_unverified"
		}
		if r.lineage.at(position) != r.lineage.at(next-1) ||
			(previous != 0 && binary.LittleEndian.Uint64(record[8:16]) != previous) {
			return verified, "", "wal_unverified"
		}
		stamp, err := transactionStopTime(record)
		if err != nil {
			return verified, "", "wal_unverified"
		}
		if position < r.backupEnd && next == r.backupEnd && backupEndMatches(record, start) {
			consistent = true
		}
		if stamp.After(target) {
			if !consistent {
				return verified, "", "backup_consistency_unverified"
			}
			return stamp, "", "archive_covered"
		}
		if latest && consistent && stamp.After(verified) {
			verified = stamp
		}
		// A segment switch skips the zero padding in the old segment.
		if record[17] == 0 && record[16]&0xf0 == 0x40 {
			next = (position/walSegmentBytes + 1) * walSegmentBytes
		}
		previous, at = position, next
	}
	return verified, "", "wal_unverified"
}

func backupEndMatches(record []byte, start uint64) bool {
	const xlogBackupEnd = 0x50
	// The backup-end record has a short main-data header and the start LSN.
	data := record[walRecordHeader:]
	return record[17] == 0 && record[16]&0xf0 == xlogBackupEnd &&
		len(data) == 10 && data[0] == 255 && data[1] == 8 &&
		binary.LittleEndian.Uint64(data[2:]) == start
}

func (r *restoreWALReader) record(ctx context.Context, at uint64) ([]byte, uint64, uint64, error) {
	page, header, flags, _, err := r.page(ctx, at)
	if err != nil {
		return nil, 0, 0, err
	}
	offset := int(at % walPageBytes)
	if offset == 0 {
		if flags&walContinuation != 0 {
			return nil, 0, 0, errWALCheckFormat
		}
		offset = header
		at += uint64(header)
	}
	if offset < header || offset%8 != 0 || len(page)-offset < 4 {
		return nil, 0, 0, errWALCheckFormat
	}
	length := int(binary.LittleEndian.Uint32(page[offset:]))
	if length < walRecordHeader {
		return nil, 0, 0, errWALCheckFormat
	}
	if length > maxWALCheckRecord {
		return nil, 0, 0, errWALCheckLimit
	}

	record := page[offset:]
	var next uint64
	if length <= len(record) {
		record = record[:length]
		next = at + uint64(length)
	} else {
		record = append(make([]byte, 0, length), record...)
		pageStart := at - uint64(offset)
		for len(record) < length {
			pageStart += walPageBytes
			page, header, flags, remaining, err := r.page(ctx, pageStart)
			if err != nil {
				return nil, 0, 0, err
			}
			if flags&walContinuation == 0 || remaining != length-len(record) {
				return nil, 0, 0, errWALCheckFormat
			}
			count := min(remaining, len(page)-header)
			record = append(record, page[header:header+count]...)
			next = pageStart + uint64(header+count)
		}
	}
	crc := crc32.Update(crc32.Checksum(record[walRecordHeader:], walCRC), walCRC, record[:20])
	if crc != binary.LittleEndian.Uint32(record[20:24]) {
		return nil, 0, 0, errWALCheckFormat
	}
	return record, at, (next + 7) &^ 7, nil
}

func (r *restoreWALReader) page(ctx context.Context, at uint64) ([]byte, int, uint16, int, error) {
	segment := at / walSegmentBytes
	if r.segment == nil || segment != r.segNumber {
		if err := r.load(ctx, segment); err != nil {
			return nil, 0, 0, 0, err
		}
	}
	pageStart := at - at%walPageBytes
	offset := int(pageStart % walSegmentBytes)
	page := r.segment[offset : offset+walPageBytes]
	flags := binary.LittleEndian.Uint16(page[2:4])
	if binary.LittleEndian.Uint16(page[:2]) != r.magic ||
		binary.LittleEndian.Uint32(page[4:8]) != r.lineage.at(pageStart) ||
		binary.LittleEndian.Uint64(page[8:16]) != pageStart || flags&walOverwrite != 0 {
		return nil, 0, 0, 0, errWALCheckFormat
	}
	header := 24
	if flags&walLongHeader != 0 {
		header = 40
	}
	if (offset == 0) != (header == 40) {
		return nil, 0, 0, 0, errWALCheckFormat
	}
	return page, header, flags, int(binary.LittleEndian.Uint32(page[16:20])), nil
}

func (r *restoreWALReader) load(ctx context.Context, segment uint64) error {
	if r.checked >= maxWALCheckSegments {
		return errWALCheckLimit
	}
	name := timelineWALName(r.lineage.fileTimeline(segment), segment)
	rc, size, err := r.store.OpenWAL(ctx, r.id, name)
	if errors.Is(err, ErrNotFound) {
		if r.names[name] || r.names[name+".partial"] {
			// A listed object disappeared or is incomplete. The lookup has
			// insufficient evidence for a definitive missing segment.
			return errWALCheckFormat
		}
		if segment*walSegmentBytes < r.backupEnd || r.lineage.startsIn(segment) {
			r.missing = name
		} else {
			for known := range r.names {
				if later, ok := archivedSegment(known); ok && later > segment && archivedTimeline(known) == r.lineage.fileTimeline(later) {
					r.missing = name
					break
				}
			}
		}
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }()
	if size != walSegmentBytes {
		return errWALCheckFormat
	}
	if r.segment == nil {
		r.segment = make([]byte, walSegmentBytes)
	}
	if _, err := io.ReadFull(rc, r.segment); err != nil {
		return err
	}
	sys := binary.LittleEndian.Uint64(r.segment[24:32])
	if binary.LittleEndian.Uint16(r.segment[:2]) != r.magic ||
		binary.LittleEndian.Uint16(r.segment[2:4])&walLongHeader == 0 ||
		binary.LittleEndian.Uint32(r.segment[32:36]) != walSegmentBytes ||
		binary.LittleEndian.Uint32(r.segment[36:40]) != walPageBytes ||
		sys == 0 || (r.systemID != 0 && r.systemID != sys) {
		return errWALCheckFormat
	}
	r.segNumber, r.systemID = segment, sys
	r.checked++
	return nil
}

// PostgreSQL's inclusive time recovery stops before the first transaction
// commit or abort strictly later than the target (xlogrecovery.c).
func transactionStopTime(record []byte) (time.Time, error) {
	if record[17] != 1 { // RM_XACT_ID
		return time.Time{}, nil
	}
	switch record[16] & 0x70 {
	case 0x00, 0x20, 0x30, 0x40: // commit, abort, and their prepared forms
	default:
		return time.Time{}, nil
	}
	data := record[walRecordHeader:]
	for len(data) > 0 {
		switch data[0] {
		case 252: // top-level transaction ID
			if len(data) < 5 {
				return time.Time{}, errWALCheckFormat
			}
			data = data[5:]
		case 253: // replication origin
			if len(data) < 3 {
				return time.Time{}, errWALCheckFormat
			}
			data = data[3:]
		case 254, 255: // long or short main-data header
			header := 2
			if data[0] == 254 {
				header = 5
			}
			if len(data) < header+8 {
				return time.Time{}, errWALCheckFormat
			}
			length := int(data[1])
			if header == 5 {
				length = int(binary.LittleEndian.Uint32(data[1:5]))
			}
			if length != len(data)-header {
				return time.Time{}, errWALCheckFormat
			}
			const postgresEpochSeconds = 946684800
			micros := int64(binary.LittleEndian.Uint64(data[header : header+8]))
			return time.Unix(postgresEpochSeconds+micros/1_000_000, micros%1_000_000*1000).UTC(), nil
		default:
			return time.Time{}, errWALCheckFormat
		}
	}
	return time.Time{}, errWALCheckFormat
}
