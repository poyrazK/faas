package referrals

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"strconv"
	"strings"
	"time"
)

// Touch tokens retain the original first-touch time across login and OAuth.
// They carry no personal data and are bound to this deployment's session key.
func SignTouch(key []byte, code string, at time.Time) string {
	payload := code + "." + strconv.FormatInt(at.Unix(), 10)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte("gregale-referral-v1:" + payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func VerifyTouch(key []byte, value string, now time.Time, ttl time.Duration) string {
	if len(key) < 32 || len(value) > 128 {
		return ""
	}
	parts := strings.Split(value, ".")
	if len(parts) != 3 || !ValidCode(parts[0]) {
		return ""
	}
	seconds, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return ""
	}
	at := time.Unix(seconds, 0)
	if at.After(now) || now.Sub(at) >= ttl {
		return ""
	}
	expected := SignTouch(key, parts[0], at)
	if !hmac.Equal([]byte(expected), []byte(value)) {
		return ""
	}
	return parts[0]
}
