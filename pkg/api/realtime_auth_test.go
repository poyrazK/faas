package api

import "testing"

func TestValidateRealtimeAuthOIDCPolicy(t *testing.T) {
	err := ValidateRealtimeAuth(
		RealtimeAuthModeOIDCJWT,
		false,
		"https://issuer.example",
		"https://issuer.example/.well-known/jwks.json",
		[]string{"realtime"},
		[]string{"RS256"},
		map[string]string{"tenant": "acme"},
	)
	if err != nil {
		t.Fatal(err)
	}
}

func TestValidateRealtimeAuthRejectsPrivateJWKS(t *testing.T) {
	if err := ValidateRealtimeAuth(
		RealtimeAuthModeOIDCJWT, false, "https://issuer.example", "https://127.0.0.1/jwks", nil, []string{"RS256"}, nil,
	); err == nil {
		t.Fatal("private JWKS URL unexpectedly accepted")
	}
}

func TestNormalizeRealtimeAuthModeLegacyDefaults(t *testing.T) {
	mode, err := NormalizeRealtimeAuthMode("", true)
	if err != nil || mode != RealtimeAuthModeStaticBearer {
		t.Fatalf("mode=%q err=%v, want static_bearer", mode, err)
	}
	mode, err = NormalizeRealtimeAuthMode("", false)
	if err != nil || mode != RealtimeAuthModeNone {
		t.Fatalf("mode=%q err=%v, want none", mode, err)
	}
}
