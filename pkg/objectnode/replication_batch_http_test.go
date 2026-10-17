package objectnode

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// ADR-1558: corrupt advertised pages cannot fall back to single-entry reads.
func TestReplicaBatchHTTPRejectsMalformedPageWithoutDowngrade(t *testing.T) {
	for _, kind := range []string{"too many", "too large", "noncanonical"} {
		t.Run(kind, func(t *testing.T) {
			primary, directory := replicationPair(t)
			putText(t, primary, "key", "data")
			head, err := primary.ReplicationStatus()
			if err != nil {
				t.Fatal(err)
			}
			status, err := json.Marshal(head)
			if err != nil {
				t.Fatal(err)
			}
			body, want := "[] ", ErrCorrupt
			switch kind {
			case "too many":
				body, want = "["+strings.Repeat("{},", api.ObjectNodeReplicationBatch)+"{}]", ErrCapacity
			case "too large":
				body, want = strings.Repeat(" ", api.ObjectNodeReplicationBatchMaxBytes+1), ErrCapacity
			}
			var singles atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Query().Get(ReplicationQuery) {
				case "status":
					w.Header().Set(ReplicationBatchHeader, "1")
					_, _ = w.Write(status)
				case "entries":
					_, _ = w.Write([]byte(body))
				default:
					singles.Add(1)
					w.WriteHeader(http.StatusBadRequest)
				}
			}))
			defer server.Close()
			source, err := NewHTTPReplicationSource(server.URL, "us-east-1", "profile-test", "profile-test")
			if err != nil {
				t.Fatal(err)
			}
			p, err := CatchUpReplica(t.Context(), directory, source, pacedTestOptions())
			if !errors.Is(err, want) || p.Local.AppliedSequence != 0 || p.Local.ReceivedSequence != 0 || p.ConfirmedSequence != nil || singles.Load() != 0 || source.EntryBatchLimit() != api.ObjectNodeReplicationBatch {
				t.Fatal("corrupt page changed durability or hid behind single-entry reads", p, singles.Load(), err)
			}
		})
	}
}
