package objectstorage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/objectrecovery"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestOwnedSettlementValidatesSignedSingleAttemptAcknowledgement(t *testing.T) {
	for _, mode := range []string{"ok", "lost response", "wrong id", "wrong status", "missing time", "future time", "oversized", "unknown field", "trailing JSON", "redirect", "unauthorized"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int64
			a := objectrecovery.Settlement{ID: NewWriteReceiptID(), Bucket: "bucket", Key: "key", Kind: "put", Status: "committed", Size: 4, ETag: `"etag"`}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				digest := sha256.Sum256(body)
				var received objectrecovery.Settlement
				if r.Method != http.MethodPost || r.URL.Path != "/" || r.URL.RawQuery != "gregale-recovery=settle" || r.Header.Get("Content-Type") != "application/json" || !strings.Contains(r.Header.Get("Authorization"), "content-type") || r.Header.Get("X-Amz-Content-Sha256") != hex.EncodeToString(digest[:]) || json.Unmarshal(body, &received) != nil || received != a {
					t.Error("settlement request lost its identity or signed payload")
				}
				ack := objectrecovery.Acknowledgement{Version: 1, ID: a.ID, Status: a.Status, SettledAt: time.Now().UTC()}
				switch mode {
				case "lost response":
					w.WriteHeader(503)
					return
				case "redirect":
					w.Header().Set("Location", "/elsewhere")
					w.WriteHeader(307)
					return
				case "unauthorized":
					w.WriteHeader(403)
					return
				case "wrong id":
					ack.ID = NewWriteReceiptID()
				case "wrong status":
					ack.Status = "rejected"
				case "missing time":
					ack.SettledAt = time.Time{}
				case "future time":
					ack.SettledAt = time.Now().Add(time.Hour)
				}
				w.Header().Set("Content-Type", "application/json")
				reply, _ := json.Marshal(ack)
				if mode == "oversized" {
					reply = []byte(strings.Repeat(" ", 8192))
				} else if mode == "unknown field" {
					reply = append(reply[:len(reply)-1], []byte(`,"other":true}`)...)
				} else if mode == "trailing JSON" {
					reply = append(reply, []byte(`{}`)...)
				}
				_, _ = w.Write(reply)
			}))
			defer server.Close()
			config := testBackend()
			config.Endpoint, config.OwnedNodeReceipts, config.OwnedNodeSettlements = server.URL, true, true
			provider, err := NewS3(config, testCredentials)
			if err != nil {
				t.Fatal(err)
			}
			err = provider.(*S3).AcknowledgeOwnedSettlement(t.Context(), a)
			if calls.Load() != 1 || mode == "ok" && err != nil || mode != "ok" && err == nil {
				t.Fatal("invalid acknowledgement or automatic retry", mode, calls.Load(), err)
			}
		})
	}
}

func TestOwnedSettlementRequiresDurablySettledGatewayRecord(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(503)
	}))
	defer server.Close()
	config := testBackend()
	config.Endpoint, config.OwnedNodeReceipts, config.OwnedNodeSettlements = server.URL, true, true
	provider, err := NewS3(config, testCredentials)
	if err != nil {
		t.Fatal(err)
	}
	c := state.ObjectUploadCompletion{ID: NewWriteReceiptID(), Key: "key", Bytes: 4, Status: "completed", ETag: `"etag"`, WritePhase: state.ObjectUploadDispatched}
	if err = AcknowledgeSettledUpload(t.Context(), provider, "bucket", c); !errors.Is(err, ErrInvalid) || calls.Load() != 0 {
		t.Fatal("provider acknowledgement preceded gateway settlement", err, calls.Load())
	}
	c.WritePhase = state.ObjectUploadSettled
	if err = AcknowledgeSettledUpload(t.Context(), provider, "bucket", c); !errors.Is(err, ErrUnavailable) || calls.Load() != 1 || c.Status != "completed" {
		t.Fatal("delivery failure changed settlement", c, err, calls.Load())
	}
	config.OwnedNodeSettlements = false
	older, err := NewS3(config, testCredentials)
	if err != nil {
		t.Fatal(err)
	}
	if err = AcknowledgeSettledUpload(context.Background(), older, "bucket", c); err != nil || calls.Load() != 1 {
		t.Fatal("older node configuration received an extension request", err, calls.Load())
	}
	config.OwnedNodeReceipts = false
	managed, err := NewS3(config, testCredentials)
	if err != nil {
		t.Fatal(err)
	}
	if err = AcknowledgeSettledUpload(t.Context(), managed, "bucket", c); err != nil || calls.Load() != 1 {
		t.Fatal("managed S3 received a private settlement", err, calls.Load())
	}
}
