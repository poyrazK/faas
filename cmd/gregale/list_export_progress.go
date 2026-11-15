package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

type listExportProgressMode string

const (
	listExportProgressOff      listExportProgressMode = "off"
	listExportProgressPlain    listExportProgressMode = "plain"
	listExportProgressNDJSON   listExportProgressMode = "ndjson"
	listExportProgressInterval                        = 250 * time.Millisecond
)

func parseListExportProgress(value string) (listExportProgressMode, error) {
	switch listExportProgressMode(value) {
	case listExportProgressOff, listExportProgressPlain, listExportProgressNDJSON:
		return listExportProgressMode(value), nil
	default:
		return listExportProgressOff, errors.New("--progress must be off, plain or ndjson")
	}
}

type listExportProgressEvent struct {
	Type           string `json:"type"`
	SchemaVersion  int    `json:"schema_version"`
	Event          string `json:"event"`
	PagesCompleted int    `json:"pages_completed"`
	ItemsEmitted   int    `json:"items_emitted"`
	BytesWritten   int64  `json:"bytes_written"`
	ElapsedMS      int64  `json:"elapsed_ms"`
	Published      bool   `json:"published"`
}

// Best-effort diagnostics have no execution authority. A failed/short write
// disables subsequent progress without changing reads, publication or receipts.
type listExportProgress struct {
	mode          listExportProgressMode
	writer        io.Writer
	now           func() time.Time
	started, last time.Time
	elapsed       int64
	failed, done  bool
}

func newListExportProgress(w io.Writer, mode listExportProgressMode, now func() time.Time) *listExportProgress {
	if mode != listExportProgressPlain && mode != listExportProgressNDJSON {
		return nil
	}
	if now == nil {
		now = time.Now
	}
	started := now()
	p := &listExportProgress{mode: mode, writer: w, now: now, started: started, last: started}
	p.write("start", started, listStreamProgress{}, 0, false)
	return p
}

func (p *listExportProgress) observe(progress listStreamProgress, bytesWritten int64) {
	if p == nil || p.failed || p.done {
		return
	}
	now := p.now()
	if now.Sub(p.last) < listExportProgressInterval {
		return
	}
	p.write("update", now, progress, bytesWritten, false)
}

func (p *listExportProgress) finish(published bool, progress listStreamProgress, bytesWritten int64) {
	if p == nil || p.done {
		return
	}
	p.done = true
	event := "stopped"
	if published {
		event = "published"
	}
	p.write(event, p.now(), progress, bytesWritten, published)
}

func (p *listExportProgress) write(event string, now time.Time, progress listStreamProgress, bytesWritten int64, published bool) {
	if p.failed {
		return
	}
	p.elapsed = max(p.elapsed, now.Sub(p.started).Milliseconds())
	record := listExportProgressEvent{Type: "list_export_progress", SchemaVersion: 1, Event: event, PagesCompleted: progress.PagesCompleted,
		ItemsEmitted: progress.ItemsEmitted, BytesWritten: bytesWritten, ElapsedMS: p.elapsed, Published: published}
	var data []byte
	if p.mode == listExportProgressNDJSON {
		encoded, err := json.Marshal(record)
		if err != nil {
			p.failed = true
			return
		}
		data = append(encoded, '\n')
	} else {
		data = fmt.Appendf(nil, "Export %s: pages=%d items=%d bytes=%d elapsed=%dms\n", event, record.PagesCompleted, record.ItemsEmitted, record.BytesWritten, record.ElapsedMS)
	}
	n, err := p.writer.Write(data)
	p.failed = err != nil || n != len(data)
	p.last = now
}
