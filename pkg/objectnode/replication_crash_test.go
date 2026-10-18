package objectnode

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestReplicationPrimaryCrashBoundaries(t *testing.T) {
	for _, boundary := range []string{"before journal", "after journal", "before received", "after received", "before catalog", "after catalog", "before applied", "after applied"} {
		t.Run(boundary, func(t *testing.T) {
			primary, follower := replicationPair(t)
			putText(t, primary, "key", "old")
			injected := errors.New("injected replication persistence failure")
			fired := false
			persist := func(path string, value any, base func(string, any) (bool, error)) (bool, error) {
				stage := ""
				switch value := value.(type) {
				case JournalEntry:
					if value.Sequence == 2 {
						stage = "journal"
					}
				case replicationState:
					if value.ReceivedSequence == 2 {
						stage = "received"
						if value.AppliedSequence == 2 {
							stage = "applied"
						}
					}
				default:
					if path == filepath.Join(primary.objectPath("test-bucket", "key"), "current.json") {
						stage = "catalog"
					}
				}
				if !fired && boundary == "before "+stage {
					fired = true
					return false, injected
				}
				published, err := base(path, value)
				if err == nil && !fired && boundary == "after "+stage {
					fired = true
					return published, injected
				}
				return published, err
			}
			primary.replicationPersist = func(path string, value any) (bool, error) { return persist(path, value, writeReplicationJSON) }
			primary.persist = func(path string, value any) (bool, error) { return persist(path, value, writeJSON) }
			if _, err := primary.Put(t.Context(), "test-bucket", "key", strings.NewReader("new"), 3, PutOptions{}); !errors.Is(err, injected) || !fired {
				t.Fatal("fault not reached", err)
			}
			if err := primary.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := Open(primary.root)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			want := "new"
			if boundary == "before journal" {
				want = "old"
			}
			if readText(t, reopened, "key") != want {
				t.Fatal("journal chose wrong publication")
			}
			catchUpTestReplica(t, reopened, follower)
			copy, err := openReplica(follower)
			if err != nil {
				t.Fatal(err)
			}
			defer copy.Close()
			if followerText(t, copy, "key") != want {
				t.Fatal("follower lost recovered publication")
			}
		})
	}
}

func TestReplicationFollowerCheckpointReplay(t *testing.T) {
	for _, boundary := range []string{"before received", "after received", "before catalog", "after catalog", "before applied", "after applied"} {
		t.Run(boundary, func(t *testing.T) {
			primary, follower := replicationPair(t)
			o := putText(t, primary, "key", "durable")
			copy, err := openReplica(follower)
			if err != nil {
				t.Fatal(err)
			}
			fired := false
			injected := errors.New("injected follower persistence failure")
			persist := func(path string, value any, base func(string, any) (bool, error)) (bool, error) {
				stage := ""
				if state, ok := value.(replicationState); ok && state.ReceivedSequence == 1 {
					stage = "received"
					if state.AppliedSequence == 1 {
						stage = "applied"
					}
				}
				if strings.HasSuffix(path, "current.json") {
					stage = "catalog"
				}
				if !fired && boundary == "before "+stage {
					fired = true
					return false, injected
				}
				published, err := base(path, value)
				if err == nil && !fired && boundary == "after "+stage {
					fired = true
					return published, injected
				}
				return published, err
			}
			copy.replicationPersist = func(path string, value any) (bool, error) { return persist(path, value, writeReplicationJSON) }
			copy.persist = func(path string, value any) (bool, error) { return persist(path, value, writeJSON) }
			if _, err = copy.catchUp(t.Context(), localReplicationSource{primary}, CatchUpOptions{MaxEntries: 32, MaxBytes: 64 << 20}); !errors.Is(err, injected) || !fired {
				t.Fatal("fault not reached", err)
			}
			if progress, err := copy.catchUp(t.Context(), localReplicationSource{primary}, CatchUpOptions{MaxEntries: 32, MaxBytes: 64 << 20}); err != nil || !progress.CaughtUp {
				t.Fatal("live worker could not recover checkpoint acknowledgement", progress, err)
			}
			if err = copy.Close(); err != nil {
				t.Fatal(err)
			}
			catchUpTestReplica(t, primary, follower)
			copy, err = openReplica(follower)
			if err != nil {
				t.Fatal(err)
			}
			defer copy.Close()
			got, err := readObject(copy.objectPath("test-bucket", "key"))
			if err != nil || got.ID != o.ID || followerText(t, copy, "key") != "durable" {
				t.Fatal("replay changed immutable identity", got, err)
			}
		})
	}
}

func TestReplicationKilledProcessesRecover(t *testing.T) {
	if mode := os.Getenv("GREGALE_REPLICATION_CRASH_MODE"); mode != "" {
		primary, err := Open(os.Getenv("GREGALE_REPLICATION_CRASH_PRIMARY"))
		if err != nil {
			t.Fatal(err)
		}
		stop := func() { _, _ = os.Stdout.WriteString("replication checkpoint reached\n"); select {} }
		if mode == "primary" {
			primary.replicationPersist = func(path string, value any) (bool, error) {
				published, err := writeReplicationJSON(path, value)
				if _, ok := value.(JournalEntry); ok && err == nil {
					stop()
				}
				return published, err
			}
			_, err = primary.Put(context.Background(), "test-bucket", "key", strings.NewReader("new"), 3, PutOptions{})
		} else {
			copy, openErr := openReplica(os.Getenv("GREGALE_REPLICATION_CRASH_FOLLOWER"))
			if openErr != nil {
				t.Fatal(openErr)
			}
			copy.persist = func(path string, value any) (bool, error) {
				published, err := writeJSON(path, value)
				if strings.HasSuffix(path, "current.json") && err == nil {
					stop()
				}
				return published, err
			}
			_, err = copy.catchUp(context.Background(), localReplicationSource{primary}, CatchUpOptions{MaxEntries: api.ObjectNodeReplicationBatch, MaxBytes: api.ObjectNodeReplicationStepBytes})
		}
		t.Fatal("crash checkpoint was not reached", err)
	}
	for _, mode := range []string{"primary", "follower"} {
		t.Run(mode, func(t *testing.T) {
			primary, follower := replicationPair(t)
			if mode == "follower" {
				putText(t, primary, "key", "new")
			}
			if err := primary.Close(); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestReplicationKilledProcessesRecover$")
			command.Env = append(os.Environ(), "GREGALE_REPLICATION_CRASH_MODE="+mode, "GREGALE_REPLICATION_CRASH_PRIMARY="+primary.root, "GREGALE_REPLICATION_CRASH_FOLLOWER="+follower)
			out, err := command.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			command.Stderr = os.Stderr
			if err = command.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = command.Process.Kill() }()
			if scanner := bufio.NewScanner(out); !scanner.Scan() || scanner.Text() != "replication checkpoint reached" {
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
			if readText(t, reopened, "key") != "new" {
				t.Fatal("crash lost committed journal bytes")
			}
			catchUpTestReplica(t, reopened, follower)
			copy, err := openReplica(follower)
			if err != nil {
				t.Fatal(err)
			}
			defer copy.Close()
			if followerText(t, copy, "key") != "new" {
				t.Fatal("follower did not recover after process death")
			}
		})
	}
}

func TestReplicaBootstrapAtNonzeroPositionAndRestoreFencing(t *testing.T) {
	primary, _ := replicationPair(t)
	putText(t, primary, "key", "snapshot")
	if err := primary.Close(); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "backup")
	if _, err := Backup(t.Context(), primary.root, backup); err != nil {
		t.Fatal(err)
	}
	follower := filepath.Join(t.TempDir(), "restored")
	if _, err := RestoreBackup(t.Context(), backup, follower); err != nil {
		t.Fatal(err)
	}
	status, err := ReadReplicationStatus(follower)
	if err != nil || status.Role != "follower" || status.BaseSequence != 1 || status.AppliedSequence != 1 {
		t.Fatal(status, err)
	}
	if node, err := Open(follower); !errors.Is(err, ErrReplica) {
		if node != nil {
			_ = node.Close()
		}
		t.Fatal("restore cloned primary authority", err)
	}
	reopened, err := Open(primary.root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	putText(t, reopened, "key", "after snapshot")
	catchUpTestReplica(t, reopened, follower)
	copy, err := openReplica(follower)
	if err != nil {
		t.Fatal(err)
	}
	defer copy.Close()
	if followerText(t, copy, "key") != "after snapshot" {
		t.Fatal("snapshot boundary skipped a change")
	}
	if _, err = VerifyBackup(t.Context(), backup); err != nil {
		t.Fatal("bootstrap modified snapshot", err)
	}
}

func TestReplicaBackupHeaderCannotDowngradeFencing(t *testing.T) {
	primary, _ := replicationPair(t)
	if err := primary.Close(); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "backup")
	if _, err := Backup(t.Context(), primary.root, backup); err != nil {
		t.Fatal(err)
	}
	info, err := readBackupInfo(backup)
	if err != nil {
		t.Fatal(err)
	}
	info.FormatVersion = 3
	if _, err = writeJSON(filepath.Join(backup, "backup.json"), info); err != nil {
		t.Fatal(err)
	}
	if _, err = VerifyBackup(t.Context(), backup); !errors.Is(err, ErrCorrupt) {
		t.Fatal("mismatched format marker verified", err)
	}
	destination := filepath.Join(t.TempDir(), "restored")
	if _, err = RestoreBackup(t.Context(), backup, destination); !errors.Is(err, ErrCorrupt) {
		t.Fatal("backup header bypassed follower role", err)
	}
	if _, err = os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unfenced restored directory was published", err)
	}
}
