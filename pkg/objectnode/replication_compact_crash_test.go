package objectnode

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestReplicationCompactionKilledProcessesResume(t *testing.T) {
	if boundary := os.Getenv("GREGALE_COMPACTION_CRASH_BOUNDARY"); boundary != "" {
		_, err := compactJournal(context.Background(), os.Getenv("GREGALE_COMPACTION_CRASH_DIRECTORY"), -1, func(stage string) error {
			if stage == boundary {
				_, _ = os.Stdout.WriteString("compaction checkpoint reached\n")
				select {}
			}
			return nil
		})
		t.Fatal("child missed compaction boundary", boundary, err)
	}
	for _, boundary := range []string{"before checkpoint", "after checkpoint", "pruned record", "pruned blob", "cleanup synced", "completed"} {
		t.Run(boundary, func(t *testing.T) {
			primary, _ := replicationPair(t)
			putText(t, primary, "key", "obsolete")
			putText(t, primary, "key", "survives")
			_ = primary.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestReplicationCompactionKilledProcessesResume$")
			command.Env = append(os.Environ(), "GREGALE_COMPACTION_CRASH_BOUNDARY="+boundary, "GREGALE_COMPACTION_CRASH_DIRECTORY="+primary.root)
			output, err := command.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			command.Stderr = os.Stderr
			if err = command.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = command.Process.Kill(); _ = command.Wait() }()
			if scanner := bufio.NewScanner(output); !scanner.Scan() || scanner.Text() != "compaction checkpoint reached" {
				t.Fatal("child did not reach durable boundary")
			}
			if err = command.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			_ = command.Wait()
			primary = reopenCompactionPrimary(t, primary.root)
			if readText(t, primary, "key") != "survives" || readText(t, primary, "baseline") != "seed" {
				t.Fatal("process death lost live bytes")
			}
			status, err := primary.ReplicationStatus()
			if err != nil || status.AppliedSequence != 2 || boundary != "before checkpoint" && (status.BaseSequence != 2 || status.Checkpoint.Pruning || primary.journalUsage.Records != 0 || primary.journalUsage.Blobs != 0) {
				t.Fatal("cleanup did not resume", status, primary.journalUsage, err)
			}
			putText(t, primary, "key", "after restart")
			_ = primary.Close()
			backup := filepath.Join(t.TempDir(), "backup")
			if _, err = Backup(t.Context(), primary.root, backup); err != nil {
				t.Fatal("resumed cleanup could not be backed up", err)
			}
			if _, err = VerifyBackup(t.Context(), backup); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestReplicationCompactionRejectsMissingRetainedHistoryBeforeCleanup(t *testing.T) {
	primary, _ := replicationPair(t)
	putText(t, primary, "first", "first")
	putText(t, primary, "second", "second")
	_ = primary.Close()
	injected := errors.New("stop after floor publication")
	if _, err := compactJournal(t.Context(), primary.root, 1, func(stage string) error {
		if stage == "after checkpoint" {
			return injected
		}
		return nil
	}); !errors.Is(err, injected) {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(primary.root, "replication/log", journalName(2))); err != nil {
		t.Fatal(err)
	}
	if reopened, err := Open(primary.root); !errors.Is(err, ErrCorrupt) {
		if reopened != nil {
			_ = reopened.Close()
		}
		t.Fatal("missing retained record was silently skipped", err)
	}
	if _, err := os.Stat(filepath.Join(primary.root, "replication/log", journalName(1))); err != nil {
		t.Fatal("cleanup proceeded before checking the retained chain", err)
	}
}

func TestReplicationCompactionRefusesCorruptOrAliasedCurrentBytes(t *testing.T) {
	for _, failure := range []string{"digest", "symlink", "hardlink"} {
		t.Run(failure, func(t *testing.T) {
			primary, _ := replicationPair(t)
			o := putText(t, primary, "key", "payload")
			_ = primary.Close()
			path := filepath.Join(primary.objectPath("test-bucket", "key"), o.ID+".data")
			outside := filepath.Join(t.TempDir(), "outside")
			var err error
			switch failure {
			case "digest":
				err = os.WriteFile(path, []byte("damaged"), 0o600)
			case "symlink":
				if err = os.Rename(path, outside); err == nil {
					err = os.Symlink(outside, path)
				}
			case "hardlink":
				err = os.Link(path, outside)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = CompactJournal(t.Context(), primary.root, -1); err == nil {
				t.Fatal("invalid live bytes were compacted", failure)
			}
			status, err := ReadReplicationStatus(primary.root)
			if err != nil || status.BaseSequence != 0 || status.Checkpoint != nil {
				t.Fatal("failed preflight published a new floor", status, err)
			}
			if _, err = os.Stat(filepath.Join(primary.root, "replication/log", journalName(1))); err != nil {
				t.Fatal("failed preflight erased history", err)
			}
		})
	}
}
