package realtimepush

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"

	"golang.org/x/oauth2"
)

// Provider tokens are reused across sends (ADR-1323, amendment of
// 2026-11-03). APNs throttles a key whose provider token changes more often
// than every 20 minutes (429 TooManyProviderTokenUpdates) and accepts one for
// an hour, so a token is reused for apnsTokenReuse. An FCM access token is
// reused until shortly before it expires instead of being fetched from
// Google's token endpoint for every send. A provider's 401 or 403 drops the
// cached token, so the next attempt makes a new one.
const (
	apnsTokenReuse      = 30 * time.Minute
	tokenCacheMaxKeys   = 4096
	fcmTokenEarlyExpiry = time.Minute
)

// TokenCache holds provider tokens for Send. The zero value is ready; a nil
// cache makes a token for every send.
type TokenCache struct {
	mu   sync.Mutex
	now  func() time.Time
	apns map[string]apnsToken
	fcm  map[string]*oauth2.Token
}

type apnsToken struct {
	value  string
	issued time.Time
}

func (tc *TokenCache) clock() time.Time {
	if tc != nil && tc.now != nil {
		return tc.now()
	}
	return time.Now()
}

func tokenKey(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func apnsKey(c Config) string { return tokenKey("apns", c.TeamID, c.KeyID, c.PrivateKey) }
func fcmKey(c Config) string  { return tokenKey("fcm", string(c.ServiceAccountJSON)) }

// apnsToken returns a provider token for c, signing one when none is cached
// or the cached one is older than apnsTokenReuse.
func (tc *TokenCache) apnsToken(c Config, sign func(time.Time) (string, error)) (string, error) {
	now := tc.clock()
	if tc == nil {
		return sign(now)
	}
	key := apnsKey(c)
	tc.mu.Lock()
	if hit, ok := tc.apns[key]; ok && now.Sub(hit.issued) < apnsTokenReuse && !now.Before(hit.issued) {
		tc.mu.Unlock()
		return hit.value, nil
	}
	tc.mu.Unlock()
	value, err := sign(now)
	if err != nil {
		return "", err
	}
	tc.mu.Lock()
	if tc.apns == nil || len(tc.apns) >= tokenCacheMaxKeys {
		tc.apns = map[string]apnsToken{}
	}
	tc.apns[key] = apnsToken{value: value, issued: now}
	tc.mu.Unlock()
	return value, nil
}

// fcmToken returns an access token for c, fetching one when none is cached
// or the cached one is about to expire.
func (tc *TokenCache) fcmToken(ctx context.Context, c Config, fetch func(context.Context) (*oauth2.Token, error)) (*oauth2.Token, error) {
	if tc == nil {
		return fetch(ctx)
	}
	key := fcmKey(c)
	now := tc.clock()
	tc.mu.Lock()
	if hit, ok := tc.fcm[key]; ok && hit.AccessToken != "" && (hit.Expiry.IsZero() || now.Add(fcmTokenEarlyExpiry).Before(hit.Expiry)) {
		tc.mu.Unlock()
		return hit, nil
	}
	tc.mu.Unlock()
	token, err := fetch(ctx)
	if err != nil {
		return nil, err
	}
	tc.mu.Lock()
	if tc.fcm == nil || len(tc.fcm) >= tokenCacheMaxKeys {
		tc.fcm = map[string]*oauth2.Token{}
	}
	tc.fcm[key] = token
	tc.mu.Unlock()
	return token, nil
}

// forget drops c's cached token after the provider refused it.
func (tc *TokenCache) forget(c Config) {
	if tc == nil {
		return
	}
	tc.mu.Lock()
	defer tc.mu.Unlock()
	switch c.Provider {
	case "apns":
		delete(tc.apns, apnsKey(c))
	case "fcm":
		delete(tc.fcm, fcmKey(c))
	}
}
