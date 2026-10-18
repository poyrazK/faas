package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/objectnode"
	"github.com/onebox-faas/faas/pkg/s3gateway"
)

func TestReplicationRetentionCommandsWithAuthenticatedWorker(t *testing.T) {
	directory, seed, follower := filepath.Join(t.TempDir(), "primary"), filepath.Join(t.TempDir(), "seed"), filepath.Join(t.TempDir(), "follower")
	node, err := objectnode.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err = node.CreateBucket("test-bucket"); err != nil {
		t.Fatal(err)
	}
	_ = node.Close()
	for _, args := range [][]string{
		{"replication-enable", "--data-dir", directory},
		{"backup", "--data-dir", directory, "--backup-dir", seed},
		{"replica-bootstrap", "--data-dir", follower, "--backup-dir", seed},
	} {
		if err = run(t.Context(), args, &bytes.Buffer{}); err != nil {
			t.Fatal(args, err)
		}
	}
	local, err := objectnode.ReadReplicationStatus(follower)
	if err != nil {
		t.Fatal(err)
	}
	registration := []string{"replica-register", "--data-dir", directory, "--replica-id", local.NodeID, "--sequence", strconv.FormatInt(local.AppliedSequence, 10), "--hash", local.AppliedHash}
	if err = run(t.Context(), registration, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	node, err = objectnode.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer node.Close()
	if _, err = node.Put(t.Context(), "test-bucket", "key", strings.NewReader("retained"), 8, objectnode.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	if err = run(t.Context(), []string{"compact", "--data-dir", directory}, &bytes.Buffer{}); err == nil {
		t.Fatal("compaction acquired an active primary's process lock")
	}
	h, err := s3gateway.NewDiskHandler(s3gateway.DiskConfig{Store: node, AccessKeyID: "retention-cli", SecretAccessKey: "retention-cli", Region: "us-east-1"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(h)
	defer server.Close()
	source, err := objectnode.NewHTTPReplicationSource(server.URL, "us-east-1", "retention-cli", "retention-cli")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAAS_OBJECT_NODE_ACCESS_KEY_ID", "retention-cli")
	t.Setenv("FAAS_OBJECT_NODE_SECRET_ACCESS_KEY", "retention-cli")
	if err = run(t.Context(), []string{"replica-catch-up", "--data-dir", follower, "--source", server.URL}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	head, err := source.Head(t.Context())
	if err != nil || len(head.Followers) != 1 || head.Followers[0].Sequence != 1 {
		t.Fatal("HTTP worker did not persist its applied cursor", head, err)
	}
	metrics := httptest.NewRecorder()
	h.MetricsHandler().ServeHTTP(metrics, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	for _, line := range []string{"faas_object_node_retention_records 1", "faas_object_node_retention_safe_sequence 1", "faas_object_node_retention_registered_followers 1"} {
		if !strings.Contains(metrics.Body.String(), line) {
			t.Fatal("missing retention metric", line, metrics.Body.String())
		}
	}
	if strings.Contains(metrics.Body.String(), local.NodeID) {
		t.Fatal("metrics exposed a follower identity")
	}
	_ = node.Close()
	var output bytes.Buffer
	if err = run(t.Context(), []string{"compact", "--data-dir", directory}, &output); err != nil {
		t.Fatal(err)
	}
	var result objectnode.CompactionResult
	if err = json.Unmarshal(output.Bytes(), &result); err != nil || result.Status.BaseSequence != 1 || result.After.Records != 0 || result.After.Blobs != 0 {
		t.Fatal(output.String(), err)
	}
	node, err = objectnode.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer node.Close()
	freshHandler, err := s3gateway.NewDiskHandler(s3gateway.DiskConfig{Store: node, AccessKeyID: "retention-cli", SecretAccessKey: "retention-cli", Region: "us-east-1"})
	if err != nil {
		t.Fatal(err)
	}
	fresh := httptest.NewServer(freshHandler)
	defer fresh.Close()
	source, _ = objectnode.NewHTTPReplicationSource(fresh.URL, "us-east-1", "retention-cli", "retention-cli")
	if _, err = source.Entry(t.Context(), result.Status.StreamID, 1); !errors.Is(err, objectnode.ErrReplicaReseed) {
		t.Fatal("HTTP did not expose reseed-required status", err)
	}
	if err = run(t.Context(), []string{"replica-catch-up", "--data-dir", follower, "--source", fresh.URL}, &bytes.Buffer{}); err != nil {
		t.Fatal("floor-equal follower could not report again", err)
	}
	_ = node.Close()
	if err = run(t.Context(), []string{"replica-retire", "--data-dir", directory, "--replica-id", local.NodeID}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if err = run(t.Context(), []string{"compact", "--data-dir", follower}, &bytes.Buffer{}); err != nil {
		t.Fatal("offline follower compaction command failed", err)
	}
	for _, args := range [][]string{
		{"replica-register", "--data-dir", directory, "--replica-id", local.NodeID},
		{"compact", "--data-dir", directory, "--through", "-2"},
		{"replica-retire", "--data-dir", directory},
		{"compact", "--data-dir", directory, "extra"},
	} {
		if err = run(t.Context(), args, &bytes.Buffer{}); err == nil {
			t.Fatal("invalid retention command succeeded", args)
		}
	}
}
