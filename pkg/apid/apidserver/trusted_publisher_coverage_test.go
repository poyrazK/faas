package apidserver

import (
	"bytes"
	"strings"
	"testing"
)

// TestCoverageSlicePublisherFilename pins the trusted-publisher file
// naming convention. The file lands at <appID>--<signer>.pem in the
// spool root; the convention is part of the §11 security boundary.
func TestCoverageSlicePublisherFilename(t *testing.T) {
	if got, want := publisherFilename("app-123", "signer-abc"), "app-123--signer-abc.pem"; got != want {
		t.Errorf("publisherFilename = %q, want %q", got, want)
	}
}

// TestCoverageSlicePEMEnveloped pins the PEM-envelope helper that
// wraps raw DER bytes for cosign.LoadPublicKeyFile. The envelope
// shape must include BEGIN/END markers + 64-char line wrapping.
func TestCoverageSlicePEMEnveloped(t *testing.T) {
	der := bytes.Repeat([]byte{0xAB}, 80) // 80 bytes → 108 base64 chars
	out := pemEnveloped(der)
	s := string(out)
	if !strings.HasPrefix(s, "-----BEGIN PUBLIC KEY-----") {
		t.Error("PEM envelope missing BEGIN header")
	}
	if !strings.HasSuffix(s, "-----END PUBLIC KEY-----\n") {
		t.Error("PEM envelope missing END footer")
	}
	// Empty input also produces a well-formed envelope.
	if out := pemEnveloped(nil); len(out) < 50 {
		t.Errorf("pemEnveloped(nil) length = %d, want >= 50", len(out))
	}
}
