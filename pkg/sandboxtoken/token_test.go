// adr: 20261027090013 §3 — a sandbox token grants calls on one session of one
// account and tenant until it expires; a forged, altered, expired or
// unknown-key token grants nothing; only the issuer can sign, so a verifier
// (a gateway) cannot mint one (ADR-20261102191500).
package sandboxtoken_test

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/sandboxtoken"
)

func TestTokenGrantsItsSessionUntilExpiry(t *testing.T) {
	issuer, err := sandboxtoken.NewIssuer(bytes.Repeat([]byte{3}, 32))
	if err != nil {
		t.Fatal(err)
	}
	again, _ := sandboxtoken.NewIssuer(bytes.Repeat([]byte{3}, 32))
	if again.PublicKey().ID != issuer.PublicKey().ID {
		t.Fatal("the same secret derived another key")
	}
	verifier := sandboxtoken.NewVerifier(issuer.PublicKey())
	now := time.Unix(1_800_000_000, 0).UTC()
	claims := sandboxtoken.Claims{SandboxID: "sb-1", AccountID: "acct-1", TenantID: "tenant-1", IssuedAt: now, ExpiresAt: now.Add(time.Minute),
		NodeID: "node-a", InstanceID: "vm-1", ExecTimeoutMaxMillis: 300_000, ExecOutputMaxBytes: 1 << 20}
	token, err := issuer.Sign(claims)
	if err != nil {
		t.Fatal(err)
	}
	got, err := verifier.Verify(token, now.Add(30*time.Second))
	if err != nil || got != claims || !got.Placed() {
		t.Fatalf("verify = %+v, %v; want %+v", got, err, claims)
	}
	if (sandboxtoken.Claims{SandboxID: "sb-1", NodeID: "node-a"}).Placed() {
		t.Fatal("a token without an instance routes to a VM")
	}

	other, _ := sandboxtoken.NewIssuer(bytes.Repeat([]byte{4}, 32))
	forged, _ := other.Sign(claims)
	parts := strings.Split(token, ".")
	flipped := "A"
	if strings.HasSuffix(parts[3], "A") {
		flipped = "B"
	}
	for name, tc := range map[string]struct {
		verifier *sandboxtoken.Verifier
		token    string
		at       time.Time
	}{
		"expired":           {verifier, token, now.Add(time.Minute)},
		"another issuer":    {verifier, forged, now},
		"no keys":           {&sandboxtoken.Verifier{}, token, now},
		"altered payload":   {verifier, strings.Join([]string{parts[0], parts[1], parts[2] + "A", parts[3]}, "."), now},
		"another key id":    {verifier, strings.Join([]string{parts[0], other.PublicKey().ID, parts[2], parts[3]}, "."), now},
		"another format":    {verifier, "sbx1." + strings.Join(parts[1:], "."), now},
		"no signature":      {verifier, strings.Join(parts[:3], "."), now},
		"oversized":         {verifier, token + strings.Repeat("A", 1024), now},
		"empty":             {verifier, "", now},
		"another signature": {verifier, strings.Join([]string{parts[0], parts[1], parts[2], parts[3][:len(parts[3])-1] + flipped}, "."), now},
	} {
		if _, err := tc.verifier.Verify(tc.token, tc.at); !errors.Is(err, sandboxtoken.ErrToken) {
			t.Errorf("%s: verify = %v, want ErrToken", name, err)
		}
	}
	if _, err := sandboxtoken.NewIssuer([]byte("short")); err == nil {
		t.Fatal("a short secret was accepted")
	}
}

func TestVerifierKeysRotateAndRefuseMislabelledKeys(t *testing.T) {
	a, _ := sandboxtoken.NewIssuer(bytes.Repeat([]byte{5}, 32))
	b, _ := sandboxtoken.NewIssuer(bytes.Repeat([]byte{6}, 32))
	now := time.Unix(1_800_000_000, 0)
	claims := sandboxtoken.Claims{SandboxID: "sb-1", AccountID: "acct-1", ExpiresAt: now.Add(time.Minute)}
	tokenA, _ := a.Sign(claims)
	tokenB, _ := b.Sign(claims)
	v := sandboxtoken.NewVerifier(a.PublicKey(), b.PublicKey())
	if !v.Ready() {
		t.Fatal("a verifier with keys is not ready")
	}
	for _, tok := range []string{tokenA, tokenB} {
		if _, err := v.Verify(tok, now); err != nil {
			t.Fatalf("during rotation: %v", err)
		}
	}
	v.SetKeys([]sandboxtoken.PublicKey{b.PublicKey()})
	if _, err := v.Verify(tokenA, now); !errors.Is(err, sandboxtoken.ErrToken) {
		t.Fatal("a retired key still verifies")
	}
	// A key published under another key's ID is dropped.
	v.SetKeys([]sandboxtoken.PublicKey{{ID: a.PublicKey().ID, Key: b.PublicKey().Key}, {ID: "x", Key: ed25519.PublicKey("short")}})
	if v.Ready() {
		t.Fatal("mislabelled keys were kept")
	}
}

// adr: 20261027090013 §11 — a preview token grants one port of one session
// and is never accepted as a data token, nor a data token as a preview token.
func TestPreviewTokensAreTheirOwnKind(t *testing.T) {
	issuer, _ := sandboxtoken.NewIssuer(bytes.Repeat([]byte{4}, 32))
	verifier := sandboxtoken.NewVerifier(issuer.PublicKey())
	now := time.Unix(1_800_000_000, 0).UTC()
	preview := sandboxtoken.PreviewClaims{SandboxID: "sb-1", AccountID: "acct-1", Port: 3000, IssuedAt: now, ExpiresAt: now.Add(time.Hour)}
	token, err := issuer.SignPreview(preview)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := verifier.VerifyPreview(token, now); err != nil || got != preview {
		t.Fatalf("verify preview = %+v, %v", got, err)
	}
	if _, err := verifier.Verify(token, now); !errors.Is(err, sandboxtoken.ErrToken) {
		t.Fatalf("a preview token passed as a data token: %v", err)
	}
	if _, err := verifier.VerifyPreview(token, now.Add(time.Hour)); !errors.Is(err, sandboxtoken.ErrToken) {
		t.Fatalf("an expired preview token passed: %v", err)
	}
	data, _ := issuer.Sign(sandboxtoken.Claims{SandboxID: "sb-1", AccountID: "acct-1", IssuedAt: now, ExpiresAt: now.Add(time.Hour)})
	if _, err := verifier.VerifyPreview(data, now); !errors.Is(err, sandboxtoken.ErrToken) {
		t.Fatalf("a data token passed as a preview token: %v", err)
	}
	if bad, _ := issuer.SignPreview(sandboxtoken.PreviewClaims{SandboxID: "sb-1", AccountID: "acct-1", ExpiresAt: now.Add(time.Hour)}); bad != "" {
		if _, err := verifier.VerifyPreview(bad, now); !errors.Is(err, sandboxtoken.ErrToken) {
			t.Fatalf("a preview token without a port passed: %v", err)
		}
	}
}
