package dbarchive

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const (
	maxRestoreTimelineHistory = 64 << 10
	maxRestoreTimelines       = 32
)

type restoreTimeline struct {
	id    uint32
	begin uint64
}

// restoreLineage is the ancestry of the target timeline, oldest first.
// Fork positions are exclusive ends of the parent timeline.
type restoreLineage []restoreTimeline

func (l restoreLineage) at(lsn uint64) uint32 {
	for i := len(l) - 1; i >= 0; i-- {
		if lsn >= l[i].begin {
			return l[i].id
		}
	}
	return 0
}

func (l restoreLineage) fileTimeline(segment uint64) uint32 {
	// A promotion within a segment copies its prefix into the child's
	// segment. PostgreSQL looks for the newest eligible timeline first.
	return l.at((segment+1)*walSegmentBytes - 1)
}

func (l restoreLineage) startsIn(segment uint64) bool {
	for _, entry := range l {
		if entry.begin != 0 && entry.begin/walSegmentBytes == segment {
			return true
		}
	}
	return false
}

func (l restoreLineage) contains(timeline uint32) bool {
	for _, entry := range l {
		if entry.id == timeline {
			return true
		}
	}
	return false
}

func resolveRestoreLineage(ctx context.Context, store Store, id string, names map[string]bool, backupTimeline int, start, end uint64) (restoreLineage, uint32, string, string) {
	if backupTimeline < 1 || uint64(backupTimeline) > uint64(^uint32(0)) || names["00000001.history"] {
		return nil, 0, "", "timeline_unverified"
	}
	base := uint32(backupTimeline)
	target := base
	for name := range names {
		target = max(target, archivedTimeline(name))
	}
	lineage := restoreLineage{{id: 1}}
	if target != 1 {
		history := fmt.Sprintf("%08X.history", target)
		file, size, err := store.OpenWAL(ctx, id, history)
		switch {
		case errors.Is(err, ErrNotFound):
			return nil, target, history, "missing_timeline_history"
		case err != nil:
			return nil, target, "", "timeline_unverified"
		}
		defer func() { _ = file.Close() }()
		if size <= 0 || size > maxRestoreTimelineHistory {
			return nil, target, "", "timeline_unverified"
		}
		lineage, err = parseRestoreHistory(file, size, target)
		if err != nil {
			return nil, target, "", "timeline_unverified"
		}
	}
	if lineage.at(start) != base || lineage.at(end-1) != base {
		return nil, target, "", "timeline_unverified"
	}
	for name := range names {
		if !lineage.contains(archivedTimeline(name)) {
			// Competing branches need an authoritative choice of primary.
			return nil, target, "", "timeline_unverified"
		}
	}
	return lineage, target, "", ""
}

func parseRestoreHistory(file io.Reader, size int64, target uint32) (restoreLineage, error) {
	// Read exactly the advertised size. A short or truncated history
	// cannot establish which WAL belongs to the promoted primary.
	body, err := io.ReadAll(io.LimitReader(file, size+1))
	if err != nil || len(body) == 0 || int64(len(body)) != size || body[len(body)-1] != '\n' {
		return nil, errWALCheckFormat
	}
	lines := bufio.NewScanner(strings.NewReader(string(body)))
	var lineage restoreLineage
	var begin uint64
	var previous uint32
	for lines.Scan() {
		line := strings.TrimSpace(lines.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 || len(lineage) >= maxRestoreTimelines-1 {
			return nil, errWALCheckFormat
		}
		rawID, parseErr := strconv.ParseUint(fields[0], 10, 32)
		id := uint32(rawID)
		switchAt, ok := parseRestoreLSN(fields[1])
		if parseErr != nil || !ok || switchAt <= begin || id <= previous || id >= target ||
			(len(lineage) == 0 && id != 1) {
			return nil, errWALCheckFormat
		}
		lineage = append(lineage, restoreTimeline{id: id, begin: begin})
		begin, previous = switchAt, id
	}
	if lines.Err() != nil || len(lineage) == 0 {
		return nil, errWALCheckFormat
	}
	return append(lineage, restoreTimeline{id: target, begin: begin}), nil
}

func archivedTimeline(name string) uint32 {
	if len(name) < 8 {
		return 0
	}
	id, _ := strconv.ParseUint(name[:8], 16, 32)
	return uint32(id)
}

func archivedSegment(name string) (uint64, bool) {
	if len(name) != 24 {
		return 0, false
	}
	const segmentsPerLog = (1 << 32) / walSegmentBytes
	log, logErr := strconv.ParseUint(name[8:16], 16, 32)
	segment, segErr := strconv.ParseUint(name[16:], 16, 32)
	return log*segmentsPerLog + segment, logErr == nil && segErr == nil && segment < segmentsPerLog
}
