package s3gateway

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectnode"
	"github.com/onebox-faas/faas/pkg/objectstorage"
)

func replicaDiskFixture(t *testing.T) (*objectnode.Store, *DiskHandler, string, *httptest.Server, *objectnode.HTTPReplicationSource) {
	t.Helper()
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
	backup, follower := filepath.Join(t.TempDir(), "seed"), filepath.Join(t.TempDir(), "follower")
	if _, err = objectnode.Backup(t.Context(), dir, backup); err != nil {
		t.Fatal(err)
	}
	if _, err = objectnode.BootstrapReplica(t.Context(), backup, follower); err != nil {
		t.Fatal(err)
	}
	local, err := objectnode.ReadReplicationStatus(follower)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = objectnode.ConfigureDurability(t.Context(), dir, objectnode.DurabilityReplica, local.NodeID); err != nil {
		t.Fatal(err)
	}
	store, err = objectnode.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	h, err := NewDiskHandler(DiskConfig{Store: store, AccessKeyID: diskTestCredential, SecretAccessKey: diskTestCredential, Region: "us-east-1", MinFreeBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)
	source, err := objectnode.NewHTTPReplicationSource(server.URL, "us-east-1", diskTestCredential, diskTestCredential)
	if err != nil {
		t.Fatal(err)
	}
	return store, h, follower, server, source
}

func confirmDiskReplica(t *testing.T, follower string, source objectnode.ReplicationSource) objectnode.ReplicationProgress {
	t.Helper()
	for range 100 {
		progress, err := objectnode.CatchUpReplica(t.Context(), follower, source, objectnode.CatchUpOptions{MaxEntries: api.ObjectNodeReplicationBatch, MaxBytes: api.ObjectNodeReplicationStepBytes})
		if err != nil {
			t.Fatal(err)
		}
		if progress.CaughtUp {
			return progress
		}
	}
	t.Fatal("replica did not confirm the final position")
	return objectnode.ReplicationProgress{}
}

func signedDurabilityRequest(t *testing.T, ctx context.Context, method, path string, body []byte, headers http.Header) *http.Request {
	t.Helper()
	r := httptest.NewRequest(method, "http://node.test"+path, bytes.NewReader(body)).WithContext(ctx)
	r.Header = headers.Clone()
	if r.Header == nil {
		r.Header = make(http.Header)
	}
	digest := sha256.Sum256(body)
	payload := hex.EncodeToString(digest[:])
	r.Header.Set("X-Amz-Content-Sha256", payload)
	if err := v4.NewSigner().SignHTTP(ctx, aws.Credentials{AccessKeyID: diskTestCredential, SecretAccessKey: diskTestCredential}, r, payload, "s3", "us-east-1", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestDiskNodeReplicaSuccessWaitsForAppliedConfirmation(t *testing.T) {
	store, _, follower, server, source := replicaDiskFixture(t)
	id := uuid.NewString()
	done := make(chan error, 1)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	go func() {
		_, err := diskClient(server).PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String("test-bucket"), Key: aws.String("key"), Body: strings.NewReader("payload"), Metadata: map[string]string{"gregale-upload-receipt": id}})
		done <- err
	}()
	for {
		proof, err := store.Receipt("test-bucket", "key", id)
		if err == nil && proof.Status == objectnode.ReceiptCommitted {
			break
		}
		select {
		case err := <-done:
			t.Fatal("request returned before local publication", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(time.Millisecond):
		}
	}
	select {
	case err := <-done:
		t.Fatal("local commit was acknowledged before replication", err)
	default:
	}
	partial, err := objectnode.CatchUpReplica(t.Context(), follower, source, objectnode.CatchUpOptions{MaxEntries: 1, MaxBytes: 1})
	if err != nil || partial.Local.ReceivedSequence <= partial.Local.AppliedSequence {
		t.Fatal(partial, err)
	}
	select {
	case err := <-done:
		t.Fatal("received bytes released a response", err)
	default:
	}
	progress := confirmDiskReplica(t, follower, source)
	if progress.ConfirmedSequence == nil || *progress.ConfirmedSequence != progress.Local.AppliedSequence {
		t.Fatal(progress)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("confirmation did not release the response")
	}
}

func TestDiskNodeReplicaTimeoutKeepsReceiptAndInventoryUncertain(t *testing.T) {
	store, h, follower, _, source := replicaDiskFixture(t)
	id := uuid.NewString()
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, signedDurabilityRequest(t, ctx, http.MethodPut, "/test-bucket/key", []byte("original"), http.Header{objectnode.ReceiptMetadataHeader: {id}}))
	if w.Code != 503 || !strings.Contains(w.Body.String(), "ReplicaDurabilityPending") || w.Header().Get("ETag") != "" {
		t.Fatal(w.Code, w.Header(), w.Body.String())
	}
	original, err := store.Receipt("test-bucket", "key", id)
	if err != nil || original.Status != objectnode.ReceiptCommitted {
		t.Fatal("local commit was rolled back", original, err)
	}
	for _, query := range []struct {
		path    string
		headers http.Header
		method  string
	}{
		{"/test-bucket/key", http.Header{objectstorage.OwnedNodeReceiptHeader: {id}}, http.MethodHead},
		{"/test-bucket?gregale-inventory=objects", nil, http.MethodGet},
		{"/test-bucket/key", nil, http.MethodGet},
	} {
		probe, stop := context.WithTimeout(t.Context(), 200*time.Millisecond)
		w = httptest.NewRecorder()
		h.ServeHTTP(w, signedDurabilityRequest(t, probe, query.method, query.path, nil, query.headers))
		stop()
		if w.Code != 503 || w.Header().Get("ETag") != "" || w.Header().Get("Content-Length") != "" || w.Header().Get(objectstorage.OwnedNodeReceiptStatusHeader) != "" || bytes.Contains(w.Body.Bytes(), []byte("original")) {
			t.Fatal("unconfirmed data/proof escaped", query.path, w.Code, w.Header(), w.Body.String())
		}
	}
	if _, err = store.Put(t.Context(), "test-bucket", "key", strings.NewReader("later"), 5, objectnode.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	confirmDiskReplica(t, follower, source)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, signedDurabilityRequest(t, t.Context(), http.MethodHead, "/test-bucket/key", nil, http.Header{objectstorage.OwnedNodeReceiptHeader: {id}}))
	if w.Code != 200 || w.Header().Get("ETag") != original.Object.ETag || w.Header().Get(objectstorage.OwnedNodeReceiptStatusHeader) != objectnode.ReceiptCommitted {
		t.Fatal("confirmed historical receipt was lost", w.Code, w.Header(), w.Body.String())
	}
	metrics := httptest.NewRecorder()
	h.MetricsHandler().ServeHTTP(metrics, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	for _, metric := range []string{"faas_object_node_replica_required 1", "faas_object_node_replica_lag_records 0", "faas_object_node_replica_caught_up 1", "faas_object_node_replica_waiting_responses 0"} {
		if !strings.Contains(metrics.Body.String(), metric) {
			t.Fatal("missing durability metric", metric, metrics.Body.String())
		}
	}
	if strings.Contains(metrics.Body.String(), id) {
		t.Fatal("receipt identity leaked into metrics")
	}
}

func TestDiskNodeReplicaNegativeReceiptWaitsForConfirmation(t *testing.T) {
	store, h, follower, _, source := replicaDiskFixture(t)
	if _, err := store.Put(t.Context(), "test-bucket", "key", strings.NewReader("prior"), 5, objectnode.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, signedDurabilityRequest(t, ctx, http.MethodPut, "/test-bucket/key", []byte("rejected"), http.Header{objectnode.ReceiptMetadataHeader: {id}, "If-None-Match": {"*"}}))
	if w.Code != 503 {
		t.Fatal("unreplicated negative receipt was definitive", w.Code, w.Body.String())
	}
	proof, err := store.Receipt("test-bucket", "key", id)
	if err != nil || proof.Status != objectnode.ReceiptRejected {
		t.Fatal(proof, err)
	}
	confirmDiskReplica(t, follower, source)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, signedDurabilityRequest(t, t.Context(), http.MethodHead, "/test-bucket/key", nil, http.Header{objectstorage.OwnedNodeReceiptHeader: {id}}))
	if w.Code != 200 || w.Header().Get(objectstorage.OwnedNodeReceiptStatusHeader) != objectnode.ReceiptRejected {
		t.Fatal(w.Code, w.Header(), w.Body.String())
	}
}

func TestDiskNodeReplicaConfirmationAuthenticatesBodyAndPolicy(t *testing.T) {
	store, h, follower, server, source := replicaDiskFixture(t)
	local, err := objectnode.ReadReplicationStatus(follower)
	if err != nil {
		t.Fatal(err)
	}
	head, err := source.Head(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	confirmation := objectnode.ReplicaConfirmation{Version: 1, StreamID: head.StreamID, PrimaryID: head.PrimaryID, Epoch: head.Epoch, PolicyID: head.Durability.PolicyID, ReplicaID: local.NodeID, Sequence: head.AppliedSequence, Hash: head.AppliedHash}
	wrong, _ := objectnode.NewHTTPReplicationSource(server.URL, "us-east-1", diskTestCredential, "wrong-secret")
	if _, err = wrong.Confirm(t.Context(), confirmation); err == nil {
		t.Fatal("unsigned confirmation accepted")
	}
	body, err := json.Marshal(confirmation)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"tampered body", "duplicate field", "unknown selector", "unsigned payload"} {
		path, encoded := "/?gregale-replication=confirm", append([]byte(nil), body...)
		if mode == "duplicate field" {
			encoded = bytes.Replace(encoded, []byte(`"version":1`), []byte(`"version":1,"version":1`), 1)
		}
		if mode == "unknown selector" {
			path += "&stream=other"
		}
		r := signedDurabilityRequest(t, t.Context(), http.MethodPost, path, encoded, http.Header{"Content-Type": {"application/json"}})
		if mode == "tampered body" {
			r.Body = io.NopCloser(strings.NewReader(strings.Replace(string(encoded), head.Durability.PolicyID, uuid.NewString(), 1)))
		}
		if mode == "unsigned payload" {
			r.Header.Set("X-Amz-Content-Sha256", "UNSIGNED-PAYLOAD")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code < 400 {
			t.Fatal("invalid confirmation accepted", mode, w.Code)
		}
	}
	p, _, err := store.DurabilityStatus()
	if err != nil || p.ConfirmedSequence != -1 {
		t.Fatal("rejected reports advanced durability", p, err)
	}
	if _, err = source.Confirm(t.Context(), confirmation); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err = store.WaitForReplica(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestDiskNodeReplicaMultipartCompletionProofWaits(t *testing.T) {
	store, h, follower, _, source := replicaDiskFixture(t)
	u, err := store.CreateMultipart("test-bucket", "assembled", objectnode.MultipartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	part, err := store.UploadPart(t.Context(), u.Bucket, u.Key, u.ID, 1, strings.NewReader("selected"), 8, objectnode.PutOptions{})
	if err != nil {
		t.Fatal(err)
	}
	manifest := []byte(`<CompleteMultipartUpload><Part><PartNumber>1</PartNumber><ETag>` + part.Object.ETag + `</ETag></Part></CompleteMultipartUpload>`)
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, signedDurabilityRequest(t, ctx, http.MethodPost, "/test-bucket/assembled?uploadId="+u.ID, manifest, nil))
	if w.Code != 503 {
		t.Fatal("unreplicated completion acknowledged", w.Code, w.Body.String())
	}
	proof, err := store.MultipartReceipt(u.Bucket, u.Key, u.ReceiptID)
	if err != nil || proof.Status != objectnode.ReceiptCommitted {
		t.Fatal(proof, err)
	}
	probe, stop := context.WithTimeout(t.Context(), 200*time.Millisecond)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, signedDurabilityRequest(t, probe, http.MethodHead, "/test-bucket/assembled", nil, http.Header{objectstorage.OwnedNodeMultipartReceiptHeader: {u.ReceiptID}}))
	stop()
	if w.Code != 503 || w.Header().Get(objectstorage.OwnedNodeReceiptStatusHeader) != "" {
		t.Fatal("local completion proof escaped", w.Code, w.Header())
	}
	confirmDiskReplica(t, follower, source)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, signedDurabilityRequest(t, t.Context(), http.MethodHead, "/test-bucket/assembled", nil, http.Header{objectstorage.OwnedNodeMultipartReceiptHeader: {u.ReceiptID}}))
	if w.Code != 200 || w.Header().Get("ETag") != proof.Object.ETag || w.Header().Get(objectstorage.OwnedNodeMultipartManifestHeader) != proof.ManifestHash {
		t.Fatal(w.Code, w.Header(), w.Body.String())
	}
	deleting, stopDelete := context.WithTimeout(t.Context(), 200*time.Millisecond)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, signedDurabilityRequest(t, deleting, http.MethodDelete, "/test-bucket/assembled", nil, nil))
	stopDelete()
	if w.Code != 503 {
		t.Fatal("unreplicated deletion acknowledged", w.Code, w.Body.String())
	}
	confirmDiskReplica(t, follower, source)
	if err = store.WaitForReplica(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestDiskNodeReplicaLostConfirmationResponseIsIdempotent(t *testing.T) {
	store, h, follower, _, _ := replicaDiskFixture(t)
	if _, err := store.Put(t.Context(), "test-bucket", "key", strings.NewReader("durable"), 7, objectnode.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	var drop atomic.Bool
	drop.Store(true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get(objectnode.ReplicationQuery) == "confirm" && drop.CompareAndSwap(true, false) {
			recorder := httptest.NewRecorder()
			h.ServeHTTP(recorder, r)
			if recorder.Code != 200 {
				t.Error("confirmation did not persist", recorder.Code, recorder.Body.String())
			}
			connection, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = connection.Close()
			return
		}
		h.ServeHTTP(w, r)
	}))
	defer server.Close()
	source, err := objectnode.NewHTTPReplicationSource(server.URL, "us-east-1", diskTestCredential, diskTestCredential)
	if err != nil {
		t.Fatal(err)
	}
	progress, err := objectnode.CatchUpReplica(t.Context(), follower, source, objectnode.CatchUpOptions{MaxEntries: api.ObjectNodeReplicationBatch, MaxBytes: api.ObjectNodeReplicationStepBytes})
	if err == nil || progress.Local.AppliedSequence != 1 {
		t.Fatal("lost response did not preserve applied checkpoint", progress, err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err = store.WaitForReplica(ctx); err != nil {
		t.Fatal("durable confirmation was forgotten", err)
	}
	retry := confirmDiskReplica(t, follower, source)
	if retry.EntriesApplied != 0 || retry.BytesTransferred != 0 || retry.ConfirmedSequence == nil || *retry.ConfirmedSequence != 1 {
		t.Fatal("confirmation retry replayed data", retry)
	}
}
