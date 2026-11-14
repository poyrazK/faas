// adr: 1027
package dbarchive

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestConcurrentStreamsRespectArchiveQuota(t *testing.T) {
	local := newStore(t)
	store := &TieredStore{LocalStore: local}
	server := &Server{Store: store, Backups: store, Retention: store}
	socket := serveSocket(t, server)
	const quota = 1000
	var clients []net.Conn
	for i := 1; i <= 4; i++ {
		conn, err := net.Dial("unix", socket)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		if err := WriteLine(conn, Header{DatabaseID: dbA, QuotaBytes: quota}); err != nil {
			t.Fatal(err)
		}
		if err := WriteLine(conn, Request{Op: OpPutWAL, Name: segName(i), Size: 800}); err != nil {
			t.Fatal(err)
		}
		clients = append(clients, conn)
	}
	// A temporary WAL file means its request passed the quota check and is
	// waiting for payload. Leave all streams in flight before supplying it.
	inFlight := 0
	for until := time.Now().Add(400 * time.Millisecond); time.Now().Before(until); {
		entries, _ := os.ReadDir(filepath.Join(local.dir, dbA, "wal"))
		inFlight = len(entries)
		if inFlight >= 4 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	for _, conn := range clients {
		_, _ = conn.Write([]byte(strings.Repeat("w", 800)))
	}
	accepted := 0
	for _, conn := range clients {
		var response Response
		if err := ReadLine(bufio.NewReader(conn), &response); err != nil {
			t.Fatal(err)
		}
		if response.Error == "" {
			accepted++
		} else if response.Error != ErrCodeQuota {
			t.Fatalf("unexpected response %+v", response)
		}
	}
	size, err := local.LocalBytes(dbA)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("quota=%d in_flight=%d accepted=%d stored=%d", quota, inFlight, accepted, size)
	if size > quota {
		t.Fatal("concurrent streams exceeded the per-database archive disk cap")
	}
}

func quotaWAL(t *testing.T, socket, db, name string, size, quota int64) net.Conn {
	t.Helper()
	conn, err := net.Dial("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if err := WriteLine(conn, Header{DatabaseID: db, QuotaBytes: quota}); err != nil {
		t.Fatal(err)
	}
	if err := WriteLine(conn, Request{Op: OpPutWAL, Name: name, Size: size}); err != nil {
		t.Fatal(err)
	}
	return conn
}
func waitQuota(t *testing.T, s *Server, db string, want int64) {
	t.Helper()
	for until := time.Now().Add(2 * time.Second); ; {
		s.quotaMu.Lock()
		state := s.quotas[db]
		s.quotaMu.Unlock()
		var got int64
		if state != nil {
			state.mu.Lock()
			got = state.pending
			state.mu.Unlock()
		}
		if got == want && (want != 0 || state == nil) {
			return
		}
		if time.Now().After(until) {
			t.Fatalf("pending quota=%d want %d", got, want)
		}
		time.Sleep(time.Millisecond)
	}
}
func TestCancelledWALReleasesItsQuota(t *testing.T) {
	local := newStore(t)
	store := &TieredStore{LocalStore: local}
	server := &Server{Store: store, Backups: store, Retention: store}
	socket := serveSocket(t, server)
	first := quotaWAL(t, socket, dbA, segName(1), 800, 1000)
	waitQuota(t, server, dbA, 800)
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	waitQuota(t, server, dbA, 0)
	next := quotaWAL(t, socket, dbA, segName(2), 800, 1000)
	if _, err := next.Write([]byte(strings.Repeat("w", 800))); err != nil {
		t.Fatal(err)
	}
	var response Response
	if err := ReadLine(bufio.NewReader(next), &response); err != nil || response.Error != "" {
		t.Fatalf("retry after aborted WAL: %+v %v", response, err)
	}
	waitQuota(t, server, dbA, 0)
	server.quotaMu.Lock()
	defer server.quotaMu.Unlock()
	if len(server.quotas) != 0 {
		t.Fatal("completed requests retained quota tracking entries")
	}
}
func TestInFlightQuotaRejectsExcessWithoutBlockingOtherDatabases(t *testing.T) {
	local := newStore(t)
	store := &TieredStore{LocalStore: local}
	server := &Server{Store: store, Backups: store, Retention: store}
	socket := serveSocket(t, server)
	first := quotaWAL(t, socket, dbA, segName(1), 800, 1000)
	waitQuota(t, server, dbA, 800)
	refused := quotaWAL(t, socket, dbA, segName(2), 800, 1000)
	var response Response
	if err := ReadLine(bufio.NewReader(refused), &response); err != nil || response.Error != ErrCodeQuota {
		t.Fatalf("concurrent quota rejection: %+v %v", response, err)
	}
	other := quotaWAL(t, socket, dbB, segName(1), 800, 1000)
	if _, err := other.Write([]byte(strings.Repeat("w", 800))); err != nil {
		t.Fatal(err)
	}
	response = Response{}
	if err := ReadLine(bufio.NewReader(other), &response); err != nil || response.Error != "" {
		t.Fatalf("other database blocked: %+v %v", response, err)
	}
	if _, err := first.Write([]byte(strings.Repeat("w", 800))); err != nil {
		t.Fatal(err)
	}
	if err := ReadLine(bufio.NewReader(first), &response); err != nil || response.Error != "" {
		t.Fatalf("reserved WAL rejected: %+v %v", response, err)
	}
}

type backupEndGate struct {
	io.Reader
	started, release chan struct{}
	once             sync.Once
}

func (g *backupEndGate) Read(p []byte) (int, error) {
	n, err := g.Reader.Read(p)
	if errors.Is(err, io.EOF) {
		g.once.Do(func() { close(g.started) })
		<-g.release
	}
	return n, err
}

func TestWALCanArchiveWhileBaseBackupWaitsForItsEnd(t *testing.T) {
	local := newStore(t)
	store := &TieredStore{LocalStore: local}
	server := &Server{Store: store, Backups: store, Retention: store, now: func() time.Time { return t0 }}
	open := serveServer(t, server)
	body := baseTar(t, "0/2000028", segName(2), t0, strings.Repeat("x", 300_000))
	gate := &backupEndGate{Reader: bytes.NewReader(body), started: make(chan struct{}), release: make(chan struct{})}
	var release sync.Once
	defer release.Do(func() { close(gate.release) })
	done := make(chan error, 1)
	backup := open(Header{DatabaseID: dbA, QuotaBytes: 1 << 20})
	go func() { _, err := backup.PutBackup(gate); done <- err }()
	select {
	case <-gate.started:
	case <-time.After(2 * time.Second):
		t.Fatal("backup did not begin")
	}
	wal := open(Header{DatabaseID: dbA, QuotaBytes: 1 << 20})
	if err := wal.PutWAL(segName(3), strings.NewReader("wal"), 3); err != nil {
		t.Fatal("WAL blocked behind backup", err)
	}
	release.Do(func() { close(gate.release) })
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("backup failed to finish")
	}
	if size, err := local.LocalBytes(dbA); err != nil || size > 1<<20 {
		t.Fatalf("archive size=%d err=%v", size, err)
	}
}
func TestFailedBackupReleasesItsQuota(t *testing.T) {
	local := newStore(t)
	store := &TieredStore{LocalStore: local}
	server := &Server{Store: store, Backups: store, Retention: store}
	open := serveServer(t, server)
	if _, err := open(Header{DatabaseID: dbA, QuotaBytes: 100_000}).PutBackup(strings.NewReader(strings.Repeat("bad", 1000))); err == nil {
		t.Fatal("invalid backup succeeded")
	}
	waitQuota(t, server, dbA, 0)
	if err := open(Header{DatabaseID: dbA, QuotaBytes: 1000}).PutWAL(segName(2), strings.NewReader(strings.Repeat("w", 800)), 800); err != nil {
		t.Fatal("failed backup retained quota", err)
	}
}

func TestCompressedBackupQuotaIncludesItsManifest(t *testing.T) {
	ctx := t.Context()
	body := baseTar(t, "0/2000028", segName(2), t0, "rows")
	reference := newStore(t)
	manifest, err := reference.PutBackup(ctx, dbA, bytes.NewReader(body), t0)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(reference.dir, dbA, "base", manifest.ID+".tar.zst"))
	if err != nil {
		t.Fatal(err)
	}
	local := newStore(t)
	store := &TieredStore{LocalStore: local}
	server := &Server{Store: store, Backups: store, Retention: store}
	quota := server.beginQuota(Header{DatabaseID: dbA, QuotaBytes: info.Size()})
	defer quota.release()
	_, err = local.putBackup(ctx, dbA, bytes.NewReader(body), t0, quota)
	if !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("manifest exceeded quota without rejection: %v", err)
	}
	quota.release()
	if size, err := local.LocalBytes(dbA); err != nil || size != 0 {
		t.Fatalf("refused backup left %d bytes: %v", size, err)
	}
}
