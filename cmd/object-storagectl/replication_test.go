package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/objectnode"
	"github.com/onebox-faas/faas/pkg/s3gateway"
)

func TestReplicationCommands(t *testing.T) {
	source, backup, follower := filepath.Join(t.TempDir(), "primary"), filepath.Join(t.TempDir(), "backup"), filepath.Join(t.TempDir(), "follower")
	s, err := objectnode.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CreateBucket("test-bucket"); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"replication-enable", "--data-dir", source},
		{"backup", "--data-dir", source, "--backup-dir", backup},
		{"replica-bootstrap", "--backup-dir", backup, "--data-dir", follower},
	} {
		var output bytes.Buffer
		if err = run(t.Context(), args, &output); err != nil {
			t.Fatal(args, err)
		}
		if !json.Valid(output.Bytes()) {
			t.Fatal(output.String())
		}
	}
	s, err = objectnode.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = s.Put(t.Context(), "test-bucket", "key", strings.NewReader("body"), 4, objectnode.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	h, err := s3gateway.NewDiskHandler(s3gateway.DiskConfig{Store: s, AccessKeyID: "replication-cli-test", SecretAccessKey: "replication-cli-test", Region: "us-east-1"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(h)
	defer server.Close()
	t.Setenv("FAAS_OBJECT_NODE_ACCESS_KEY_ID", "replication-cli-test")
	t.Setenv("FAAS_OBJECT_NODE_SECRET_ACCESS_KEY", "replication-cli-test")
	var output bytes.Buffer
	if err = run(t.Context(), []string{"replica-catch-up", "--data-dir", follower, "--source", server.URL}, &output); err != nil {
		t.Fatal(err)
	}
	var progress objectnode.ReplicationProgress
	if err = json.Unmarshal(output.Bytes(), &progress); err != nil || !progress.CaughtUp || progress.BytesTransferred != 4 {
		t.Fatal(output.String(), err)
	}
	output.Reset()
	if err = run(t.Context(), []string{"replica-status", "--data-dir", follower}, &output); err != nil {
		t.Fatal(err)
	}
	var status objectnode.ReplicationStatus
	if err = json.Unmarshal(output.Bytes(), &status); err != nil || status.AppliedSequence != 1 {
		t.Fatal(output.String(), err)
	}
	// A continuous worker keeps its lock, retries a temporary outage, and closes
	// cleanly when interrupted after the next successful progress report.
	httpSource, err := objectnode.NewHTTPReplicationSource(server.URL, "us-east-1", "replication-cli-test", "replication-cli-test")
	if err != nil {
		t.Fatal(err)
	}
	transient := &transientReplicationSource{base: httpSource}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	output.Reset()
	if err = followReplica(ctx, follower, transient, objectnode.CatchUpOptions{MaxEntries: 32, MaxBytes: 64 << 20}, true, cancelProgressWriter{writer: &output, cancel: cancel}); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(&output)
	var failed, resumed replicationFollowProgress
	if err = decoder.Decode(&failed); err != nil {
		t.Fatal(err)
	}
	if err = decoder.Decode(&resumed); err != nil {
		t.Fatal(err)
	}
	if transient.calls != 2 || failed.Error == "" || failed.ConfirmedSequence != nil || resumed.Error != "" || !resumed.CaughtUp || resumed.Follow.Retries != 1 || resumed.Follow.Reconnects != 1 {
		t.Fatal("worker did not retry its outage", transient.calls, output.String())
	}
	if node, err := objectnode.Open(follower); !errors.Is(err, objectnode.ErrReplica) {
		if node != nil {
			_ = node.Close()
		}
		t.Fatal("CLI bootstrap produced a writable clone", err)
	}
	var grant objectnode.PrimaryHandoff
	for _, command := range []string{"primary-fence", "primary-fence", "replica-catch-up", "replica-promote", "replica-promote"} {
		output.Reset()
		if err = run(t.Context(), []string{command, "--data-dir", follower, "--source", server.URL}, &output); err != nil {
			t.Fatal(command, err)
		}
		switch command {
		case "primary-fence":
			var repeated objectnode.PrimaryHandoff
			if err = json.Unmarshal(output.Bytes(), &repeated); err != nil || grant.ID != "" && grant != repeated {
				t.Fatal("fence retry changed handoff", output.String(), err)
			}
			grant = repeated
		case "replica-promote":
			if err = json.Unmarshal(output.Bytes(), &status); err != nil || status.Role != "primary" || status.Epoch != 2 || status.Promotion == nil || status.Promotion.ID != grant.ID {
				t.Fatal("promotion retry changed authority", output.String(), err)
			}
		}
	}
	if err = s.CanServe(); !errors.Is(err, objectnode.ErrFenced) {
		t.Fatal("old primary remains writable", err)
	}
	next, err := objectnode.Open(follower)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	_, file, err := next.Get("test-bucket", "key")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(file)
	_ = file.Close()
	if err != nil || string(body) != "body" {
		t.Fatal(string(body), err)
	}
}

type transientReplicationSource struct {
	base  objectnode.ReplicationSource
	calls int
}

func (s *transientReplicationSource) Head(ctx context.Context) (objectnode.ReplicationStatus, error) {
	s.calls++
	if s.calls == 1 {
		return objectnode.ReplicationStatus{}, io.ErrUnexpectedEOF
	}
	return s.base.Head(ctx)
}
func (s *transientReplicationSource) Entry(ctx context.Context, stream string, sequence int64) (objectnode.JournalEntry, error) {
	return s.base.Entry(ctx, stream, sequence)
}
func (s *transientReplicationSource) Blob(ctx context.Context, stream, digest string, offset, length int64) (io.ReadCloser, error) {
	return s.base.Blob(ctx, stream, digest, offset, length)
}

type cancelProgressWriter struct {
	writer io.Writer
	cancel context.CancelFunc
}

func (w cancelProgressWriter) Write(body []byte) (int, error) {
	var progress replicationFollowProgress
	if err := json.Unmarshal(body, &progress); err != nil {
		return 0, err
	}
	n, err := w.writer.Write(body)
	if progress.Error == "" {
		w.cancel()
	}
	return n, err
}
