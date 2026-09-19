package gateway

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
)

const sessionAffinityCookieName = "gregale_affinity"

// affinityTargetFromRequest returns the authenticated instance hint carried by
// the app's cookie. Invalid, cross-app, or oversized cookies are ignored so a
// client can never steer traffic to an arbitrary instance.
func (h *Handler) affinityTargetFromRequest(r *http.Request, appID string) string {
	if h == nil || !h.sessionAffinityKeyValid || r == nil || appID == "" {
		return ""
	}
	c, err := r.Cookie(sessionAffinityCookieName)
	if err != nil || len(c.Value) == 0 || len(c.Value) > 2048 {
		return ""
	}
	block, err := aes.NewCipher(h.sessionAffinityKey[:])
	if err != nil {
		return ""
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return ""
	}
	sealed, err := base64.RawURLEncoding.DecodeString(c.Value)
	if err != nil || len(sealed) <= gcm.NonceSize() {
		return ""
	}
	nonce, ciphertext := sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, []byte("gregale-session-affinity-v1"))
	if err != nil {
		return ""
	}
	wantPrefix := appID + "\x00"
	if !strings.HasPrefix(string(plaintext), wantPrefix) {
		return ""
	}
	instanceID := strings.TrimPrefix(string(plaintext), wantPrefix)
	if instanceID == "" || strings.ContainsAny(instanceID, "\x00\r\n") {
		return ""
	}
	return instanceID
}

func (h *Handler) sessionAffinityCookieValue(appID, instanceID string) (string, error) {
	if h == nil || !h.sessionAffinityKeyValid || appID == "" || instanceID == "" {
		return "", errors.New("empty session affinity identity")
	}
	block, err := aes.NewCipher(h.sessionAffinityKey[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	plaintext := []byte(appID + "\x00" + instanceID)
	sealed := append(nonce, gcm.Seal(nil, nonce, plaintext, []byte("gregale-session-affinity-v1"))...)
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (h *Handler) setSessionAffinityCookie(w http.ResponseWriter, appID, instanceID string) {
	value, err := h.sessionAffinityCookieValue(appID, instanceID)
	if err != nil {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionAffinityCookieName,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}
