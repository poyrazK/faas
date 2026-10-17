package objectnode

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestOnlineCompactionKilledProcessesResume(t *testing.T) {
	if boundary := os.Getenv("GREGALE_ONLINE_COMPACTION_BOUNDARY"); boundary != "" {
		store, err := Open(os.Getenv("GREGALE_ONLINE_COMPACTION_DIRECTORY"))
		if err != nil {
			t.Fatal(err)
		}
		store.compactionCheckpoint = func(event string) error {
			if event == boundary {
				_, _ = os.Stdout.WriteString("online compaction boundary\n")
				select {}
			}
			return nil
		}
		for range 1000 {
			if _, err = store.CompactStep(context.Background(), onlineOptions()); err != nil {
				t.Fatal(err)
			}
		}
		t.Fatal("child missed boundary", boundary)
	}
	for _, boundary := range []string{"before checkpoint", "after checkpoint", "pruned record", "cleanup synced", "completed", "pruned blob"} {
		t.Run(boundary, func(t *testing.T) {
			primary, _ := replicationPair(t)
			id := uuid.NewString()
			if _, err := receiptPut(t, primary, "key", "acknowledged", id, PutOptions{}); err != nil {
				t.Fatal(err)
			}
			putText(t, primary, "key", "survives")
			_ = primary.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestOnlineCompactionKilledProcessesResume$")
			command.Env = append(os.Environ(), "GREGALE_ONLINE_COMPACTION_BOUNDARY="+boundary, "GREGALE_ONLINE_COMPACTION_DIRECTORY="+primary.root)
			output, err := command.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			command.Stderr = os.Stderr
			if err = command.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = command.Process.Kill(); _ = command.Wait() }()
			if scanner := bufio.NewScanner(output); !scanner.Scan() || scanner.Text() != "online compaction boundary" {
				t.Fatal("child did not reach boundary", boundary)
			}
			if err = command.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			_ = command.Wait()
			primary = reopenCompactionPrimary(t, primary.root)
			if readText(t, primary, "key") != "survives" {
				t.Fatal("live bytes lost")
			}
			proof, err := primary.Receipt("test-bucket", "key", id)
			if err != nil || proof.Status != ReceiptCommitted {
				t.Fatal("historical proof lost", proof, err)
			}
			drainOnline(t, primary, onlineOptions())
			if primary.journalUsage.Records != 0 || primary.journalUsage.Blobs != 0 {
				t.Fatal("retained cleanup did not finish", primary.journalUsage)
			}
			putText(t, primary, "new", "after restart")
			_ = primary.Close()
			backup := filepath.Join(t.TempDir(), "backup")
			if _, err = Backup(t.Context(), primary.root, backup); err != nil {
				t.Fatal(err)
			}
			if _, err = VerifyBackup(t.Context(), backup); err != nil {
				t.Fatal(err)
			}
		})
	}
}
