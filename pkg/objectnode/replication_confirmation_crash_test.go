package objectnode

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type lostReplicaConfirmationSource struct{ localReplicationSource }

func (lostReplicaConfirmationSource) Confirm(context.Context, ReplicaConfirmation) (ReplicaDurability, error) {
	return ReplicaDurability{}, io.ErrUnexpectedEOF
}

func TestReplicaConfirmationKilledAtDurableCheckpoint(t *testing.T) {
	if mode := os.Getenv("GREGALE_CONFIRMATION_CRASH_MODE"); mode != "" {
		primary, err := Open(os.Getenv("GREGALE_CONFIRMATION_CRASH_PRIMARY"))
		if err != nil {
			t.Fatal(err)
		}
		stop := func() { _, _ = os.Stdout.WriteString("confirmation checkpoint reached\n"); select {} }
		if mode == "primary" {
			primary.replicationPersist = func(path string, value any) (bool, error) {
				published, err := writeReplicationJSON(path, value)
				if state, ok := value.(replicationState); ok && state.Durability != nil && state.Durability.ConfirmedSequence == 1 && err == nil {
					stop()
				}
				return published, err
			}
			_, err = primary.ConfirmReplica(context.Background(), confirmationAt(t, primary, 1))
		} else {
			copy, openErr := openReplica(os.Getenv("GREGALE_CONFIRMATION_CRASH_FOLLOWER"))
			if openErr != nil {
				t.Fatal(openErr)
			}
			copy.replicationPersist = func(path string, value any) (bool, error) {
				published, err := writeReplicationJSON(path, value)
				if state, ok := value.(replicationState); ok && state.AppliedSequence == 1 && err == nil {
					stop()
				}
				return published, err
			}
			_, err = copy.catchUp(context.Background(), localReplicationSource{primary}, CatchUpOptions{MaxEntries: api.ObjectNodeReplicationBatch, MaxBytes: api.ObjectNodeReplicationStepBytes})
		}
		t.Fatal("child missed confirmation checkpoint", err)
	}
	for _, mode := range []string{"primary", "follower"} {
		t.Run(mode, func(t *testing.T) {
			primary, follower := durableReplicationPair(t)
			putText(t, primary, "key", "durable")
			if mode == "primary" {
				progress, err := CatchUpReplica(t.Context(), follower, lostReplicaConfirmationSource{localReplicationSource{primary}}, CatchUpOptions{MaxEntries: api.ObjectNodeReplicationBatch, MaxBytes: api.ObjectNodeReplicationStepBytes})
				if !errors.Is(err, io.ErrUnexpectedEOF) || progress.Local.AppliedSequence != 1 {
					t.Fatal(progress, err)
				}
			}
			if err := primary.Close(); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestReplicaConfirmationKilledAtDurableCheckpoint$")
			command.Env = append(os.Environ(), "GREGALE_CONFIRMATION_CRASH_MODE="+mode, "GREGALE_CONFIRMATION_CRASH_PRIMARY="+primary.root, "GREGALE_CONFIRMATION_CRASH_FOLLOWER="+follower)
			output, err := command.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			command.Stderr = os.Stderr
			if err = command.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = command.Process.Kill(); _ = command.Wait() }()
			if scanner := bufio.NewScanner(output); !scanner.Scan() || scanner.Text() != "confirmation checkpoint reached" {
				t.Fatal("child did not reach durable checkpoint")
			}
			if err = command.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			_ = command.Wait()
			reopened, err := Open(primary.root)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			if mode == "follower" {
				requireReplicaPending(t, reopened)
			}
			progress := catchUpTestReplica(t, reopened, follower)
			if progress.EntriesApplied != 0 || progress.BytesTransferred != 0 || progress.ConfirmedSequence == nil || *progress.ConfirmedSequence != 1 {
				t.Fatal("checkpoint retry recopied or lost data", progress)
			}
			if err = reopened.WaitForReplica(t.Context()); err != nil {
				t.Fatal(err)
			}
			copy, err := openReplica(follower)
			if err != nil {
				t.Fatal(err)
			}
			defer copy.Close()
			if followerText(t, copy, "key") != "durable" {
				t.Fatal("confirmed payload was lost")
			}
		})
	}
}
