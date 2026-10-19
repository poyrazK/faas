package s3gateway

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectnode"
)

func diskWaitCursor(t *testing.T, source *objectnode.HTTPReplicationSource) objectnode.ReplicationWaitCursor {
	t.Helper()
	head, err := source.Head(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	cursor, err := objectnode.NewReplicationWaitCursor(head)
	if err != nil {
		t.Fatal(err)
	}
	return cursor
}

func diskWaitQuery(c objectnode.ReplicationWaitCursor) url.Values {
	return url.Values{objectnode.ReplicationQuery: {"wait"}, "stream": {c.StreamID}, "primary": {c.PrimaryID}, "epoch": {strconv.FormatInt(c.Epoch, 10)}, "sequence": {strconv.FormatInt(c.Sequence, 10)}, "hash": {c.Hash}, "revision": {c.Revision}}
}

func awaitDiskWaiters(t *testing.T, h *DiskHandler, count int) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for len(h.metrics.replication.slots) != count {
		select {
		case <-deadline.C:
			t.Fatalf("wait slots: got %d, want %d", len(h.metrics.replication.slots), count)
		case <-tick.C:
		}
	}
}

func TestDiskReplicationWaitAuthenticatedBoundedAndCanceled(t *testing.T) {
	store, h, _, server, source := replicaDiskFixture(t)
	cursor := diskWaitCursor(t, source)
	wrong, err := objectnode.NewHTTPReplicationSource(server.URL, "us-east-1", diskTestCredential, "wrong-secret")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = wrong.WaitForChange(t.Context(), cursor); !errors.Is(err, objectnode.ErrInvalid) {
		t.Fatal("unsigned wait was accepted", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		head, err := source.WaitForChange(ctx, cursor)
		if err == nil && (head.AppliedSequence != 1 || head.Durability.ConfirmedSequence != -1) {
			err = fmt.Errorf("change notification claimed durability: %+v", head)
		}
		done <- err
	}()
	awaitDiskWaiters(t, h, 1)
	if _, err = store.Put(t.Context(), "test-bucket", "wake", strings.NewReader("payload"), 7, objectnode.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	cursor = diskWaitCursor(t, source)
	go func() { _, err := source.WaitForChange(ctx, cursor); done <- err }()
	awaitDiskWaiters(t, h, 1)
	cancel()
	if err = <-done; !errors.Is(err, context.Canceled) {
		t.Fatal("cancel did not release the HTTP wait", err)
	}
	awaitDiskWaiters(t, h, 0)
	metrics := httptest.NewRecorder()
	h.MetricsHandler().ServeHTTP(metrics, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	for _, name := range []string{"faas_object_node_replication_waiting 0", "faas_object_node_replication_change_wait_seconds_count{outcome=\"changed\"} 1", "faas_object_node_replica_lag_records 1"} {
		if !strings.Contains(metrics.Body.String(), name) {
			t.Fatal("missing wait/lag observation", name, metrics.Body.String())
		}
	}
	if strings.Contains(metrics.Body.String(), cursor.StreamID) || strings.Contains(metrics.Body.String(), cursor.Hash) {
		t.Fatal("wait identities leaked into metric labels")
	}
}

func TestDiskReplicationWaitRejectsAmbiguousSelectors(t *testing.T) {
	_, h, _, _, source := replicaDiskFixture(t)
	cursor := diskWaitCursor(t, source)
	for _, field := range []string{"duplicate", "unknown", "sequence", "epoch", "stream", "primary", "hash", "revision", "missing"} {
		t.Run(field, func(t *testing.T) {
			q := diskWaitQuery(cursor)
			switch field {
			case "duplicate":
				q.Add("revision", cursor.Revision)
			case "unknown":
				q.Set("wait_seconds", "999999")
			case "sequence", "epoch":
				q.Set(field, "01")
			case "missing":
				q.Del("hash")
			default:
				q.Set(field, "bad")
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, signedInventoryRequest(t, http.MethodGet, "/?"+q.Encode()))
			if w.Code != http.StatusBadRequest || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal(w.Code, w.Header(), w.Body.String())
			}
		})
	}
	path := "/?" + diskWaitQuery(cursor).Encode()
	for _, header := range []string{"Range", "If-Match"} {
		r := signedInventoryRequest(t, http.MethodGet, path)
		r.Header.Set(header, "invalid")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code < 400 {
			t.Fatal("conditional wait accepted", header)
		}
	}
	for _, method := range []string{http.MethodHead, http.MethodPost, http.MethodPut} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, signedInventoryRequest(t, method, path))
		if w.Code < 400 {
			t.Fatal("wrong wait method accepted", method)
		}
	}
}

func TestDiskReplicationWaitAdmissionAndGracefulShutdown(t *testing.T) {
	_, h, _, server, source := replicaDiskFixture(t)
	server.Config.RegisterOnShutdown(h.CancelReplicationWaits)
	cursor := diskWaitCursor(t, source)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var workers sync.WaitGroup
	errorsSeen := make(chan error, api.ObjectNodeReplicationMaxWaiters)
	for range api.ObjectNodeReplicationMaxWaiters {
		workers.Go(func() { _, err := source.WaitForChange(ctx, cursor); errorsSeen <- err })
	}
	awaitDiskWaiters(t, h, api.ObjectNodeReplicationMaxWaiters)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, signedInventoryRequest(t, http.MethodGet, "/?"+diskWaitQuery(cursor).Encode()))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatal("excess waiters were admitted", w.Code)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/?"+diskWaitQuery(cursor).Encode(), nil))
	if w.Code != http.StatusForbidden {
		t.Fatal("wait limit bypassed authentication", w.Code)
	}
	shutdown, stop := context.WithTimeout(t.Context(), 3*time.Second)
	defer stop()
	if err := server.Config.Shutdown(shutdown); err != nil {
		t.Fatal("idle waits blocked graceful shutdown", err)
	}
	workers.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		if err == nil {
			t.Fatal("shutdown was reported as new durable data")
		}
	}
	awaitDiskWaiters(t, h, 0)
}

func TestHTTPReplicationWaitRejectsSourceIdentityAndPositionChanges(t *testing.T) {
	store, _, _, _, source := replicaDiskFixture(t)
	if _, err := store.Put(t.Context(), "test-bucket", "key", strings.NewReader("body"), 4, objectnode.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	head, err := source.Head(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	cursor, err := objectnode.NewReplicationWaitCursor(head)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"stream", "primary", "hash", "regress", "follower", "invalid"} {
		t.Run(field, func(t *testing.T) {
			bad := head
			want := objectnode.ErrReplicationFork
			switch field {
			case "stream":
				bad.StreamID = uuid.NewString()
				bad.BaseHash = fmt.Sprintf("%x", sha256.Sum256([]byte("gregale-replication-v1:"+bad.StreamID)))
			case "primary":
				bad.PrimaryID = uuid.NewString()
				bad.NodeID = bad.PrimaryID
			case "hash":
				bad.AppliedHash, bad.ReceivedHash = strings.Repeat("0", 64), strings.Repeat("0", 64)
			case "regress":
				bad.AppliedSequence, bad.ReceivedSequence = 0, 0
				bad.AppliedHash, bad.ReceivedHash = bad.BaseHash, bad.BaseHash
			case "follower":
				bad.Role, bad.NodeID, bad.Version, bad.Durability = "follower", uuid.NewString(), 2, nil
			case "invalid":
				bad.Version, want = 999, objectnode.ErrCorrupt
			}
			fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				body, _ := json.Marshal(bad)
				_, _ = w.Write(body)
			}))
			defer fake.Close()
			client, err := objectnode.NewHTTPReplicationSource(fake.URL, "us-east-1", diskTestCredential, diskTestCredential)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = client.WaitForChange(t.Context(), cursor); !errors.Is(err, want) {
				t.Fatal("untrusted wait response was accepted", field, err)
			}
		})
	}
}
