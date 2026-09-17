package oauthpkce

import (
	"strings"
	"testing"
)

func TestS256VerifierAndChallenge(t *testing.T) {
	verifier, challenge, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(verifier) != 43 {
		t.Fatalf("verifier length = %d, want 43", len(verifier))
	}
	if len(challenge) != 43 {
		t.Fatalf("challenge length = %d, want 43", len(challenge))
	}
	if !Verify(verifier, challenge) {
		t.Fatal("Verify rejected the generated S256 pair")
	}
	if Verify(verifier+"a", challenge) {
		t.Fatal("Verify accepted a different verifier")
	}
	tamperedLast := byte('a')
	if challenge[len(challenge)-1] == tamperedLast {
		tamperedLast = 'b'
	}
	if Verify(verifier, challenge[:len(challenge)-1]+string(tamperedLast)) {
		t.Fatal("Verify accepted a tampered challenge")
	}
}

func TestVerifyRejectsInvalidVerifierLength(t *testing.T) {
	if Verify("short", "challenge") {
		t.Fatal("Verify accepted a verifier shorter than RFC 7636 minimum")
	}
}

func TestValidVerifierRejectsReservedCharacters(t *testing.T) {
	if ValidVerifier(strings.Repeat("a", 43) + "+") {
		t.Fatal("ValidVerifier accepted a reserved verifier character")
	}
}
