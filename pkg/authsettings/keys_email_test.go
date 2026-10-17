package authsettings

import (
	"crypto/ecdh"
	"crypto/x509"
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

func TestSMSSettings(t *testing.T) {
	set, unset, err := Change{SMS: &SMS{Provider: "twilio", Account: "AC123", Sender: "MG456", Secret: "auth-token"}}.Secrets()
	want := map[string]string{"GOTRUE_EXTERNAL_PHONE_ENABLED": "true", "GOTRUE_SMS_PROVIDER": "twilio", "GOTRUE_SMS_TWILIO_ACCOUNT_SID": "AC123",
		"GOTRUE_SMS_TWILIO_MESSAGE_SERVICE_SID": "MG456", "GOTRUE_SMS_TWILIO_AUTH_TOKEN": "auth-token"}
	if err != nil || !reflect.DeepEqual(set, want) {
		t.Fatalf("twilio: set %v err %v", set, err)
	}
	// Switching providers removes every other provider's credentials.
	for _, stale := range []string{"GOTRUE_SMS_VONAGE_API_SECRET", "GOTRUE_SMS_MESSAGEBIRD_ACCESS_KEY", "GOTRUE_SMS_TWILIO_VERIFY_AUTH_TOKEN", "GOTRUE_SMS_TEXTLOCAL_API_KEY"} {
		if !strings.Contains(strings.Join(unset, ","), stale) {
			t.Errorf("%s not removed: %v", stale, unset)
		}
	}
	if strings.Contains(strings.Join(unset, ","), "GOTRUE_SMS_TWILIO_AUTH_TOKEN") {
		t.Fatalf("the chosen provider's token is removed: %v", unset)
	}
	set, _, err = Change{SMS: &SMS{Provider: "messagebird", Sender: "Shop", Secret: "live_key"}}.Secrets()
	if err != nil || set["GOTRUE_SMS_MESSAGEBIRD_ORIGINATOR"] != "Shop" || set["GOTRUE_SMS_MESSAGEBIRD_ACCESS_KEY"] != "live_key" {
		t.Fatalf("messagebird: %v %v", set, err)
	}
	set, unset, err = Change{SMS: &SMS{}}.Secrets()
	if err != nil || set["GOTRUE_EXTERNAL_PHONE_ENABLED"] != "false" || !strings.Contains(strings.Join(unset, ","), "GOTRUE_SMS_PROVIDER") || !strings.Contains(strings.Join(unset, ","), "GOTRUE_SMS_TWILIO_AUTH_TOKEN") {
		t.Fatalf("off: set %v unset %v err %v", set, unset, err)
	}
	for _, bad := range []SMS{
		{Provider: "sinch", Sender: "s", Secret: "x"},
		{Provider: "twilio", Sender: "MG456", Secret: "token"},
		{Provider: "vonage", Account: "key", Sender: "Shop Inc", Secret: "secret"},
		{Provider: "textlocal", Account: "acct", Sender: "SHOP", Secret: "key"},
		{Provider: "textlocal", Sender: "SHOP"},
		{Provider: "vonage", Account: "key", Sender: "Shop", Secret: "bad\nsecret"},
	} {
		if _, _, err := (Change{SMS: &bad}).Secrets(); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}

func TestNewSAMLKeyIsWhatGoTrueAccepts(t *testing.T) {
	value, err := NewSAMLKey()
	if err != nil {
		t.Fatal(err)
	}
	der, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	key, err := x509.ParsePKCS1PrivateKey(der)
	if err != nil || key.E != 65537 || key.N.BitLen() < 2048 {
		t.Fatalf("SAML key: %v", err)
	}
	on := true
	if set, _, err := (Change{SSO: &on}).Secrets(); err != nil || set["AUTH_SSO"] != "1" {
		t.Fatalf("SSO on = %v %v", set, err)
	}
}
