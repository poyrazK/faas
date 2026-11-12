// adr: 20261105153007
package preview

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	faas "github.com/poyrazK/faas/sdk/go"
)

func TestEnvironmentPromotionReceiptSDKTransport(t *testing.T) {
	attempt, hash := strings.Repeat("a", 32), strings.Repeat("b", 64)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			if r.Header.Get(EnvironmentPromotionAttemptHeader) != attempt || r.Header.Get(EnvironmentPromotionHashHeader) != hash || r.Header.Get("Idempotency-Key") != "original" {
				t.Error("lost original write headers")
			}
			_, _ = w.Write([]byte(`{"promotion_id":"promotion"}`))
			return
		}
		if r.Header.Get(EnvironmentPromotionAttemptHeader) != "" || r.Header.Get(EnvironmentPromotionHashHeader) != "" || r.Header.Get("Idempotency-Key") != "" || !strings.HasSuffix(r.URL.Path, "/promotion-attempts/"+attempt) {
			t.Error("receipt GET carried mutation authority or wrong identity")
		}
		_, _ = w.Write([]byte(`{"attempt_id":"` + attempt + `","request_hash":"` + hash + `","promotion_id":"promotion"}`))
	}))
	defer backend.Close()
	client, err := NewClient(backend.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	ctx := ContextWithEnvironmentPromotionAttempt(faas.WithIdempotencyKey(context.Background(), "original"), attempt, hash)
	if _, err := client.PromoteProjectEnvironment(ctx, "shop", "production", PromoteProjectEnvironmentRequest{FromEnvironment: "staging", PromotionToken: "private-preview"}); err != nil {
		t.Fatal(err)
	}
	if receipt, err := client.GetEnvironmentPromotionReceipt(ctx, "shop", "production", attempt); err != nil || receipt.AttemptID != attempt {
		t.Fatal("receipt transport", err)
	}
}

func TestEnvironmentPromotionFingerprintCanonicalIDs(t *testing.T) {
	hash := EnvironmentPromotionRequestHash("e377519c-cbca-4dfa-a3e7-26b1321e0de4", "8677f406-6b97-4b24-bf26-cf795e040713", "142b7504-f03a-4ee2-aeb3-14d922a845d4", strings.Repeat("a", 64))
	if hash != "a9a7a44d6fc73a30bf5a548dd3645c5b9158845b2ed331738b87526291166cab" {
		t.Fatalf("original binding hash=%s", hash)
	}
	compact := EnvironmentPromotionRequestHash("e377519ccbca4dfaa3e726b1321e0de4", "8677f406-6b97-4b24-bf26-cf795e040713", "142b7504-f03a-4ee2-aeb3-14d922a845d4", strings.Repeat("a", 64))
	if compact != hash {
		t.Fatal("UUID spelling changed the original binding")
	}
}
