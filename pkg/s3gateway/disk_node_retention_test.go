package s3gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/objectnode"
)

func TestDiskNodeReplicationFollowerCursorAuthenticatesAndReseeds(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "node")
	store, err := objectnode.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.CreateBucket("test-bucket"); err != nil {
		t.Fatal(err)
	}
	_ = store.Close()
	head, err := objectnode.EnableReplication(t.Context(), directory)
	if err != nil {
		t.Fatal(err)
	}
	replica := uuid.NewString()
	head, err = objectnode.RegisterFollower(t.Context(), directory, replica, head.AppliedSequence, head.AppliedHash)
	if err != nil {
		t.Fatal(err)
	}
	store, err = objectnode.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	h, err := NewDiskHandler(DiskConfig{Store: store, AccessKeyID: diskTestCredential, SecretAccessKey: diskTestCredential, Region: "us-east-1"})
	if err != nil {
		t.Fatal(err)
	}
	o, err := store.Put(t.Context(), "test-bucket", "key", strings.NewReader("payload"), 7, objectnode.PutOptions{})
	if err != nil {
		t.Fatal(err)
	}
	head, _ = store.ReplicationStatus()
	report := objectnode.FollowerReport{Version: 1, StreamID: head.StreamID, PrimaryID: head.PrimaryID, Epoch: head.Epoch, RegistrationID: head.Followers[0].RegistrationID, ReplicaID: replica, Sequence: head.AppliedSequence, Hash: head.AppliedHash}
	body, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"no signature", "tampered body", "duplicate field", "unknown field", "unknown selector", "unsigned payload", "wrong generation", "wrong hash"} {
		path, encoded := "/?gregale-replication=cursor", append([]byte(nil), body...)
		switch mode {
		case "duplicate field":
			encoded = bytes.Replace(encoded, []byte(`"version":1`), []byte(`"version":1,"version":1`), 1)
		case "unknown field":
			encoded = bytes.Replace(encoded, []byte(`"version":1`), []byte(`"version":1,"extra":true`), 1)
		case "unknown selector":
			path += "&stream=other"
		case "wrong generation":
			encoded = bytes.ReplaceAll(encoded, []byte(report.RegistrationID), []byte(uuid.NewString()))
		case "wrong hash":
			encoded = bytes.ReplaceAll(encoded, []byte(report.Hash), []byte(strings.Repeat("0", 64)))
		}
		r := signedDurabilityRequest(t, t.Context(), http.MethodPost, path, encoded, http.Header{"Content-Type": {"application/json"}})
		switch mode {
		case "no signature":
			r.Header.Del("Authorization")
		case "tampered body":
			r.Body = io.NopCloser(bytes.NewReader(bytes.ReplaceAll(encoded, []byte(report.RegistrationID), []byte(uuid.NewString()))))
		case "unsigned payload":
			r.Header.Set("X-Amz-Content-Sha256", "UNSIGNED-PAYLOAD")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code < 400 {
			t.Fatal("invalid cursor was accepted", mode, w.Code)
		}
	}
	unchanged, _ := store.ReplicationStatus()
	if unchanged.Followers[0].Sequence != 0 {
		t.Fatal("invalid report advanced a pin", unchanged)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, signedDurabilityRequest(t, t.Context(), http.MethodPost, "/?gregale-replication=cursor", body, http.Header{"Content-Type": {"application/json"}}))
	if w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	_ = store.Close()
	result, err := objectnode.CompactJournal(t.Context(), directory, -1)
	if err != nil || result.Status.BaseSequence != 1 {
		t.Fatal(result, err)
	}
	store, err = objectnode.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	h, err = NewDiskHandler(DiskConfig{Store: store, AccessKeyID: diskTestCredential, SecretAccessKey: diskTestCredential, Region: "us-east-1"})
	if err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, signedInventoryRequest(t, http.MethodGet, "/?gregale-replication=entry&stream="+head.StreamID+"&sequence=1"))
	if w.Code != http.StatusGone || !strings.Contains(w.Body.String(), "ReplicaReseedRequired") || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("retired prefix did not return a clear reseed error", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, signedInventoryRequest(t, http.MethodGet, "/test-bucket/key"))
	if w.Code != http.StatusOK || w.Body.String() != "payload" || w.Header().Get("ETag") != o.ETag {
		t.Fatal("S3 read changed after compaction", w.Code, w.Header(), w.Body.String())
	}
}
