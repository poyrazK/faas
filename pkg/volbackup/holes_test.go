package volbackup_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/volbackup"
)

// A first backup given the volume's data ranges reads only the chunks they
// touch; the rest are holes and back up as zeros.
func TestFirstBackupSkipsHoles(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	vol := filepath.Join(t.TempDir(), "vol.ext4")
	if err := os.WriteFile(vol, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(vol, 8*volbackup.ChunkBytes); err != nil {
		t.Fatal(err)
	}
	writeAt(t, vol, 5*volbackup.ChunkBytes+3, []byte("data"))
	data := []storage.Range{{Start: 5*volbackup.ChunkBytes + 3, End: 5*volbackup.ChunkBytes + 7}}
	m, st, err := volbackup.Backup(ctx, store, account, sandbox, "b1", vol, nil, data)
	if err != nil {
		t.Fatal(err)
	}
	if st.ChunksRead != 1 || m.Chunks[5] == "" || len(m.ChunkSet()) != 1 {
		t.Fatalf("first backup read %d chunks: %v", st.ChunksRead, m.Chunks)
	}
}
