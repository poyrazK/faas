package s3gateway

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectnode"
)

func TestDiskNodeReplicationAuthenticatedCatchUp(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "primary")
	store, err := objectnode.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.CreateBucket("test-bucket"); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = objectnode.EnableReplication(t.Context(), dir); err != nil {
		t.Fatal(err)
	}
	backup, follower := filepath.Join(t.TempDir(), "backup"), filepath.Join(t.TempDir(), "follower")
	if _, err = objectnode.Backup(t.Context(), dir, backup); err != nil {
		t.Fatal(err)
	}
	if _, err = objectnode.BootstrapReplica(t.Context(), backup, follower); err != nil {
		t.Fatal(err)
	}
	store, err = objectnode.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	o, err := store.Put(t.Context(), "test-bucket", "key", strings.NewReader("replicated"), 10, objectnode.PutOptions{})
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewDiskHandler(DiskConfig{Store: store, AccessKeyID: diskTestCredential, SecretAccessKey: diskTestCredential, Region: "us-east-1"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(h)
	defer server.Close()
	source, err := objectnode.NewHTTPReplicationSource(server.URL, "us-east-1", diskTestCredential, diskTestCredential)
	if err != nil {
		t.Fatal(err)
	}
	progress, err := objectnode.CatchUpReplica(t.Context(), follower, source, objectnode.CatchUpOptions{MaxEntries: api.ObjectNodeReplicationBatch, MaxBytes: api.ObjectNodeReplicationStepBytes})
	if err != nil || !progress.CaughtUp || progress.Local.AppliedSequence != 1 || progress.BytesTransferred != 10 {
		t.Fatal(progress, err)
	}
	entry, err := source.Entry(t.Context(), progress.Local.StreamID, 1)
	if err != nil || entry.Blob == nil || entry.Blob.SHA256 != o.SHA256 {
		t.Fatal(entry, err)
	}
	body, err := source.Blob(t.Context(), entry.StreamID, o.SHA256, 2, 4)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(body)
	_ = body.Close()
	if err != nil || string(data) != "plic" {
		t.Fatal(string(data), err)
	}
	wrong, _ := objectnode.NewHTTPReplicationSource(server.URL, "us-east-1", diskTestCredential, "wrong-secret")
	if _, err = wrong.Head(t.Context()); err == nil {
		t.Fatal("invalid signature accepted")
	}
	for _, path := range []string{
		"/?gregale-replication=status&gregale-replication=status",
		"/?gregale-replication=status&gregale-inventory=usage",
		"/test-bucket?gregale-replication=status",
		"/?gregale-replication=entry&stream=x&sequence=01",
		"/?gregale-replication=blob&stream=x&sha256=../../outside&offset=0&length=1",
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, signedInventoryRequest(t, http.MethodGet, path))
		if w.Code < 400 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/?gregale-replication=status", nil))
	if w.Code != 403 {
		t.Fatal("unauthenticated journal was readable", w.Code)
	}
	if node, err := objectnode.Open(follower); !errors.Is(err, objectnode.ErrReplica) {
		if node != nil {
			_ = node.Close()
		}
		t.Fatal(err)
	}
	request := objectnode.FenceRequest{StreamID: progress.Local.StreamID, PrimaryID: progress.Local.PrimaryID, TargetID: progress.Local.NodeID, Epoch: progress.Local.Epoch}
	if _, err = wrong.Fence(t.Context(), request); err == nil {
		t.Fatal("unauthenticated operator fenced the node")
	}
	stale := request
	stale.Epoch++
	if _, err = source.Fence(t.Context(), stale); !errors.Is(err, objectnode.ErrReplicationFork) {
		t.Fatal("stale epoch fenced the node", err)
	}
	if err = store.CanServe(); err != nil {
		t.Fatal("rejected fence changed authority", err)
	}
	grant, err := source.Fence(t.Context(), request)
	if err != nil || grant.TargetID != request.TargetID || grant.Sequence != 1 {
		t.Fatal(grant, err)
	}
	if repeat, err := source.Fence(t.Context(), request); err != nil || repeat != grant {
		t.Fatal(repeat, err)
	}
	stale = request
	stale.TargetID = uuid.NewString()
	if _, err = source.Fence(t.Context(), stale); !errors.Is(err, objectnode.ErrReplicationFork) {
		t.Fatal("fence issued a second grant", err)
	}
	for _, operation := range []struct{ method, path string }{
		{http.MethodGet, "/test-bucket/key"},
		{http.MethodPut, "/test-bucket/late"},
		{http.MethodDelete, "/test-bucket/key"},
		{http.MethodGet, "/test-bucket?gregale-inventory=objects"},
		{http.MethodPost, "/?gregale-inventory=scrub"},
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, signedInventoryRequest(t, operation.method, operation.path))
		if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "PrimaryFenced") {
			t.Fatal(operation, w.Code, w.Body.String())
		}
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := objectnode.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	restartedHandler, err := NewDiskHandler(DiskConfig{Store: restarted, AccessKeyID: diskTestCredential, SecretAccessKey: diskTestCredential, Region: "us-east-1"})
	if err != nil {
		t.Fatal(err)
	}
	export := httptest.NewServer(restartedHandler)
	defer export.Close()
	restartedSource, err := objectnode.NewHTTPReplicationSource(export.URL, "us-east-1", diskTestCredential, diskTestCredential)
	if err != nil {
		t.Fatal(err)
	}
	head, err := restartedSource.Head(t.Context())
	if err != nil || head.Role != "fenced" || head.Handoff == nil || *head.Handoff != grant {
		t.Fatal("restart lost exportable fence", head, err)
	}
	if _, err = objectnode.PromoteReplica(t.Context(), follower, restartedSource); err != nil {
		t.Fatal("authenticated handoff could not promote", err)
	}
}

func TestDiskNodeReplicationDisabled(t *testing.T) {
	_, h := diskFixture(t)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, signedInventoryRequest(t, http.MethodGet, "/?gregale-replication=status"))
	if w.Code != 409 || !strings.Contains(w.Body.String(), "ReplicationDisabled") {
		t.Fatal(w.Code, w.Body.String())
	}
}
