// adr: 20261105102920
package preview

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	faas "github.com/poyrazK/faas/sdk/go"
)

func TestEnvironmentRollbackReceiptSDKTransport(t *testing.T) {
	attempt, hash := strings.Repeat("a", 32), strings.Repeat("b", 64)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			if r.Header.Get(EnvironmentRollbackAttemptHeader) != attempt || r.Header.Get(EnvironmentRollbackHashHeader) != hash || r.Header.Get("Idempotency-Key") != "original" {
				t.Error("lost original write headers")
			}
			_, _ = w.Write([]byte(`{"promotion_id":"promotion"}`))
			return
		}
		if r.Header.Get(EnvironmentRollbackAttemptHeader) != "" || r.Header.Get(EnvironmentRollbackHashHeader) != "" || r.Header.Get("Idempotency-Key") != "" || !strings.HasSuffix(r.URL.Path, "/rollback-attempts/"+attempt) {
			t.Error("receipt GET carried mutation authority or wrong identity")
		}
		_, _ = w.Write([]byte(`{"attempt_id":"` + attempt + `","request_hash":"` + hash + `","promotion_id":"promotion"}`))
	}))
	defer backend.Close()
	client, err := NewClient(backend.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	ctx := ContextWithEnvironmentRollbackAttempt(faas.WithIdempotencyKey(context.Background(), "original"), attempt, hash)
	if _, err := client.RollbackProjectEnvironmentPromotion(ctx, "shop", "production", "promotion"); err != nil {
		t.Fatal(err)
	}
	if receipt, err := client.GetEnvironmentRollbackReceipt(ctx, "shop", "production", "promotion", attempt); err != nil || receipt.AttemptID != attempt {
		t.Fatal("receipt transport", err)
	}
}

func TestEnvironmentRollbackFingerprintCanonicalIDs(t *testing.T) {
	hash := EnvironmentRollbackRequestHash("e377519c-cbca-4dfa-a3e7-26b1321e0de4", "8677f406-6b97-4b24-bf26-cf795e040713", "142b7504-f03a-4ee2-aeb3-14d922a845d4", "3e9f323a-ade6-442b-8444-c91da107fe44", strings.Repeat("a", 64))
	if hash != "1f9139812c6c2fd9f478f4d9b7e212dcf5818b7fd1b3e6590a77b02f3a96e620" {
		t.Fatalf("original binding hash=%s", hash)
	}
	compact := EnvironmentRollbackRequestHash("e377519ccbca4dfaa3e726b1321e0de4", "8677f406-6b97-4b24-bf26-cf795e040713", "142b7504-f03a-4ee2-aeb3-14d922a845d4", "3e9f323a-ade6-442b-8444-c91da107fe44", strings.Repeat("a", 64))
	if compact != hash {
		t.Fatal("UUID spelling changed the original binding")
	}
}
