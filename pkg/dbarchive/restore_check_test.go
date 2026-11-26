package dbarchive

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestArchiveRestoreCheck(t *testing.T) {
	store := newStore(t)
	ctx := t.Context()
	s := &Server{Store: store, Backups: store}
	start := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	target := start.Add(time.Minute)
	if _, err := store.PutBackup(ctx, dbA, bytes.NewReader(baseTar(t, "0/2000028", restoreWALName(2), start, "base")), start); err != nil {
		t.Fatal(err)
	}
	history := "START WAL LOCATION: 0/2000028\nSTOP WAL LOCATION: 0/2000050\nSTOP TIME: " + start.UTC().Format("2006-01-02 15:04:05 MST") + "\n"
	putFile(t, store, dbA, restoreWALName(2)+".00000028.backup", history)
	const longHeader = 40
	wal := make([]byte, walSegmentBytes)
	install := func(segment int, record func([]byte)) {
		t.Helper()
		clear(wal)
		addr := uint64(segment) * walSegmentBytes
		writeCheckPageHeader(wal, addr, walLongHeader, 0)
		record(wal[longHeader:])
		if err := store.PutWAL(ctx, dbA, restoreWALName(uint64(segment)), bytes.NewReader(wal), walSegmentBytes); err != nil {
			t.Fatal(err)
		}
	}
	install(2, func(b []byte) {
		const startLSN = 2*walSegmentBytes + longHeader
		backupEnd := []byte{255, 8, 0, 0, 0, 0, 0, 0, 0, 0}
		binary.LittleEndian.PutUint64(backupEnd[2:], startLSN)
		commit := writeCheckRecord(b, 0x50, 0, 0, backupEnd)
		switchRecord := commit + writeCheckRecord(b[commit:], 0, 1, startLSN, checkTransactionData(target))
		writeCheckRecord(b[switchRecord:], 0x40, 0, startLSN+uint64(commit), nil)
	})
	install(3, func(b []byte) {
		writeCheckRecord(b, 0x40, 0, 2*walSegmentBytes+120, nil)
	})
	later := target.Add(time.Second)
	install(4, func(b []byte) {
		writeCheckRecord(b, 0, 1, 3*walSegmentBytes+40, checkTransactionData(later))
	})

	check := s.checkRestore(ctx, dbA, target, 16)
	if check.Outcome != RestoreCovered || check.WALStopTime == nil || !check.WALStopTime.Equal(later) ||
		check.BackupCompleted == nil || !check.BackupCompleted.Equal(start) {
		t.Fatalf("covered check = %+v", check)
	}
	if got := s.checkRestore(ctx, dbA, start.Add(-time.Minute), 16); got.Outcome != RestoreNoBaseBackup {
		t.Fatalf("too-old target = %+v", got)
	}

	t.Run("latest verified point in the primary archive", func(t *testing.T) {
		host, client := net.Pipe()
		defer func() { _ = client.Close() }()
		go func() {
			defer func() { _ = host.Close() }()
			s.handle(ctx, host)
		}()
		if err := WriteLine(client, Header{DatabaseID: dbA, Admin: true}); err != nil {
			t.Fatal(err)
		}
		if err := WriteLine(client, Request{Op: OpCheckRestore, Target: later.Add(time.Minute).Format(time.RFC3339Nano), Major: 16, Latest: true}); err != nil {
			t.Fatal(err)
		}
		var reply Response
		if err := ReadLine(bufio.NewReader(client), &reply); err != nil {
			t.Fatal(err)
		}
		point := later.Add(-time.Microsecond)
		if got := reply.RestoreCheck; got == nil || got.Outcome != RestoreCovered || got.RecoveryPoint == nil || !got.RecoveryPoint.Equal(point) {
			t.Fatalf("latest primary archive evidence: %+v", got)
		}
		if got := s.checkRestore(ctx, dbA, point, 16); got.Outcome != RestoreCovered {
			t.Fatalf("latest point was not replayable: %+v", got)
		}
	})

	t.Run("recovery only", func(t *testing.T) {
		mirror := &memRemote{objects: make(map[string][]byte)}
		source := &Uploader{Store: store, Remote: mirror, Prefix: "recovery"}
		if _, err := source.RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
		offline := &TieredStore{LocalStore: newStore(t), Remote: downRemote{}, Prefix: "primary"}
		recovery := &TieredStore{LocalStore: newStore(t), Remote: mirror, Prefix: "recovery"}
		s := &Server{Store: offline, Backups: offline, Recovery: recovery}

		check := func(point time.Time, latest bool) RestoreCheck {
			t.Helper()
			host, client := net.Pipe()
			defer func() { _ = client.Close() }()
			go func() {
				defer func() { _ = host.Close() }()
				s.handle(ctx, host)
			}()
			if err := WriteLine(client, Header{DatabaseID: dbA, Admin: true}); err != nil {
				t.Fatal(err)
			}
			if err := WriteLine(client, Request{Op: OpCheckRecovery, Target: point.Format(time.RFC3339Nano), Major: 16, Latest: latest}); err != nil {
				t.Fatal(err)
			}
			var reply Response
			if err := ReadLine(bufio.NewReader(client), &reply); err != nil || reply.RestoreCheck == nil {
				t.Fatalf("regional check: %+v, %v", reply, err)
			}
			return *reply.RestoreCheck
		}
		destinationPoint := later.Add(-time.Microsecond)
		if got := check(later.Add(time.Minute), true); got.Outcome != RestoreCovered || got.RecoveryPoint == nil || !got.RecoveryPoint.Equal(destinationPoint) {
			t.Fatalf("latest verified point read from the mirror: %+v", got)
		}
		if got := check(destinationPoint, false); got.Outcome != RestoreCovered {
			t.Fatalf("latest advertised point was not covered: %+v", got)
		}
		host, client := net.Pipe()
		go func() {
			defer func() { _ = host.Close() }()
			s.handle(ctx, host)
		}()
		defer func() { _ = client.Close() }()
		if err := WriteLine(client, Header{DatabaseID: dbB, RestoreSource: RecoverySource(dbA)}); err != nil {
			t.Fatal(err)
		}
		if err := WriteLine(client, Request{Op: OpGetWAL, Name: restoreWALName(3)}); err != nil {
			t.Fatal(err)
		}
		br := bufio.NewReader(client)
		var walReply Response
		if err := ReadLine(br, &walReply); err != nil || walReply.Size != walSegmentBytes {
			t.Fatalf("recovery relay WAL: %+v, %v", walReply, err)
		}
		if _, err := io.CopyN(io.Discard, br, walReply.Size); err != nil {
			t.Fatal(err)
		}

		// The ordinary store still has segment 3. It must never complete
		// the recovery region's archive when that region misses a file.
		s.Store, s.Backups = store, store
		if err := mirror.Delete(ctx, WALKey("recovery", dbA, restoreWALName(3))); err != nil {
			t.Fatal(err)
		}
		if got := check(destinationPoint, false); got.Outcome != RestoreWALGap || got.MissingWAL != restoreWALName(3) {
			t.Fatalf("the ordinary archive concealed a regional gap: %+v", got)
		}
		if got := check(later.Add(time.Minute), true); got.RecoveryPoint == nil || !got.RecoveryPoint.Equal(target.Add(-time.Microsecond)) ||
			got.MissingWAL != restoreWALName(3) {
			t.Fatalf("latest recovery point advanced across a gap: %+v", got)
		}
	})

	t.Run("promotion", func(t *testing.T) {
		history := "00000002.history"
		child := timelineWALName(2, 4)
		drop := func(name string) {
			t.Helper()
			path, err := store.walPath(dbA, name)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		}
		putHistory := func(name, content string) {
			t.Helper()
			if err := store.PutWAL(ctx, dbA, name, strings.NewReader(content), int64(len(content))); err != nil {
				t.Fatal(err)
			}
		}
		putHistory(history, "1\t0/4000000\tstandby promoted\n")
		clear(wal)
		writeCheckPageHeader(wal, 4*walSegmentBytes, walLongHeader, 0)
		binary.LittleEndian.PutUint32(wal[4:8], 2)
		writeCheckRecord(wal[longHeader:], 0, 1, 3*walSegmentBytes+40, checkTransactionData(later))
		if err := store.PutWAL(ctx, dbA, child, bytes.NewReader(wal), walSegmentBytes); err != nil {
			t.Fatal(err)
		}
		if got := s.checkRestore(ctx, dbA, target, 16); got.Outcome != RestoreCovered || got.Timeline != 2 {
			t.Fatalf("verified promoted timeline: %+v", got)
		}
		putHistory("00000003.history", "1\t0/5000000\tcompeting branch\n")
		if got := s.checkRestore(ctx, dbA, target, 16); got.Outcome != RestoreUnverified || got.Reason != "timeline_unverified" {
			t.Fatalf("competing branches were treated as one lineage: %+v", got)
		}
		if got := s.inspectRestore(ctx, dbA, target, 16, false, 2); got.Outcome != RestoreCovered || got.Timeline != 2 {
			t.Fatalf("archived timeline changed after reservation: %+v", got)
		}
		drop("00000003.history")
		drop(history)
		if got := s.checkRestore(ctx, dbA, target, 16); got.Outcome != RestoreUnverified || got.MissingHistory != history {
			t.Fatalf("missing promotion history: %+v", got)
		}
		drop(child)
	})

	removeWAL := func(segment uint64) string {
		t.Helper()
		name := restoreWALName(segment)
		path, err := store.walPath(dbA, name)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		return name
	}
	missing := removeWAL(3)
	if got := s.checkRestore(ctx, dbA, target, 16); got.Outcome != RestoreWALGap || got.MissingWAL != missing {
		t.Fatalf("interior archive gap = %+v", got)
	}

	// The same segment can be supplied by the configured mirror.
	clear(wal)
	writeCheckPageHeader(wal, 3*walSegmentBytes, walLongHeader, 0)
	writeCheckRecord(wal[longHeader:], 0x40, 0, 2*walSegmentBytes+120, nil)
	primary := &memRemote{objects: make(map[string][]byte)}
	mirror := &memRemote{objects: map[string][]byte{WALKey("mirror", dbA, missing): wal}}
	tiered := &TieredStore{LocalStore: store, Remote: primary, Prefix: "primary", Offsite: mirror, OffsitePrefix: "mirror"}
	if got := (&Server{Store: tiered, Backups: tiered}).checkRestore(ctx, dbA, target, 16); got.Outcome != RestoreCovered {
		t.Fatalf("archive with mirror segment = %+v", got)
	}

	removeWAL(4)
	if got := s.checkRestore(ctx, dbA, target, 16); got.Outcome != RestoreUnverified || got.Reason != "target_not_reached" {
		t.Fatalf("archive tail = %+v", got)
	}
}

func TestRestoreCheckRequiresAdminArchiveStream(t *testing.T) {
	store := newStore(t)
	s := &Server{Store: store, Backups: store}
	for _, admin := range []bool{true, false} {
		client, host := net.Pipe()
		done := make(chan struct{})
		go func() {
			defer close(done)
			defer host.Close()
			s.handle(t.Context(), host)
		}()
		req := Request{Op: OpCheckRestore, Target: "2026-11-21T12:00:00Z", Major: 16}
		if err := WriteLine(client, Header{DatabaseID: dbA, Admin: admin}); err != nil {
			t.Fatal(err)
		}
		if err := WriteLine(client, req); err != nil {
			t.Fatal(err)
		}
		var resp Response
		if err := ReadLine(bufio.NewReader(client), &resp); err != nil {
			t.Fatal(err)
		}
		_ = client.Close()
		<-done
		if admin && (resp.RestoreCheck == nil || resp.RestoreCheck.Outcome != RestoreNoBaseBackup) {
			t.Fatalf("admin check = %+v", resp)
		}
		if !admin && (resp.Error != ErrCodeInvalid || resp.RestoreCheck != nil) {
			t.Fatalf("guest check = %+v", resp)
		}
	}
}

func restoreWALName(segment uint64) string { return timelineWALName(1, segment) }

func writeCheckPageHeader(page []byte, address uint64, flags uint16, remaining uint32) {
	binary.LittleEndian.PutUint16(page[:2], walPageMagic(16))
	binary.LittleEndian.PutUint16(page[2:4], flags)
	binary.LittleEndian.PutUint32(page[4:8], 1)
	binary.LittleEndian.PutUint64(page[8:16], address)
	binary.LittleEndian.PutUint32(page[16:20], remaining)
	if flags&walLongHeader != 0 {
		binary.LittleEndian.PutUint64(page[24:32], 42)
		binary.LittleEndian.PutUint32(page[32:36], walSegmentBytes)
		binary.LittleEndian.PutUint32(page[36:40], walPageBytes)
	}
}

func writeCheckRecord(dst []byte, info, manager byte, previous uint64, data []byte) int {
	length := walRecordHeader + len(data)
	binary.LittleEndian.PutUint32(dst[:4], uint32(length))
	binary.LittleEndian.PutUint64(dst[8:16], previous)
	dst[16], dst[17] = info, manager
	copy(dst[walRecordHeader:], data)
	crc := crc32.Update(crc32.Checksum(data, walCRC), walCRC, dst[:20])
	binary.LittleEndian.PutUint32(dst[20:24], crc)
	return (length + 7) &^ 7
}

func checkTransactionData(at time.Time) []byte {
	data := []byte{255, 8, 0, 0, 0, 0, 0, 0, 0, 0}
	binary.LittleEndian.PutUint64(data[2:], uint64(at.UnixMicro()-946684800000000))
	return data
}

func TestRestoreCheckWithPostgresWAL(t *testing.T) {
	fixture := os.Getenv("FAAS_RESTORE_CHECK_FIXTURE")
	if fixture == "" {
		t.Skip("set FAAS_RESTORE_CHECK_FIXTURE to a PostgreSQL 16 archive fixture")
	}
	read := func(name string) string {
		t.Helper()
		body, err := os.ReadFile(filepath.Join(fixture, name))
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(body))
	}
	target, err := time.Parse(time.RFC3339Nano, read("point.txt"))
	if err != nil {
		t.Fatal(err)
	}
	store := newStore(t)
	if err := withRestoreFixtureFile(filepath.Join(fixture, "base.tar"), func(r io.Reader, _ int64) error {
		_, err := store.PutBackup(t.Context(), dbA, r, target)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(fixture, "archive"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if name := entry.Name(); ValidWALName(name) {
			if err := withRestoreFixtureFile(filepath.Join(fixture, "archive", name), func(r io.Reader, size int64) error {
				return store.PutWAL(t.Context(), dbA, name, r, size)
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
	server := &Server{Store: store, Backups: store}
	check := server.checkRestore(t.Context(), dbA, target, 16)
	if check.Outcome != RestoreCovered {
		t.Fatalf("actual PostgreSQL WAL check = %+v", check)
	}
	latest := server.inspectRestore(t.Context(), dbA, target.Add(time.Minute), 16, true, 0)
	if latest.Outcome != RestoreCovered || latest.RecoveryPoint == nil || latest.RecoveryPoint.Before(target) {
		t.Fatalf("latest point in actual PostgreSQL WAL = %+v", latest)
	}
	if verified := server.checkRestore(t.Context(), dbA, *latest.RecoveryPoint, 16); verified.Outcome != RestoreCovered {
		t.Fatalf("latest point not covered in actual PostgreSQL WAL: %+v", verified)
	}
}

func withRestoreFixtureFile(path string, read func(io.Reader, int64) error) error {
	f, err := os.Open(path) //nolint:forbidigo // Read a local PostgreSQL fixture supplied to the test.
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	return read(f, info.Size())
}

func TestRestoreCheckWithPromotedPostgres(t *testing.T) {
	fixture := os.Getenv("FAAS_RESTORE_TIMELINE_FIXTURE")
	if fixture == "" {
		t.Skip("set FAAS_RESTORE_TIMELINE_FIXTURE to a PostgreSQL 16 archive from standby promotion")
	}
	read := func(name string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(fixture, name))
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(b))
	}
	targets := make(map[string]time.Time)
	for _, name := range []string{"before", "after"} {
		point, err := time.Parse(time.RFC3339Nano, read(name+".txt"))
		if err != nil {
			t.Fatal(err)
		}
		targets[name] = point
	}
	ctx := t.Context()
	store := newStore(t)
	err := withRestoreFixtureFile(filepath.Join(fixture, "base", "base.tar"), func(r io.Reader, _ int64) error {
		_, err := store.PutBackup(ctx, dbA, r, targets["before"])
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(fixture, "archive"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if !ValidWALName(name) {
			continue
		}
		if err := withRestoreFixtureFile(filepath.Join(fixture, "archive", name), func(r io.Reader, size int64) error {
			return store.PutWAL(ctx, dbA, name, r, size)
		}); err != nil {
			t.Fatal(err)
		}
	}
	server := &Server{Store: store, Backups: store}
	for name, target := range targets {
		got := server.checkRestore(ctx, dbA, target, 16)
		if got.Outcome != RestoreCovered || got.Timeline != 2 || got.WALStopTime == nil || !got.WALStopTime.After(target) {
			t.Fatalf("%s promotion check at %v: %+v", name, target, got)
		}
	}
	latest := server.inspectRestore(ctx, dbA, targets["after"].Add(time.Minute), 16, true, 0)
	if latest.Outcome != RestoreCovered || latest.Timeline != 2 || latest.RecoveryPoint == nil || !latest.RecoveryPoint.After(targets["after"]) {
		t.Fatalf("latest recovery point after promotion: %+v", latest)
	}

	moveAside := func(name string) func() {
		t.Helper()
		path, err := store.walPath(dbA, name)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(path, path+".excluded"); err != nil {
			t.Fatal(err)
		}
		return func() {
			if err := os.Rename(path+".excluded", path); err != nil {
				t.Fatal(err)
			}
		}
	}
	segment := read("child-segment.txt")
	restoreSegment := moveAside(segment)
	if gap := server.checkRestore(ctx, dbA, targets["after"], 16); gap.Outcome != RestoreWALGap || gap.MissingWAL != segment {
		t.Fatalf("removed child segment: %+v", gap)
	}
	restoreSegment()
	restoreHistory := moveAside("00000002.history")
	defer restoreHistory()
	if gap := server.checkRestore(ctx, dbA, targets["after"], 16); gap.Outcome != RestoreUnverified || gap.MissingHistory != "00000002.history" {
		t.Fatalf("removed history: %+v", gap)
	}
}
