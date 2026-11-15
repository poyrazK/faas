package fcvm

// adr: 20261110210000

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"filippo.io/age"
)

type countingCaptureOpener struct {
	opens int
	err   error
	id    *age.X25519Identity
}

func (c *countingCaptureOpener) OpenCaptureKey(context.Context, string, string) (*age.X25519Identity, error) {
	c.opens++
	return c.id, c.err
}

// Slice 3d-3: a sealed park/warm snapshot's key is opened through secretd
// once per capture; crash captures and failed opens are never cached.
func TestOpenRemoteCaptureKey_CachesSealedSnapshots(t *testing.T) {
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	m := &Manager{}
	opener := &countingCaptureOpener{id: id}
	snap := &Snapshot{CaptureID: "c1", SealedKey: []byte("k"), SealedCache: true}
	for range 3 {
		got, err := m.openRemoteCaptureKey(context.Background(), opener, "app", snap)
		if err != nil || got != id {
			t.Fatalf("open = %v, %v", got, err)
		}
	}
	if opener.opens != 1 {
		t.Fatalf("secretd opens = %d, want 1", opener.opens)
	}
	// Another app naming the same capture is not served from the cache.
	if _, err := m.openRemoteCaptureKey(context.Background(), opener, "other-app", snap); err != nil || opener.opens != 2 {
		t.Fatalf("other app: opens=%d err=%v, want a fresh secretd open", opener.opens, err)
	}
	crash := &Snapshot{CaptureID: "c2", SealedKey: []byte("k")}
	for range 2 {
		if _, err := m.openRemoteCaptureKey(context.Background(), opener, "app", crash); err != nil {
			t.Fatal(err)
		}
	}
	if opener.opens != 4 {
		t.Fatalf("crash capture opens = %d, want every open to reach secretd", opener.opens-2)
	}
	failing := &countingCaptureOpener{err: errors.New("refused")}
	fresh := &Snapshot{CaptureID: "c3", SealedKey: []byte("k"), SealedCache: true}
	for range 2 {
		if _, err := m.openRemoteCaptureKey(context.Background(), failing, "app", fresh); err == nil {
			t.Fatal("a refused open succeeded")
		}
	}
	if failing.opens != 2 {
		t.Fatalf("refused opens = %d, want no cached refusal", failing.opens)
	}
}

func TestCaptureKeyCache_ExpiresAndBounds(t *testing.T) {
	id, _ := age.GenerateX25519Identity()
	var c captureKeyCache
	now := time.Now()
	c.put(captureKeyRef{"a", "1"}, id, now)
	if _, ok := c.get(captureKeyRef{"a", "1"}, now.Add(captureKeyCacheTTL)); ok {
		t.Fatal("an expired key was served")
	}
	for i := range captureKeyCacheMax + 10 {
		c.put(captureKeyRef{"a", strconv.Itoa(i)}, id, now.Add(time.Duration(i)*time.Millisecond))
	}
	if len(c.entries) > captureKeyCacheMax {
		t.Fatalf("cache holds %d keys, want at most %d", len(c.entries), captureKeyCacheMax)
	}
}

// A sealed snapshot's backing is checked when it restores, never by a probe
// that would spend a secretd open.
func TestCheckSnapshotBackingCurrent_SealedIsUnknown(t *testing.T) {
	m := &Manager{}
	verdict, err := m.CheckSnapshotBackingCurrent(context.Background(), &Snapshot{StorageKey: "snap/d/captures/c/v2/mem", SealedKey: []byte("k"), SealedCache: true}, "base/x")
	if verdict != SnapshotBackingUnknown || err == nil {
		t.Fatalf("verdict = %v, %v; want unknown", verdict, err)
	}
}
