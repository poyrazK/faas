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

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectrecovery"
)

func TestRecoveryCleanupKilledProcessesResumeIndexAndPairedRetirement(t *testing.T) {
	if boundary := os.Getenv("GREGALE_INDEXED_RECOVERY_BOUNDARY"); boundary != "" {
		s, err := Open(os.Getenv("GREGALE_INDEXED_RECOVERY_DIRECTORY"))
		if err != nil {
			t.Fatal(err)
		}
		now, err := time.Parse(time.RFC3339Nano, os.Getenv("GREGALE_INDEXED_RECOVERY_CLOCK"))
		if err != nil {
			t.Fatal(err)
		}
		s.recoveryClock = func() time.Time { return now }
		checkpoint := func(phase string) error {
			if phase == boundary {
				_, _ = os.Stdout.WriteString("indexed recovery boundary\n")
				select {}
			}
			return nil
		}
		s.cleanupCheckpoint, s.recoveryCheckpoint = checkpoint, checkpoint
		_, err = s.RecoveryCleanupStep(context.Background(), cleanupTestOptions(1))
		t.Fatal("child missed boundary", boundary, err)
	}
	for _, boundary := range []string{"barrier synced", "receipt removed", "session removed", "index applied", "identity finished"} {
		t.Run(boundary, func(t *testing.T) {
			s, now := recoveryTestStore(t)
			u := prepareMultipartRetirement(t, s, "committed")
			putText(t, s, "survives", "current bytes")
			*now = now.Add(api.ObjectNodeRecoveryWindow + 48*time.Hour)
			_ = s.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRecoveryCleanupKilledProcessesResumeIndexAndPairedRetirement$")
			command.Env = append(os.Environ(), "GREGALE_INDEXED_RECOVERY_BOUNDARY="+boundary, "GREGALE_INDEXED_RECOVERY_DIRECTORY="+s.root, "GREGALE_INDEXED_RECOVERY_CLOCK="+now.Format(time.RFC3339Nano))
			output, err := command.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			command.Stderr = os.Stderr
			if err = command.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = command.Process.Kill(); _ = command.Wait() }()
			if scanner := bufio.NewScanner(output); !scanner.Scan() || scanner.Text() != "indexed recovery boundary" {
				t.Fatal("child missed boundary", boundary)
			}
			if err = command.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			_ = command.Wait()
			next, err := Open(s.root)
			if err != nil {
				t.Fatal("restart failed", err)
			}
			next.recoveryClock = func() time.Time { return *now }
			p := cleanupStep(t, next, 1)
			if p.Candidates != 0 || readText(t, next, "survives") != "current bytes" {
				t.Fatal("retirement did not reconcile index", p)
			}
			if _, err = next.readReceipt(u.ReceiptID); !errors.Is(err, ErrNoReceipt) {
				t.Fatal("receipt resurrected", err)
			}
			if _, err = next.readMultipart(u.ID); !errors.Is(err, ErrNoUpload) {
				t.Fatal("session resurrected", err)
			}
			_ = next.Close()
			backup := filepath.Join(t.TempDir(), "backup")
			if _, err = Backup(t.Context(), s.root, backup); err != nil {
				t.Fatal("resumed catalog cannot be backed up", err)
			}
			if _, err = VerifyBackup(t.Context(), backup); err != nil {
				t.Fatal(err)
			}
			if _, err = os.Stat(filepath.Join(backup, "data", recoveryIndexName)); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("local index leaked into backup", err)
			}
		})
	}
}

func TestRecoveryCleanupRebuildsOnPromotionAndReplicatesRetirement(t *testing.T) {
	s, follower := replicationPair(t)
	now := time.Now().UTC()
	s.recoveryClock = func() time.Time { return now }
	if _, err := s.EnableRecoveryRetention(); err != nil {
		t.Fatal(err)
	}
	a := recoveryPutSettlement(t, s, "retire-before-promotion", "old", objectrecovery.NewID())
	acknowledgeTestRecovery(t, s, a)
	putText(t, s, a.Key, "replacement")
	b := recoveryPutSettlement(t, s, "retire-after-promotion", "old", objectrecovery.NewID())
	acknowledgeTestRecovery(t, s, b)
	putText(t, s, b.Key, "replacement")
	unknown := recoveryPutSettlement(t, s, "unknown", "protected", objectrecovery.NewID())
	catchUpTestReplica(t, s, follower)
	now = now.Add(api.ObjectNodeRecoveryWindow + 48*time.Hour)
	if p := cleanupStep(t, s, 1); p.ReceiptsRemoved != 1 {
		t.Fatal(p)
	}
	if _, err := FenceReplicaPrimary(t.Context(), follower, localReplicationSource{s}); err != nil {
		t.Fatal(err)
	}
	if _, err := PromoteReplica(t.Context(), follower, localReplicationSource{s}); !errors.Is(err, ErrReplicationFork) {
		t.Fatal("lagging follower bypassed retirement", err)
	}
	catchUpTestReplica(t, s, follower)
	if _, err := PromoteReplica(t.Context(), follower, localReplicationSource{s}); err != nil {
		t.Fatal(err)
	}
	next, err := Open(follower)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	next.recoveryClock = func() time.Time { return now }
	if p := cleanupStep(t, next, 1); p.IndexRebuilds != 1 || p.ReceiptsRemoved != 1 || p.Candidates != 0 {
		t.Fatal("promoted primary did not rebuild replicated eligibility", p)
	}
	for _, a := range []objectrecovery.Settlement{a, b} {
		if _, err = next.Receipt(a.Bucket, a.Key, a.ID); !errors.Is(err, ErrNoReceipt) {
			t.Fatal("promotion resurrected proof", err)
		}
		if err = next.checkNewReceiptID(a.ID); !errors.Is(err, ErrReceiptExpired) || readText(t, next, a.Key) != "replacement" {
			t.Fatal("retired identity or current bytes lost", err)
		}
	}
	if _, err = next.Receipt(unknown.Bucket, unknown.Key, unknown.ID); err != nil {
		t.Fatal("unacknowledged proof lost", err)
	}
}
