package realtimepush

import (
	"context"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

// adr: 1323 (amendment of 2026-11-03) — provider tokens are reused: an APNs
// provider token for 30 minutes, an FCM access token until shortly before it
// expires, and a refused token is dropped.
func TestTokenCacheReusesProviderTokens(t *testing.T) {
	now := time.Unix(10_000, 0)
	tc := &TokenCache{now: func() time.Time { return now }}
	apns := Config{Provider: "apns", TeamID: "TEAM000001", KeyID: "KEY0000001", PrivateKey: "pem"}
	signs := 0
	sign := func(issued time.Time) (string, error) {
		signs++
		return issued.String(), nil
	}
	first, _ := tc.apnsToken(apns, sign)
	now = now.Add(29 * time.Minute)
	if again, _ := tc.apnsToken(apns, sign); again != first || signs != 1 {
		t.Fatalf("APNs token within 30 minutes re-signed (%d signs)", signs)
	}
	now = now.Add(2 * time.Minute)
	if _, _ = tc.apnsToken(apns, sign); signs != 2 {
		t.Fatalf("APNs token after 30 minutes was not re-signed (%d signs)", signs)
	}
	tc.forget(apns)
	if _, _ = tc.apnsToken(apns, sign); signs != 3 {
		t.Fatalf("a refused APNs token was reused (%d signs)", signs)
	}

	fcm := Config{Provider: "fcm", ServiceAccountJSON: []byte(`{"client_email":"a@b"}`)}
	fetches := 0
	fetch := func(context.Context) (*oauth2.Token, error) {
		fetches++
		return &oauth2.Token{AccessToken: "token", Expiry: now.Add(time.Hour)}, nil
	}
	ctx := context.Background()
	_, _ = tc.fcmToken(ctx, fcm, fetch)
	now = now.Add(58 * time.Minute)
	if _, _ = tc.fcmToken(ctx, fcm, fetch); fetches != 1 {
		t.Fatalf("a valid FCM token was fetched again (%d fetches)", fetches)
	}
	now = now.Add(90 * time.Second)
	if _, _ = tc.fcmToken(ctx, fcm, fetch); fetches != 2 {
		t.Fatalf("an FCM token about to expire was reused (%d fetches)", fetches)
	}

	var none *TokenCache
	if _, _ = none.apnsToken(apns, sign); signs != 4 {
		t.Fatal("a nil cache did not sign per send")
	}
}
