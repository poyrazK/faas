// adr: 1027
package dbarchive

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	dbA = "0b7d3c5e-8f43-4c7a-9d0e-5a1f2b3c4d5e"
	dbB = "1b7d3c5e-8f43-4c7a-9d0e-5a1f2b3c4d5e"
	seg = "000000010000000000000001"
)

// serve starts a Server on a unix socket and returns a dialer that writes
// the relay header the way vmmd does.
func serve(t *testing.T, store Store) func(Header) *Client {
	t.Helper()
	short, err := os.MkdirTemp("", "dba") // unix socket paths are short on macOS
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(short) })
	sock := filepath.Join(short, "a.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = (&Server{Store: store}).Serve(ctx, ln); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return func(h Header) *Client {
		conn, err := net.Dial("unix", sock)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
		if err := WriteLine(conn, h); err != nil {
			t.Fatal(err)
		}
		return NewClient(conn)
	}
}

func newStore(t *testing.T) *LocalStore {
	t.Helper()
	s, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestArchiveRoundTripAndIdempotence(t *testing.T) {
	dial := serve(t, newStore(t))
	c := dial(Header{DatabaseID: dbA})
	body := bytes.Repeat([]byte("wal"), 1000)
	for range 2 { // PostgreSQL re-archives after a crash: identical is success
		if err := c.PutWAL(seg, bytes.NewReader(body), int64(len(body))); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.PutWAL(seg, bytes.NewReader([]byte("other")), 5); !errors.Is(err, ErrConflict) {
		t.Fatalf("different content = %v, want ErrConflict", err)
	}
	// The stream survives a conflict; later requests still work.
	var got bytes.Buffer
	if err := c.GetWAL(seg, &got); err != nil || !bytes.Equal(got.Bytes(), body) {
		t.Fatalf("get = %d bytes, %v", got.Len(), err)
	}
	if err := c.GetWAL("000000010000000000000002", &got); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing segment = %v, want ErrNotFound", err)
	}
	for _, name := range []string{"00000002.history", seg + ".partial", seg + ".00000028.backup"} {
		if err := c.PutWAL(name, strings.NewReader("x"), 1); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// Isolation: a database reads only its own archive, or the restore source
// the relay names; it never chooses one.
func TestArchiveIsolationFollowsTheHeader(t *testing.T) {
	dial := serve(t, newStore(t))
	if err := dial(Header{DatabaseID: dbA}).PutWAL(seg, strings.NewReader("a-wal"), 5); err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if err := dial(Header{DatabaseID: dbB}).GetWAL(seg, &got); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another database read A's WAL: %v %q", err, got.String())
	}
	if err := dial(Header{DatabaseID: dbB, RestoreSource: dbA}).GetWAL(seg, &got); err != nil || got.String() != "a-wal" {
		t.Fatalf("restore from A = %q, %v", got.String(), err)
	}
}

func TestArchiveRejectsUnsafeInput(t *testing.T) {
	store := newStore(t)
	for _, name := range []string{"../../etc/passwd", "00000001000000000000000", "000000010000000000000001/x", "", "000000010000000000000001.tmp"} {
		if ValidWALName(name) {
			t.Errorf("%q accepted as a WAL name", name)
		}
		if err := store.PutWAL(context.Background(), dbA, name, strings.NewReader("x"), 1); err == nil {
			t.Errorf("store accepted %q", name)
		}
	}
	if err := store.PutWAL(context.Background(), "../"+dbA, seg, strings.NewReader("x"), 1); err == nil {
		t.Error("store accepted a path as database id")
	}
	if err := store.PutWAL(context.Background(), dbA, seg, strings.NewReader("short"), 10); err == nil {
		t.Error("a truncated upload was stored")
	}
	if _, _, err := store.OpenWAL(context.Background(), dbA, seg); !errors.Is(err, ErrNotFound) {
		t.Fatalf("truncated upload left a file: %v", err)
	}
	for _, h := range []Header{{}, {DatabaseID: "x"}, {DatabaseID: dbA, RestoreSource: "../b"}} {
		if h.Validate() == nil {
			t.Errorf("header %+v accepted", h)
		}
	}
}
