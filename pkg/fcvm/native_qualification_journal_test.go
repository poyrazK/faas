//go:build linux || darwin

// adr: 568 — incoming attempt revocation must precede any delayed Create RPC.
package fcvm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func nativeQualificationFixture(t *testing.T) (*nativeQualificationJournal, state.EnvironmentQualificationExecution, context.Context) {
	t.Helper()
	frame := state.EnvironmentQualificationExecution{InstanceID: uuid.NewString(), RequestID: uuid.NewString(), GraphID: uuid.NewString(),
		AppID: uuid.NewString(), DeploymentID: uuid.NewString(), NodeID: uuid.NewString(), WakeID: uuid.NewString(), SourceID: uuid.NewString(),
		EnvironmentID: uuid.NewString(), RevisionID: uuid.NewString(), Resource: "workload/api", Scope: "production", PlanHash: strings.Repeat("a", 64),
		Generation: 1, IntentVersion: 1, Attempt: 1, RAMMB: 256, CleanupToken: uuid.NewString(),
		Artifact: state.EnvironmentWorkloadArtifact{RootfsKey: "apps/qualified.ext4", RootfsBytes: 1234, Kind: state.DeploymentKindImage}}
	root := t.TempDir()
	j := nativeJournalFixture(filepath.Join(root, ".native-processes")).qualifications(frame.NodeID)
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	t.Cleanup(cancel)
	return j, frame, ctx
}

func TestNativeQualificationRetireBeforeCreateRetainsExactTombstone(t *testing.T) {
	j, frame, ctx := nativeQualificationFixture(t)
	revoked, err := j.revoke(t.Context(), frame)
	if err != nil || !revoked.Revoked || revoked.CreateStarted || !revoked.Deadline.IsZero() {
		t.Fatalf("original pre-dispatch revocation: %s %v", revoked, err)
	}
	restarted := &nativeQualificationJournal{root: j.root, nodeID: j.nodeID, owner: j.owner}
	if _, err := restarted.claim(ctx, frame); err == nil {
		t.Fatal("delayed Create bypassed durable original revocation")
	}
	again, err := restarted.revoke(t.Context(), frame)
	if err != nil || again.Generation != revoked.Generation || again.Execution != frame {
		t.Fatal("recovery changed original tombstone", err)
	}
	for _, diagnostic := range []string{fmt.Sprintf("%v", again), fmt.Sprintf("%+v", again), fmt.Sprintf("%#v", again)} {
		if strings.Contains(diagnostic, frame.CleanupToken) {
			t.Fatal("diagnostic exposed cleanup authority")
		}
	}
	public, err := json.Marshal(again.Execution)
	if err != nil || strings.Contains(string(public), frame.CleanupToken) {
		t.Fatal("execution public projection exposed cleanup authority", err)
	}
	path, err := j.path(frame.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal("cleanup authority is not in a private journal", err)
	}
}

func TestNativeQualificationClaimAndRevocationCannotBorrowAnotherFrame(t *testing.T) {
	j, frame, ctx := nativeQualificationFixture(t)
	claimed, err := j.claim(ctx, frame)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := j.claim(ctx, frame); err == nil {
		t.Fatal("duplicate Create gained dispatch authority")
	}
	for _, field := range []string{"request", "wake", "source", "scope", "artifact", "generation", "attempt", "cleanup"} {
		changed := frame
		switch field {
		case "request":
			changed.RequestID = uuid.NewString()
		case "wake":
			changed.WakeID = uuid.NewString()
		case "source":
			changed.SourceID = uuid.NewString()
		case "scope":
			changed.Scope = "staging"
		case "artifact":
			changed.Artifact.RootfsKey = "apps/replacement.ext4"
		case "generation":
			changed.Generation++
		case "attempt":
			changed.Attempt++
		case "cleanup":
			changed.CleanupToken = uuid.NewString()
		}
		if _, err := j.revoke(t.Context(), changed); err == nil {
			t.Fatalf("changed %s gained original cleanup authority", field)
		}
	}
	revoked, err := j.revoke(t.Context(), frame)
	if err != nil || !revoked.Revoked || !revoked.CreateStarted || revoked.Generation != claimed.Generation || !revoked.Deadline.Equal(claimed.Deadline) {
		t.Fatal("revocation rewrote the frozen dispatch", err)
	}
}

func TestNativeQualificationLostPublicationAcknowledgementCannotReplayCreate(t *testing.T) {
	j, frame, ctx := nativeQualificationFixture(t)
	j.writeValue = func(path string, record nativeQualificationRecord) error {
		if err := writeNativeJournalValue(path, record); err != nil {
			return err
		}
		return errors.New("injected acknowledgement loss")
	}
	if _, err := j.claim(ctx, frame); err == nil {
		t.Fatal("publication uncertainty was ignored")
	}
	restarted := &nativeQualificationJournal{root: j.root, nodeID: j.nodeID, owner: j.owner}
	if _, err := restarted.claim(ctx, frame); err == nil {
		t.Fatal("uncertain publication authorized another Create")
	}
	if _, err := restarted.revoke(t.Context(), frame); err != nil {
		t.Fatal("original revocation was not recoverable", err)
	}
}

func TestNativeQualificationClaimAndRevocationShareOriginalFileLock(t *testing.T) {
	j, frame, ctx := nativeQualificationFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	j.writeValue = func(path string, record nativeQualificationRecord) error {
		close(entered)
		<-release
		return writeNativeJournalValue(path, record)
	}
	done := make(chan error, 1)
	go func() { _, err := j.claim(ctx, frame); done <- err }()
	<-entered
	other := &nativeQualificationJournal{root: j.root, nodeID: j.nodeID, owner: j.owner}
	cleanup, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	_, err := other.revoke(cleanup, frame)
	cancel()
	close(release)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("revocation bypassed an original incoming producer: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := other.revoke(t.Context(), frame); err != nil {
		t.Fatal(err)
	}
	if _, err := other.claim(ctx, frame); err == nil {
		t.Fatal("another journal borrowed revoked dispatch authority")
	}
}

func TestNativeQualificationDamagedOrPublicJournalHoldsOwnership(t *testing.T) {
	for _, damage := range []string{"public", "symlink", "incomplete", "version"} {
		t.Run(damage, func(t *testing.T) {
			j, frame, ctx := nativeQualificationFixture(t)
			if _, err := j.claim(ctx, frame); err != nil {
				t.Fatal(err)
			}
			path, err := j.path(frame.InstanceID)
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			switch damage {
			case "public":
				err = os.Chmod(path, 0o644)
			case "symlink":
				target := filepath.Join(t.TempDir(), "replacement")
				if err = os.WriteFile(target, data, 0o600); err == nil {
					err = os.Remove(path)
				}
				if err == nil {
					err = os.Symlink(target, path)
				}
			case "incomplete":
				err = os.WriteFile(path, []byte(strings.Replace(string(data), `,"revoked":false`, "", 1)), 0o600)
			case "version":
				err = os.WriteFile(path, []byte(strings.Replace(string(data), `"version":2`, `"version":3`, 1)), 0o600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := j.revoke(t.Context(), frame); err == nil {
				t.Fatalf("%s journal gained retirement authority", damage)
			}
			if _, err := j.claim(ctx, frame); err == nil {
				t.Fatalf("%s journal gained dispatch authority", damage)
			}
		})
	}
}

func TestNativeQualificationStrictRecordAndKernelBootProof(t *testing.T) {
	j, frame, ctx := nativeQualificationFixture(t)
	record, err := j.claim(ctx, frame)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	for _, damaged := range []string{
		strings.Replace(string(raw), `,"revoked":false`, "", 1),
		strings.Replace(string(raw), `"revoked":false`, `"revoked":false,"revoked":true`, 1),
		strings.Replace(string(raw), `"create_started":true`, `"create_started":null`, 1),
		strings.Replace(string(raw), `"rootfs_bytes":1234`, `"rootfs_bytes":1234,"rootfs_bytes":1235`, 1),
		strings.Replace(string(raw), `"rootfs_bytes":1234`, `"unknown":1234`, 1),
		strings.Replace(string(raw), `"rootfs_key":"apps/qualified.ext4"`, `"rootfs_key":null`, 1),
		string(raw) + `{}`,
	} {
		var decoded nativeQualificationRecord
		if err := json.Unmarshal([]byte(damaged), &decoded); err == nil {
			t.Fatal("ambiguous incoming authority accepted")
		}
	}
	restarted := &nativeQualificationJournal{root: j.root, nodeID: j.nodeID, owner: &nativeLaunchJournal{bootID: func() (string, error) { return uuid.NewString(), nil }}}
	if _, err := restarted.revoke(t.Context(), frame); err == nil {
		t.Fatal("another kernel boot gained incoming authority")
	}
}

func TestNativeQualificationOriginalDispatchRequiresBoundedDeadlineAndNode(t *testing.T) {
	for _, kind := range []string{"unbounded", "expired", "oversized", "node", "alias"} {
		t.Run(kind, func(t *testing.T) {
			j, frame, ctx := nativeQualificationFixture(t)
			switch kind {
			case "unbounded":
				ctx = t.Context()
			case "expired":
				j.now = func() time.Time { return time.Now().Add(2 * time.Minute) }
			case "oversized":
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(t.Context(), api.EnvironmentGitOpsQualificationMaxLeaseDuration+time.Minute)
				defer cancel()
			case "node":
				frame.NodeID = uuid.NewString()
			case "alias":
				if _, err := j.revoke(t.Context(), frame); err != nil {
					t.Fatal(err)
				}
				frame.InstanceID = strings.ReplaceAll(frame.InstanceID, "-", "")
			}
			if _, err := j.claim(ctx, frame); err == nil {
				t.Fatalf("%s caller obtained an original dispatch claim", kind)
			}
		})
	}
}
