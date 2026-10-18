package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/objectnode"
	"github.com/onebox-faas/faas/pkg/s3gateway"
)

func TestReplicaDurabilityCommands(t *testing.T) {
	directory, backup, follower := filepath.Join(t.TempDir(), "primary"), filepath.Join(t.TempDir(), "seed"), filepath.Join(t.TempDir(), "follower")
	node, err := objectnode.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err = node.CreateBucket("test-bucket"); err != nil {
		t.Fatal(err)
	}
	if err = node.Close(); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"replication-enable", "--data-dir", directory},
		{"backup", "--data-dir", directory, "--backup-dir", backup},
		{"replica-bootstrap", "--data-dir", follower, "--backup-dir", backup},
	} {
		if err = run(t.Context(), args, &bytes.Buffer{}); err != nil {
			t.Fatal(args, err)
		}
	}
	local, err := objectnode.ReadReplicationStatus(follower)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	args := []string{"replication-durability", "--data-dir", directory, "--mode", "replica", "--replica-id", local.NodeID}
	if err = run(t.Context(), args, &output); err != nil {
		t.Fatal(err)
	}
	var configured objectnode.ReplicationStatus
	if err = json.Unmarshal(output.Bytes(), &configured); err != nil || configured.Durability == nil || configured.Durability.Mode != objectnode.DurabilityReplica {
		t.Fatal(output.String(), err)
	}
	node, err = objectnode.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = node.Put(t.Context(), "test-bucket", "key", strings.NewReader("pending"), 7, objectnode.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	if err = node.Close(); err != nil {
		t.Fatal(err)
	}
	if err = run(t.Context(), []string{"replication-durability", "--data-dir", directory, "--mode", "local"}, &bytes.Buffer{}); !errors.Is(err, objectnode.ErrReplicaPending) {
		t.Fatal("CLI weakened an unconfirmed promise", err)
	}
	node, err = objectnode.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer node.Close()
	h, err := s3gateway.NewDiskHandler(s3gateway.DiskConfig{Store: node, AccessKeyID: "durability-cli", SecretAccessKey: "durability-cli", Region: "us-east-1"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(h)
	defer server.Close()
	t.Setenv("FAAS_OBJECT_NODE_ACCESS_KEY_ID", "durability-cli")
	t.Setenv("FAAS_OBJECT_NODE_SECRET_ACCESS_KEY", "durability-cli")
	output.Reset()
	if err = run(t.Context(), []string{"replica-catch-up", "--data-dir", follower, "--source", server.URL}, &output); err != nil {
		t.Fatal(err)
	}
	var progress objectnode.ReplicationProgress
	if err = json.Unmarshal(output.Bytes(), &progress); err != nil || progress.ConfirmedSequence == nil || *progress.ConfirmedSequence != 1 {
		t.Fatal(output.String(), err)
	}
	if err = node.Close(); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if err = run(t.Context(), []string{"replication-durability", "--data-dir", directory, "--mode", "local"}, &output); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(output.Bytes(), &configured); err != nil || configured.Durability.Mode != objectnode.DurabilityLocal {
		t.Fatal(output.String(), err)
	}
}
