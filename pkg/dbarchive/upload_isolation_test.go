// adr: 1027
package dbarchive

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

type failingDatabaseRemote struct {
	*memRemote
	failKey  string
	attempts []string
}

func (r *failingDatabaseRemote) Put(ctx context.Context, key string, body io.ReadSeeker, size int64) error {
	r.attempts = append(r.attempts, key)
	if key == r.failKey {
		return errBucketGone
	}
	return r.memRemote.Put(ctx, key, body, size)
}
func TestUploaderContinuesOtherDatabasesAfterAnUploadFailure(t *testing.T) {
	local := newStore(t)
	backup := putBackup(t, local, dbA, 2, t0, true)
	putFile(t, local, dbB, segName(3), "other-database-wal")
	remote := &failingDatabaseRemote{memRemote: &memRemote{objects: map[string][]byte{}}, failKey: BaseKey("native", dbA, backup.ID+".tar.zst")}
	pending := 0
	uploader := &Uploader{Store: local, Remote: remote, Prefix: "native", OnPending: func(n int, _ time.Duration) { pending = n }}
	for pass := 1; pass <= 2; pass++ {
		if _, err := uploader.RunOnce(t.Context()); !errors.Is(err, errBucketGone) {
			t.Fatalf("pass %d: %v", pass, err)
		}
		if !remote.has(WALKey("native", dbB, segName(3))) {
			t.Fatal("other database's WAL was blocked")
		}
		if remote.has(BaseKey("native", dbA, backup.ID+".json")) || pending != 2 {
			t.Fatalf("manifest preceded backup data, or incorrect pending count: %d", pending)
		}
	}
	remote.failKey = ""
	if n, err := uploader.RunOnce(t.Context()); err != nil || n != 2 || pending != 0 || !remote.has(BaseKey("native", dbA, backup.ID+".json")) {
		t.Fatalf("recovery upload=%d pending=%d err=%v", n, pending, err)
	}
	if got := strings.Join(remote.attempts, " "); strings.Count(got, WALKey("native", dbB, segName(3))) != 1 {
		t.Fatal("unrelated successful WAL was uploaded more than once")
	}
}
