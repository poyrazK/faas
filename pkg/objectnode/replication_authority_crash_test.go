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
)

func TestReplicaAuthorityPersistenceFailures(t *testing.T) {
	for _, operation := range []string{"fence", "promotion"} {
		for _, boundary := range []string{"before", "after"} {
			t.Run(operation+"/"+boundary, func(t *testing.T) {
				primary, follower := replicationPair(t)
				putText(t, primary, "key", "durable")
				catchUpTestReplica(t, primary, follower)
				request := fenceTestRequest(t, primary, follower)
				candidate := primary
				if operation == "promotion" {
					if _, err := primary.FencePrimary(t.Context(), request); err != nil {
						t.Fatal(err)
					}
					var err error
					candidate, err = openReplica(follower)
					if err != nil {
						t.Fatal(err)
					}
				}
				injected, fired := errors.New("authority publication acknowledgement lost"), false
				candidate.replicationPersist = func(path string, value any) (bool, error) {
					state, ok := value.(replicationState)
					target := ok && (operation == "fence" && state.Role == "fenced" || operation == "promotion" && state.Role == "primary" && state.Epoch == 2)
					if target && !fired && boundary == "before" {
						fired = true
						return false, injected
					}
					published, err := writeReplicationJSON(path, value)
					if err == nil && target && !fired && boundary == "after" {
						fired = true
						return published, injected
					}
					return published, err
				}
				var err error
				if operation == "fence" {
					_, err = primary.FencePrimary(t.Context(), request)
					if !errors.Is(primary.CanServe(), ErrFenced) {
						t.Fatal("uncertain fence allowed service")
					}
					if _, e := primary.Put(t.Context(), "test-bucket", "late", strings.NewReader("late"), 4, PutOptions{}); !errors.Is(e, ErrFenced) {
						t.Fatal(e)
					}
				} else {
					_, err = candidate.promoteReplica(t.Context(), localReplicationSource{primary})
				}
				if !errors.Is(err, injected) || !fired {
					t.Fatal("publication fault not reached", err)
				}
				if err = candidate.Close(); err != nil {
					t.Fatal(err)
				}
				if operation == "fence" {
					primary, err = Open(primary.root)
					if err != nil {
						t.Fatal(err)
					}
					defer primary.Close()
					state, err := primary.ReplicationStatus()
					if err != nil || boundary == "after" && state.Role != "fenced" || boundary == "before" && state.Role != "primary" {
						t.Fatal(state, err)
					}
					if _, err = primary.FencePrimary(t.Context(), request); err != nil {
						t.Fatal("fence decision could not resume", err)
					}
				}
				if _, err = PromoteReplica(t.Context(), follower, localReplicationSource{primary}); err != nil {
					t.Fatal("promotion decision could not resume", err)
				}
				state, err := ReadReplicationStatus(follower)
				if err != nil || state.Role != "primary" || state.Epoch != 2 || state.AppliedSequence != 1 {
					t.Fatal(state, err)
				}
				if _, err = primary.FencePrimary(t.Context(), FenceRequest{StreamID: request.StreamID, PrimaryID: request.PrimaryID, Epoch: request.Epoch, TargetID: state.NodeID}); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestReplicaAuthorityKilledAtPublication(t *testing.T) {
	if operation := os.Getenv("GREGALE_HANDOFF_CRASH_OPERATION"); operation != "" {
		primary, err := Open(os.Getenv("GREGALE_HANDOFF_CRASH_PRIMARY"))
		if err != nil {
			t.Fatal(err)
		}
		follower, boundary := os.Getenv("GREGALE_HANDOFF_CRASH_FOLLOWER"), os.Getenv("GREGALE_HANDOFF_CRASH_BOUNDARY")
		candidate := primary
		if operation == "promotion" {
			candidate, err = openReplica(follower)
			if err != nil {
				t.Fatal(err)
			}
		}
		stop := func() { _, _ = os.Stdout.WriteString("authority boundary reached\n"); select {} }
		candidate.replicationPersist = func(path string, value any) (bool, error) {
			state, ok := value.(replicationState)
			target := ok && (operation == "fence" && state.Role == "fenced" || operation == "promotion" && state.Role == "primary")
			if target && boundary == "before" {
				stop()
			}
			published, err := writeReplicationJSON(path, value)
			if target && boundary == "after" && err == nil {
				stop()
			}
			return published, err
		}
		if operation == "fence" {
			_, err = primary.FencePrimary(context.Background(), fenceTestRequest(t, primary, follower))
		} else {
			_, err = candidate.promoteReplica(context.Background(), localReplicationSource{primary})
		}
		t.Fatal("child missed authority boundary", err)
	}
	for _, operation := range []string{"fence", "promotion"} {
		for _, boundary := range []string{"before", "after"} {
			t.Run(operation+"/"+boundary, func(t *testing.T) {
				primary, follower := replicationPair(t)
				putText(t, primary, "key", "durable")
				catchUpTestReplica(t, primary, follower)
				request := fenceTestRequest(t, primary, follower)
				if operation == "promotion" {
					if _, err := primary.FencePrimary(t.Context(), request); err != nil {
						t.Fatal(err)
					}
				}
				if err := primary.Close(); err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
				defer cancel()
				command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestReplicaAuthorityKilledAtPublication$")
				command.Env = append(os.Environ(), "GREGALE_HANDOFF_CRASH_OPERATION="+operation, "GREGALE_HANDOFF_CRASH_BOUNDARY="+boundary, "GREGALE_HANDOFF_CRASH_PRIMARY="+primary.root, "GREGALE_HANDOFF_CRASH_FOLLOWER="+follower)
				output, err := command.StdoutPipe()
				if err != nil {
					t.Fatal(err)
				}
				command.Stderr = os.Stderr
				if err = command.Start(); err != nil {
					t.Fatal(err)
				}
				defer func() { _ = command.Process.Kill(); _ = command.Wait() }()
				if scanner := bufio.NewScanner(output); !scanner.Scan() || scanner.Text() != "authority boundary reached" {
					t.Fatal("child did not reach authority boundary")
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
				if operation == "fence" {
					if boundary == "after" && !errors.Is(reopened.CanServe(), ErrFenced) {
						t.Fatal("restart forgot durable fence")
					}
					if _, err = reopened.FencePrimary(t.Context(), request); err != nil {
						t.Fatal(err)
					}
				}
				if _, err = PromoteReplica(t.Context(), follower, localReplicationSource{reopened}); err != nil {
					t.Fatal(err)
				}
				next, err := Open(follower)
				if err != nil {
					t.Fatal(err)
				}
				defer next.Close()
				if readText(t, next, "key") != "durable" {
					t.Fatal("interrupted promotion lost data")
				}
				status, err := next.ReplicationStatus()
				if err != nil || status.Epoch != 2 {
					t.Fatal("retry changed authority twice", status, err)
				}
			})
		}
	}
}
