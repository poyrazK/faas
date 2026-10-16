package authsettings

import (
	"crypto/ecdh"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestNewSigningKeyIsAnES256PrivateJWK(t *testing.T) {
	id, value, err := NewSigningKey()
	if err != nil {
		t.Fatal(err)
	}
	if !KeyID.MatchString(id) || !SigningKeyName.MatchString(SigningKeyPrefix+id) {
		t.Fatalf("key ID %q", id)
	}
	var jwk map[string]string
	if err := json.Unmarshal([]byte(value), &jwk); err != nil {
		t.Fatal(err)
	}
	if jwk["kty"] != "EC" || jwk["crv"] != "P-256" || jwk["alg"] != "ES256" || jwk["kid"] != "gregale-"+strings.ToLower(id) {
		t.Fatalf("jwk = %v", jwk)
	}
	d, _ := base64.RawURLEncoding.DecodeString(jwk["d"])
	private, err := ecdh.P256().NewPrivateKey(d)
	if err != nil {
		t.Fatal(err)
	}
	point := private.PublicKey().Bytes()
	if base64.RawURLEncoding.EncodeToString(point[1:33]) != jwk["x"] || base64.RawURLEncoding.EncodeToString(point[33:]) != jwk["y"] {
		t.Fatal("public point does not match the private key")
	}
}

func TestSealedKeysReadSecretNames(t *testing.T) {
	created := map[string]string{
		"AUTH_JWT_KEY_FFFF0001":    "2026-10-16T10:00:00Z",
		"AUTH_JWT_KEY_0A1B2C3D":    "2026-10-13T10:00:00Z",
		"AUTH_JWT_SIGNER_FFFF0001": "2026-10-16T10:05:00Z",
		"AUTH_JWT_SECRET":          "2026-10-13T10:00:00Z",
		"AUTH_JWT_KEY_lowercase":   "2026-10-13T10:00:00Z",
	}
	want := []SealedKey{{ID: "0A1B2C3D", CreatedAt: "2026-10-13T10:00:00Z"}, {ID: "FFFF0001", CreatedAt: "2026-10-16T10:00:00Z", Signer: true}}
	if got := SealedKeys(created); !reflect.DeepEqual(got, want) {
		t.Fatalf("SealedKeys = %+v", got)
	}
	if got := SignerMarkers([]string{"AUTH_JWT_SIGNER_FFFF0001", "AUTH_JWT_KEY_FFFF0001", "AUTH_JWT_SIGNER_0A1B2C3D"}); !reflect.DeepEqual(got, []string{"AUTH_JWT_SIGNER_FFFF0001", "AUTH_JWT_SIGNER_0A1B2C3D"}) {
		t.Fatalf("SignerMarkers = %v", got)
	}
}

func TestEmailSettings(t *testing.T) {
	set, unset, err := Change{Email: &Email{Provider: "postmark", From: "Shop <auth@shop.example.com>", APIKey: "server-token-1"}}.Secrets()
	want := map[string]string{"AUTH_EMAIL_PROVIDER": "postmark", "AUTH_EMAIL_FROM": "Shop <auth@shop.example.com>", "AUTH_EMAIL_API_KEY": "server-token-1"}
	if err != nil || !reflect.DeepEqual(set, want) || len(unset) != 0 {
		t.Fatalf("set %v unset %v err %v", set, unset, err)
	}
	set, unset, err = Change{Email: &Email{}}.Secrets()
	if err != nil || len(set) != 0 || !reflect.DeepEqual(unset, EmailSecrets) {
		t.Fatalf("removal: set %v unset %v err %v", set, unset, err)
	}
	for _, bad := range []Email{
		{Provider: "smtp", From: "auth@shop.example.com", APIKey: "re_test_key"},
		{Provider: "resend", From: "not an address", APIKey: "re_test_key"},
		{Provider: "resend", From: "auth@localhost", APIKey: "re_test_key"},
		{Provider: "resend", From: "a@b.c (Comment)", APIKey: "re_test_key"},
		{Provider: "resend", From: "Shop <auth@shop.example.com>\r\nBcc: x@y.z", APIKey: "re_test_key"},
		{Provider: "resend", From: "Shop <auth x@shop.example.com>", APIKey: "re_test_key"},
		{Provider: "resend", From: strings.Repeat("n", 101) + " <auth@shop.example.com>", APIKey: "re_test_key"},
		{Provider: "resend", From: "auth@shop.example.com", APIKey: "short"},
		{Provider: "resend", From: "auth@shop.example.com", APIKey: "has a space"},
		{Provider: "resend", From: "auth@shop.example.com", APIKey: strings.Repeat("k", 513)},
	} {
		if _, _, err := (Change{Email: &bad}).Secrets(); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}
