package objectnode

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestReplayPreparationKilledBoundaries(t *testing.T) {
	if boundary := os.Getenv("GREGALE_REPLAY_PREPARE_CRASH"); boundary != "" {
		primary, err := Open(os.Getenv("GREGALE_REPLAY_PREPARE_DIRECTORY"))
		if err != nil {
			t.Fatal(err)
		}
		stop := func() { _, _ = os.Stdout.WriteString("replay preparation checkpoint reached\n"); select {} }
		if boundary == "prepared" {
			primary.replayCopy = func(ctx context.Context, source, destination string, size int64, buffer []byte) (string, error) {
				digest, err := copyBackupFile(ctx, source, destination, size, buffer)
				if err == nil {
					stop()
				}
				return digest, err
			}
		} else {
			primary.replicationPersist = func(path string, value any) (bool, error) {
				_, journal := value.(JournalEntry)
				if journal && boundary == "installed" {
					stop()
				}
				published, err := writeReplicationJSON(path, value)
				if journal && err == nil {
					stop()
				}
				return published, err
			}
		}
		_, err = primary.Put(context.Background(), "test-bucket", "key", strings.NewReader("new"), 3, PutOptions{Receipt: os.Getenv("GREGALE_REPLAY_PREPARE_RECEIPT")})
		t.Fatal("child missed replay boundary", err)
	}
	for _, boundary := range []string{"prepared", "installed", "journal"} {
		t.Run(boundary, func(t *testing.T) {
			primary, follower := replicationPair(t)
			putText(t, primary, "key", "old")
			if err := primary.Close(); err != nil {
				t.Fatal(err)
			}
			id := uuid.NewString()
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestReplayPreparationKilledBoundaries$")
			command.Env = append(os.Environ(), "GREGALE_REPLAY_PREPARE_CRASH="+boundary, "GREGALE_REPLAY_PREPARE_DIRECTORY="+primary.root, "GREGALE_REPLAY_PREPARE_RECEIPT="+id)
			output, err := command.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			command.Stderr = os.Stderr
			if err = command.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = command.Process.Kill(); _ = command.Wait() }()
			if scanner := bufio.NewScanner(output); !scanner.Scan() || scanner.Text() != "replay preparation checkpoint reached" {
				t.Fatal("child missed replay checkpoint")
			}
			if err = command.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			_ = command.Wait()
			reopened := reopenCompactionPrimary(t, primary.root)
			want := "old"
			if boundary == "journal" {
				want = "new"
			}
			if actual := readText(t, reopened, "key"); actual != want {
				t.Fatal("preparation changed the commit boundary", actual, want)
			}
			proof, err := reopened.Receipt("test-bucket", "key", id)
			if boundary == "journal" {
				if err != nil || proof.Status != ReceiptCommitted {
					t.Fatal(proof, err)
				}
			} else if !errors.Is(err, ErrNoReceipt) {
				t.Fatal("uncommitted preparation fabricated a proof", proof, err)
			}
			catchUpTestReplica(t, reopened, follower)
		})
	}
}
