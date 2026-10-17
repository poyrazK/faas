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

func TestRecoveryCollectionKilledProcessesResumePairedDeletion(t *testing.T) {
	if boundary := os.Getenv("GREGALE_RECOVERY_BOUNDARY"); boundary != "" {
		s, err := Open(os.Getenv("GREGALE_RECOVERY_DIRECTORY"))
		if err != nil {
			t.Fatal(err)
		}
		now, err := time.Parse(time.RFC3339Nano, os.Getenv("GREGALE_RECOVERY_CLOCK"))
		if err != nil {
			t.Fatal(err)
		}
		s.recoveryClock = func() time.Time { return now }
		s.recoveryCheckpoint = func(phase string) error {
			if phase == boundary {
				_, _ = os.Stdout.WriteString("recovery boundary\n")
				select {}
			}
			return nil
		}
		_, err = s.CollectRecovery(context.Background(), "", api.ObjectNodeRecoveryBatch)
		t.Fatal("child missed boundary", boundary, err)
	}
	for _, boundary := range []string{"barrier synced", "receipt removed", "session removed"} {
		t.Run(boundary, func(t *testing.T) {
			s, now := recoveryTestStore(t)
			u := prepareMultipartRetirement(t, s, "committed")
			putText(t, s, "survives", "current bytes")
			*now = now.Add(api.ObjectNodeRecoveryWindow + 48*time.Hour)
			_ = s.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRecoveryCollectionKilledProcessesResumePairedDeletion$")
			command.Env = append(os.Environ(), "GREGALE_RECOVERY_BOUNDARY="+boundary, "GREGALE_RECOVERY_DIRECTORY="+s.root, "GREGALE_RECOVERY_CLOCK="+now.Format(time.RFC3339Nano))
			output, err := command.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			command.Stderr = os.Stderr
			if err = command.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = command.Process.Kill(); _ = command.Wait() }()
			if scanner := bufio.NewScanner(output); !scanner.Scan() || scanner.Text() != "recovery boundary" {
				t.Fatal("child did not reach boundary", boundary)
			}
			if err = command.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			_ = command.Wait()
			reopened, err := Open(s.root)
			if err != nil {
				t.Fatal("restart failed to finish paired deletion", err)
			}
			if _, err = reopened.readReceipt(u.ReceiptID); !errors.Is(err, ErrNoReceipt) {
				t.Fatal("retired proof returned", err)
			}
			if _, err = reopened.readMultipart(u.ID); !errors.Is(err, ErrNoUpload) {
				t.Fatal("paired tombstone was not removed", err)
			}
			if err = reopened.checkNewReceiptID(u.ReceiptID); !errors.Is(err, ErrReceiptExpired) || readText(t, reopened, "survives") != "current bytes" {
				t.Fatal("restart reopened the identity or lost current data", err)
			}
			_ = reopened.Close()
			backup := filepath.Join(t.TempDir(), "backup")
			if _, err = Backup(t.Context(), s.root, backup); err != nil {
				t.Fatal("restart did not leave a verifiable backup", err)
			}
		})
	}
}

func TestRecoveryCollectionReplicatesBeforePromotion(t *testing.T) {
	s, follower := replicationPair(t)
	now := time.Now().UTC()
	s.recoveryClock = func() time.Time { return now }
	if _, err := s.EnableRecoveryRetention(); err != nil {
		t.Fatal(err)
	}
	a := recoveryPutSettlement(t, s, "gone", "original", objectrecovery.NewID())
	acknowledgeTestRecovery(t, s, a)
	putText(t, s, a.Key, "replacement")
	unknown := recoveryPutSettlement(t, s, "pending", "recover me", objectrecovery.NewID())
	u := prepareMultipartRetirement(t, s, "aborted")
	catchUpTestReplica(t, s, follower)
	if version, err := nodeFormatVersion(follower); err != nil || version != 5 {
		t.Fatal("follower did not upgrade before accepting retention state", version, err)
	}
	now = now.Add(api.ObjectNodeRecoveryWindow + 48*time.Hour)
	if _, err := s.CollectRecovery(t.Context(), "", api.ObjectNodeRecoveryBatch); err != nil {
		t.Fatal(err)
	}
	grant, err := FenceReplicaPrimary(t.Context(), follower, localReplicationSource{s})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = PromoteReplica(t.Context(), follower, localReplicationSource{s}); !errors.Is(err, ErrReplicationFork) {
		t.Fatal("lagging follower skipped retirement decisions", err)
	}
	catchUpTestReplica(t, s, follower)
	status, err := PromoteReplica(t.Context(), follower, localReplicationSource{s})
	if err != nil || status.Epoch != grant.Epoch+1 {
		t.Fatal(status, err)
	}
	next, err := Open(follower)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if _, err = next.Receipt(a.Bucket, a.Key, a.ID); !errors.Is(err, ErrNoReceipt) {
		t.Fatal("promotion restored collected proof", err)
	}
	if err = next.checkNewReceiptID(a.ID); !errors.Is(err, ErrReceiptExpired) || readText(t, next, a.Key) != "replacement" {
		t.Fatal("promotion reopened a retired request", err)
	}
	if _, err = next.MultipartInfo(u.Bucket, u.Key, u.ID); !errors.Is(err, ErrNoUpload) {
		t.Fatal("promotion resurrected a retired upload", err)
	}
	if proof, err := next.Receipt(unknown.Bucket, unknown.Key, unknown.ID); err != nil || proof.Status != ReceiptCommitted {
		t.Fatal("unacknowledged proof lost during promotion", proof, err)
	}
}
