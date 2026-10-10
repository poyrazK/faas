package realtimepush

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"testing"
)

func TestVAPIDKeySigningRoundTrip(t *testing.T) {
	original, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := original.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := vapidKey(base64.RawURLEncoding.EncodeToString(raw))
	if err != nil {
		t.Fatal(err)
	}
	if !parsed.PublicKey.Equal(&original.PublicKey) {
		t.Fatal("parsed key changed the public key")
	}
	digest := sha256.Sum256([]byte("VAPID notification"))
	signature, err := ecdsa.SignASN1(rand.Reader, parsed, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	if !ecdsa.VerifyASN1(&original.PublicKey, digest[:], signature) {
		t.Fatal("parsed key signature did not verify")
	}
}

func TestVAPIDKeyRejectsInvalidSecrets(t *testing.T) {
	overflow := make([]byte, 32)
	for i := range overflow {
		overflow[i] = 0xff
	}
	for name, raw := range map[string]string{
		"encoding": "!invalid!",
		"short":    base64.RawURLEncoding.EncodeToString(make([]byte, 31)),
		"long":     base64.RawURLEncoding.EncodeToString(make([]byte, 33)),
		"zero":     base64.RawURLEncoding.EncodeToString(make([]byte, 32)),
		"overflow": base64.RawURLEncoding.EncodeToString(overflow),
	} {
		t.Run(name, func(t *testing.T) {
			if key, err := vapidKey(raw); err == nil || key != nil {
				t.Fatal("invalid secret accepted")
			}
		})
	}
}
