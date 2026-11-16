//go:build linux

package main

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func advisoryEvents(n int, pathLen int) []advisoryEvent {
	out := make([]advisoryEvent, n)
	for i := range out {
		out[i] = advisoryEvent{
			Path:   "/data/" + strings.Repeat("x", pathLen),
			Masks:  []string{"modify"},
			TsUnix: int64(1_700_000_000_000 + i),
		}
	}
	return out
}

// TestMarshalAdvisoryBatchWithinLimit_AlwaysParseable is the regression pin.
//
// An earlier implementation marshalled the whole batch and byte-sliced the
// result down to advisoryMaxBody. That produced a truncated JSON document the
// host could not unmarshal, so the whole batch was lost. Whatever this
// returns must parse.
func TestMarshalAdvisoryBatchWithinLimit_AlwaysParseable(t *testing.T) {
	cases := []struct {
		name    string
		events  []advisoryEvent
		limit   int
		wantAll bool
	}{
		{"empty batch", nil, advisoryMaxBody, true},
		{"comfortably under the limit", advisoryEvents(4, 10), advisoryMaxBody, true},
		{"full batch at the documented sizing", advisoryEvents(advisoryBatchCap, 20), advisoryMaxBody, true},
		{"over the limit by volume", advisoryEvents(advisoryBatchCap, 400), advisoryMaxBody, false},
		{"over the limit by a single huge event", advisoryEvents(1, 20000), advisoryMaxBody, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, sent, err := marshalAdvisoryBatchWithinLimit(tc.events, tc.limit)
			if err != nil {
				t.Fatalf("marshalAdvisoryBatchWithinLimit: %v", err)
			}
			if len(body) > tc.limit {
				t.Fatalf("body is %d bytes, over the %d limit", len(body), tc.limit)
			}
			if sent > len(tc.events) {
				t.Fatalf("reported %d events sent, batch had %d", sent, len(tc.events))
			}
			if len(body) == 0 {
				if len(tc.events) > 0 && tc.name != "over the limit by a single huge event" {
					t.Fatalf("returned no body for a batch that should have fit a prefix")
				}
				return
			}
			var decoded advisoryBatch
			if err := json.Unmarshal(body, &decoded); err != nil {
				t.Fatalf("returned body does not parse: %v\nbody: %.120q", err, body)
			}
			if len(decoded.Events) != sent {
				t.Fatalf("decoded %d events, reported %d", len(decoded.Events), sent)
			}
			if tc.wantAll && sent != len(tc.events) {
				t.Fatalf("dropped events that should have fit: sent %d of %d", sent, len(tc.events))
			}
		})
	}
}

// TestMarshalAdvisoryBatchWithinLimit_KeepsLargestPrefix pins that the binary
// search finds the true boundary: the returned count fits and one more does not.
func TestMarshalAdvisoryBatchWithinLimit_KeepsLargestPrefix(t *testing.T) {
	events := advisoryEvents(advisoryBatchCap, 200)
	body, sent, err := marshalAdvisoryBatchWithinLimit(events, advisoryMaxBody)
	if err != nil {
		t.Fatalf("marshalAdvisoryBatchWithinLimit: %v", err)
	}
	if sent == 0 || sent == len(events) {
		t.Skipf("fixture did not straddle the limit (sent %d of %d, %d bytes)", sent, len(events), len(body))
	}
	oneMore, err := json.Marshal(advisoryBatch{Events: events[:sent+1]})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(oneMore) <= advisoryMaxBody {
		t.Fatalf("prefix of %d fit in %d bytes but was not selected", sent+1, len(oneMore))
	}
}

func TestAdvisoryWatchRootsAddTheAppsOwnDataDirs(t *testing.T) {
	cases := []struct {
		workingDir string
		want       []string
	}{
		{"/app", []string{"/app/data", "/app/db"}},
		{"/srv/app/", []string{"/srv/app/data", "/srv/app/db"}},
		{"/", nil},   // /data and /db are already in the closed set
		{"", nil},    // the manifest default is /
		{"rel", nil}, // never trust a relative working directory
	}
	for _, tc := range cases {
		got := advisoryWatchRoots(tc.workingDir)
		want := append(append([]string(nil), statelessRuntimePaths...), tc.want...)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("advisoryWatchRoots(%q) = %v, want %v", tc.workingDir, got, want)
		}
	}
}

func TestAdvisoryPipeReportsEachPathOnce(t *testing.T) {
	p := newAdvisoryPipe()
	p.add(advisoryEvent{Path: "/app/data/app.db", Masks: []string{"create"}})
	p.add(advisoryEvent{Path: "/app/data/app.db", Masks: []string{"modify"}})
	p.add(advisoryEvent{Path: "/data/a", Masks: []string{"create"}})
	got := p.drain()
	want := []advisoryEvent{
		{Path: "/app/data/app.db", Masks: []string{"create", "modify"}},
		{Path: "/data/a", Masks: []string{"create"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("drain = %+v, want %+v", got, want)
	}
	p.add(advisoryEvent{Path: "/app/data/app.db", Masks: []string{"modify"}})
	if got := p.drain(); len(got) != 0 {
		t.Fatalf("a reported path was reported again: %+v", got)
	}

	p.add(advisoryEvent{Path: "/data/" + strings.Repeat("x", advisoryMaxPath), Masks: []string{"create"}})
	p.add(advisoryEvent{Path: "/data/\xff", Masks: []string{"create"}})
	got = p.drain()
	if len(got) != 1 || got[0].Path != "/data/�" {
		t.Fatalf("drain = %+v, want only the UTF-8-repaired path", got)
	}

	capped := newAdvisoryPipe()
	for i := 0; i < advisoryReportedPathCap+10; i++ {
		capped.add(advisoryEvent{Path: filepath.Join("/data", strings.Repeat("y", i+1)), Masks: []string{"create"}})
	}
	if got := capped.drain(); len(got) != advisoryReportedPathCap {
		t.Fatalf("pipe held %d paths, want the lifetime cap %d", len(got), advisoryReportedPathCap)
	}
}

// TestAdvisoryWatcherReportsWritesUnderWatchedRoots drives a real inotify fd
// over temporary directories: an existing root, a root created after the
// watch, a subdirectory, and a write outside every root.
func TestAdvisoryWatcherReportsWritesUnderWatchedRoots(t *testing.T) {
	tmp := t.TempDir()
	app := filepath.Join(tmp, "app")
	existing := filepath.Join(app, "data")
	later := filepath.Join(app, "db")
	if err := os.MkdirAll(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
	if err != nil {
		t.Skipf("inotify unavailable: %v", err)
	}
	t.Cleanup(func() { _ = unix.Close(fd) })
	w := newAdvisoryWatcher(fd, slog.New(slog.NewTextHandler(io.Discard, nil)))
	w.watchRoot(existing)
	w.watchRoot(later)

	var got []advisoryEvent
	pump := func() {
		buf := make([]byte, advisoryReadBuf)
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			n, err := unix.Read(fd, buf)
			if err == unix.EAGAIN {
				time.Sleep(10 * time.Millisecond)
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			w.handle(buf[:n], time.Now(), func(ev advisoryEvent) { got = append(got, ev) })
			return
		}
	}
	write := func(path string) {
		t.Helper()
		if err := os.WriteFile(path, []byte("state"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write(filepath.Join(existing, "app.db"))
	pump()
	write(filepath.Join(app, "outside.txt"))
	if err := os.Mkdir(later, 0o755); err != nil {
		t.Fatal(err)
	}
	pump() // the root's creation arms it
	write(filepath.Join(later, "late.db"))
	pump()
	if err := os.Mkdir(filepath.Join(existing, "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	pump() // the subdirectory's creation arms it
	write(filepath.Join(existing, "sessions", "s1"))
	pump()

	paths := map[string]bool{}
	for _, ev := range got {
		paths[ev.Path] = true
		if len(ev.Masks) == 0 {
			t.Errorf("event %s has no verbs", ev.Path)
		}
	}
	for _, want := range []string{
		filepath.Join(existing, "app.db"),
		filepath.Join(later, "late.db"),
		filepath.Join(existing, "sessions", "s1"),
	} {
		if !paths[want] {
			t.Errorf("no event for %s; got %+v", want, got)
		}
	}
	if paths[filepath.Join(app, "outside.txt")] {
		t.Errorf("reported a write outside every watched root")
	}
	for p := range paths {
		if p == existing || p == later || p == filepath.Join(existing, "sessions") {
			t.Errorf("reported a directory as a write: %s", p)
		}
	}
}

func TestAdvisoryShipperFramesEachPendingWrite(t *testing.T) {
	p := newAdvisoryPipe()
	frames := make(chan []byte, 4)
	done := make(chan struct{})
	go func() {
		advisoryShipper(p, func(frame []byte) error { frames <- frame; return nil }, slog.New(slog.NewTextHandler(io.Discard, nil)))
		close(done)
	}()
	for i := 0; i < advisoryBatchCap+1; i++ {
		p.add(advisoryEvent{Path: filepath.Join("/data", strings.Repeat("z", i+1)), Masks: []string{"create"}, TsUnix: 1})
	}
	var events []advisoryEvent
	for len(events) < advisoryBatchCap+1 {
		select {
		case frame := <-frames:
			if binary.BigEndian.Uint32(frame[0:4]) != VsockStatelessAdvisoryMsgType || int(binary.BigEndian.Uint32(frame[4:8])) != len(frame)-8 {
				t.Fatalf("bad frame header % x", frame[:8])
			}
			var batch advisoryBatch
			if err := json.Unmarshal(frame[8:], &batch); err != nil {
				t.Fatal(err)
			}
			if len(batch.Events) > advisoryBatchCap {
				t.Fatalf("frame carries %d events, cap %d", len(batch.Events), advisoryBatchCap)
			}
			events = append(events, batch.Events...)
		case <-time.After(5 * time.Second):
			t.Fatalf("shipped %d of %d events", len(events), advisoryBatchCap+1)
		}
	}
	p.close()
	<-done
}
