// adr: 20261105153007
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEnvironmentPromotionReceiptTransport(t *testing.T) {
	for _, company := range []bool{false, true} {
		for _, explicit := range []bool{false, true} {
			attempt, hash := strings.Repeat("a", 32), strings.Repeat("b", 64)
			base := "/v1/projects/shop/environments/production"
			if company {
				base = "/v1/orgs/team/projects/shop/environments/production"
			}
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "POST" {
					if r.URL.Path != base+"/promote" || r.Header.Get(EnvironmentPromotionAttemptHeader) != attempt || r.Header.Get(EnvironmentPromotionHashHeader) != hash || r.Header.Get("Idempotency-Key") != "original" {
						t.Error("lost exact write headers")
					}
					var body PromoteProjectEnvironmentRequest
					if json.NewDecoder(r.Body).Decode(&body) != nil || body.ApprovalToken != "private-approval" {
						t.Error("lost reviewed body")
					}
					_, _ = w.Write([]byte(`{"promotion_id":"promotion"}`))
					return
				}
				if r.Method != "GET" || r.URL.Path != base+"/promotion-attempts/"+attempt || r.Header.Get(EnvironmentPromotionAttemptHeader) != "" || r.Header.Get(EnvironmentPromotionHashHeader) != "" || r.Header.Get("Idempotency-Key") != "" {
					t.Error("receipt read carried mutation authority or changed selectors")
				}
				_, _ = w.Write([]byte(`{"attempt_id":"` + attempt + `","request_hash":"` + hash + `","promotion_id":"promotion"}`))
			}))
			client := NewClient(backend.URL, "fixture")
			ctx := ContextWithEnvironmentPromotionAttempt(ContextWithIdempotencyKey(t.Context(), "original"), attempt, hash)
			body := PromoteProjectEnvironmentRequest{FromEnvironment: "staging", PromotionToken: "private-preview", ApprovalToken: "private-approval"}
			var err error
			switch {
			case company && explicit:
				_, err = client.PromoteOrgProjectEnvironmentWithIdempotencyKey(ctx, "team", "shop", "production", body, "original")
			case company:
				_, err = client.PromoteOrgProjectEnvironment(ctx, "team", "shop", "production", body)
			case explicit:
				_, err = client.PromoteProjectEnvironmentWithIdempotencyKey(ctx, "shop", "production", body, "original")
			default:
				_, err = client.PromoteProjectEnvironment(ctx, "shop", "production", body)
			}
			if err != nil {
				t.Fatal(err)
			}
			var receipt EnvironmentPromotionReceipt
			if company {
				receipt, err = client.GetOrgEnvironmentPromotionReceipt(ctx, "team", "shop", "production", attempt)
			} else {
				receipt, err = client.GetEnvironmentPromotionReceipt(ctx, "shop", "production", attempt)
			}
			if err != nil || receipt.AttemptID != attempt {
				t.Fatal("receipt", err)
			}
			backend.Close()
		}
	}
}
func TestEnvironmentPromotionFingerprintCanonicalIDs(t *testing.T) {
	hash := EnvironmentPromotionRequestHash("e377519c-cbca-4dfa-a3e7-26b1321e0de4", "8677f406-6b97-4b24-bf26-cf795e040713", "142b7504-f03a-4ee2-aeb3-14d922a845d4", strings.Repeat("a", 64))
	if hash != "a9a7a44d6fc73a30bf5a548dd3645c5b9158845b2ed331738b87526291166cab" {
		t.Fatal(hash)
	}
	if EnvironmentPromotionRequestHash("E377519CCBCA4DFAA3E726B1321E0DE4", "8677f406-6b97-4b24-bf26-cf795e040713", "142b7504-f03a-4ee2-aeb3-14d922a845d4", strings.Repeat("a", 64)) != hash {
		t.Fatal("UUID spelling changed original binding")
	}
}
